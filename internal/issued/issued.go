// Package issued merkt sich, je verifizierter Aufrufer-Identität, welche
// Dokument-, Kontakt- und Reminder-IDs dieser Server tatsächlich über ein
// Lese-Werkzeug ausgeliefert hat — und lässt ein destruktives Werkzeug,
// bevor es handelt, fragen, ob die ihm übergebene ID eine davon ist.
//
// Der Grund, warum es das überhaupt braucht, ist ADR-0013 Punkt 3:
// Dokumentinhalte sind fremdbestimmte Daten (den Text schreibt, wer das
// Dokument verschickt hat, nicht wer diesen Server benutzt) — eine ID, die
// nur im Text eines Dokuments auftaucht, etwa in einem in eine Rechnung
// eingebetteten Prompt-Injection-Versuch, darf deshalb nie ein zulässiges
// Ziel für eine mutierende Operation sein. Destruktive Werkzeuge an IDs zu
// binden, die dieser Server selbst über einen vorangegangenen, echten
// Lese-Schritt ausgeliefert hat, schließt genau diese Lücke: eine ID, die
// das Modell nur als Text in einem fremden Dokument gesehen hat, wurde nie
// per Record aufgenommen — Check lehnt sie deshalb genauso ab wie jede
// andere unbekannte ID.
//
// Die Identitätsbindung folgt derselben Regel, die clientFor
// (internal/tools/read.go) bereits für die Konto-Auflösung etabliert hat
// (ADR-0012): der Aufrufer kommt ausschließlich aus
// serve.IdentityFrom(ctx), Gangways zustandslosem Pro-Anfrage-Blick auf
// das verifizierte Token — nie gecacht, nie durch eine feste Identität
// ersetzt. Unter ADR-0015s erzwungener Zustandslosigkeit öffnet und
// schließt eine Gangway-Sitzung pro Anfrage neu, eine sitzungsgebundene
// Merkliste könnte sich also nie über den einzelnen Aufruf hinaus etwas
// merken — dieses Paket schlüsselt stattdessen über das Subject der
// verifizierten Identität, genau wie ADR-0013 Punkt 3 es verlangt.
//
// Diese Datei trägt Aufnahme und Prüfung ohne jeden Begriff von Verfall
// oder einer Obergrenze je Identität — New nimmt beides (ttl und
// maxPerIdentity) bereits entgegen, damit eine spätere Änderung, die
// beides tatsächlich auswertet, hier keine Signaturänderung mehr braucht;
// beide Felder liegen vorerst ungenutzt, jeweils an ihrer eigenen
// Deklaration dokumentiert.
package issued

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/strausmann/gangway/serve"
)

// ErrNotIssued meldet, dass eine ID aktuell nicht als der aufrufenden
// Identität ausgeliefert gilt — entweder weil dieser Server sie nie an
// irgendjemanden ausgeliefert hat, oder weil sie an eine andere Identität
// ausgeliefert wurde. Aufrufer prüfen mit errors.Is darauf; Checks eigener
// Fehler, der diesen hier einwickelt, macht bewusst keinen Unterschied
// über "nicht ausgeliefert" hinaus (siehe Checks eigenen Doc-Kommentar).
var ErrNotIssued = errors.New("fileee-mcp: issued: id was not handed out to this identity")

// Store merkt sich, je verifizierter Aufrufer-Identität, welche IDs dieser
// Server über ein Lese-Werkzeug ausgeliefert hat.
//
// Der Nullwert ist nicht benutzbar; einen Store baut man mit New.
type Store struct {
	// ttl begrenzt, wie lange eine aufgenommene ID gültig bleibt. Noch
	// nicht ausgewertet — Record setzt nie einen Verfall und Check fragt
	// nie einen ab; eine aufgenommene ID gilt Stand dieser Datei
	// unbegrenzt (innerhalb der Prozesslaufzeit). Ttl tatsächlich
	// durchzusetzen ist eine spätere Änderung (siehe den Doc-Kommentar
	// dieses Pakets).
	ttl time.Duration

	// maxPerIdentity begrenzt, wie viele IDs der Eimer einer einzelnen
	// Identität gleichzeitig halten darf. Noch nicht ausgewertet — Record
	// erzwingt keine Obergrenze; der Eimer eines Aufrufers wächst Stand
	// dieser Datei unbegrenzt. Diese Obergrenze durchzusetzen ist eine
	// spätere Änderung (siehe den Doc-Kommentar dieses Pakets).
	maxPerIdentity int

	mu sync.Mutex
	// byIdent bildet das Subject einer verifizierten Identität auf die
	// Menge der für sie aufgenommenen IDs ab, jede mit der time.Time
	// ihrer Aufnahme. Die aufgenommene Zeit wird von nichts in dieser
	// Datei gelesen (siehe ttl oben), wird aber schon jetzt festgehalten,
	// damit eine spätere Verfallsprüfung keine Änderung an Records eigener
	// Logik braucht, nur an der von Check.
	byIdent map[string]map[string]time.Time
}

// New baut einen Store. ttl und maxPerIdentity werden entgegengenommen und
// gespeichert, aber Stand dieser Datei von Record oder Check noch nicht
// ausgewertet — siehe deren eigene Doc-Kommentare und den Doc-Kommentar
// dieses Pakets.
func New(ttl time.Duration, maxPerIdentity int) *Store {
	return &Store{
		ttl:            ttl,
		maxPerIdentity: maxPerIdentity,
		byIdent:        map[string]map[string]time.Time{},
	}
}

// subjectOf löst das Subject des verifizierten Aufrufers aus ctx auf,
// genau wie clientFor (internal/tools/read.go) den Aufrufer für die
// Konto-Auflösung auflöst: ausschließlich über serve.IdentityFrom(ctx),
// nie gecacht, nie ersetzt. Der false-Rückgabewert ist dieses Pakets
// einziges Fail-Closed-Tor — erreicht, sobald ctx gar keine Identität
// trägt, eine nil-Identität, oder eine Identität mit leerem Subject — und
// Record wie Check behandeln ihn identisch: keine Identität heißt, dass
// weder "das merken" noch "das durchlassen" je zutrifft.
func subjectOf(ctx context.Context) (string, bool) {
	id, ok := serve.IdentityFrom(ctx)
	if !ok || id == nil || id.Subject == "" {
		return "", false
	}
	return id.Subject, true
}

// Record markiert ids als an ctx' verifizierten Aufrufer ausgeliefert, so
// dass ein späteres Check für dieselbe Identität und eine dieser ids
// gelingt.
//
// Ohne verifizierte Identität in ctx tut Record überhaupt nichts — nicht
// einmal in einen gemeinsamen, identitätslosen Eimer (das würde den
// ganzen Zweck unterlaufen: eine unauthentifizierte Aufnahme ließe sich
// nie von einer im Auftrag eines echten Aufrufers unterscheiden). Leere
// ids werden übersprungen; eine doppelte id (für diese Identität schon
// aufgenommen) bekommt einfach ihre Aufnahmezeit aufgefrischt, was nie ein
// Fehler ist.
func (s *Store) Record(ctx context.Context, ids ...string) {
	subject, ok := subjectOf(ctx)
	if !ok {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	bucket := s.byIdent[subject]
	if bucket == nil {
		bucket = map[string]time.Time{}
		s.byIdent[subject] = bucket
	}
	now := time.Now()
	for _, id := range ids {
		if id == "" {
			continue
		}
		bucket[id] = now
	}
}

// Check meldet, ob id als an ctx' verifizierten Aufrufer ausgeliefert
// gilt — nil, wenn ein vorheriger Record-Aufruf id für genau diese
// Identität aufgenommen hat, sonst ein Fehler, der ErrNotIssued
// einwickelt.
//
// Der Fehler ist über "nicht ausgeliefert" hinaus bewusst nichtssagend: er
// gibt id nie zurück, und er unterscheidet nie "diese id wurde für
// niemanden je aufgenommen" von "sie wurde aufgenommen, aber für eine
// andere Identität" oder (sobald eine spätere Änderung ttl durchsetzt)
// "sie wurde aufgenommen, ist inzwischen aber verfallen". Jede dieser
// Unterscheidungen würde einem Aufrufer erlauben, nach der Existenz von
// IDs zu forschen, die jemand anderem gehören — genau das, wovor diese
// Whitelist schützen soll (ADR-0013 Punkt 3; siehe den Doc-Kommentar
// dieses Pakets). Ohne verifizierte Identität in ctx, oder für eine leere
// id, scheitert Check auf demselben Weg: fail-closed, derselbe Fehler,
// dieselbe fehlende Detailtiefe.
//
// Der id == ""-Frühausstieg unten ist Tiefenverteidigung, keine alleinige
// Absicherung: Record nimmt leere ids ohnehin nie auf (siehe Records
// eigenen Doc-Kommentar), eine leere id landet also über die öffentliche
// API sowieso nie im Bucket, und der Nachschlag weiter unten würde sie
// auch ohne diesen Frühausstieg als "nicht aufgenommen" behandeln. Der
// Frühausstieg greift trotzdem eigenständig, falls je ein anderer Weg
// (heute keiner) eine leere id in byIdent ablegen würde — siehe
// issued_test.go, TestCheckLehntLeereIDAbAuchWennSieImBucketStuende, wo
// genau das isoliert geprüft wird.
func (s *Store) Check(ctx context.Context, id string) error {
	subject, ok := subjectOf(ctx)
	if !ok || id == "" {
		return errNotIssuedFor()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, recorded := s.byIdent[subject][id]; recorded {
		return nil
	}
	return errNotIssuedFor()
}

// errNotIssuedFor baut Checks nach außen sichtbaren Fehler: englisch, weil
// er als Werkzeug-Fehler beim aufrufenden Modell ankommt (Doc-Kommentar
// dieses Pakets; CONTRIBUTING.md zu nutzersichtbarem Text), wickelt
// ErrNotIssued ein, damit Aufrufer mit errors.Is dagegen prüfen können,
// und nennt den Weg nach vorn statt nur die Ablehnung.
func errNotIssuedFor() error {
	return fmt.Errorf(
		"this id was not handed out by a read tool in this session; fetch it first "+
			"(for example via list_documents or get_document) and retry: %w",
		ErrNotIssued,
	)
}
