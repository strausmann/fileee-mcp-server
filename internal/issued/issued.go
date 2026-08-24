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
// Eine aufgenommene ID gilt nicht unbegrenzt: sie verfällt nach ttl (siehe
// Store.ttl) und der Eimer je Identität hält höchstens maxPerIdentity IDs
// gleichzeitig (siehe Store.maxPerIdentity) — beide Grenzen wertet Record
// bzw. Check tatsächlich aus, keine der beiden ist mehr nur entgegengenommen
// und ungenutzt. Die Uhr, die beide Grenzen dabei befragen, ist über
// Store.now austauschbar; SetClock ist der dafür vorgesehene Test-Seam
// (siehe dessen eigenen Doc-Kommentar) — der Produktionspfad bleibt bei
// time.Now, New setzt es genau einmal, kein Aufrufer außerhalb eines Tests
// ruft SetClock je auf.
package issued

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/strausmann/gangway/serve"
)

// ErrNotIssued meldet, dass eine ID aktuell nicht als der aufrufenden
// Identität ausgeliefert gilt — entweder weil dieser Server sie nie an
// irgendjemanden ausgeliefert hat, weil sie an eine andere Identität
// ausgeliefert wurde, oder weil ihre Gültigkeit inzwischen verfallen ist
// (Store.ttl) bzw. sie durch den Deckel je Identität (Store.maxPerIdentity)
// verdrängt wurde. Aufrufer prüfen mit errors.Is darauf; Checks eigener
// Fehler, der diesen hier einwickelt, macht bewusst keinen Unterschied
// zwischen diesen Fällen (siehe Checks eigenen Doc-Kommentar).
var ErrNotIssued = errors.New("fileee-mcp: issued: id was not handed out to this identity")

// Store merkt sich, je verifizierter Aufrufer-Identität, welche IDs dieser
// Server über ein Lese-Werkzeug ausgeliefert hat.
//
// Der Nullwert ist nicht benutzbar; einen Store baut man mit New.
type Store struct {
	// ttl begrenzt, wie lange eine aufgenommene ID gültig bleibt. Check
	// wertet ttl aus (isExpired): eine ID, deren Aufnahmezeit länger als
	// ttl zurückliegt, gilt als nicht ausgeliefert, genau wie eine nie
	// aufgenommene.
	//
	// Grenzfall ttl <= 0: bewusst NICHT als "unbegrenzt gültig" gelesen —
	// dieselbe Entscheidung, die dieses Repository für FILEEE_MAX_INFLIGHT
	// bereits getroffen hat (internal/server/ratelimit.go,
	// newToolCallLimiter: ein MaxInflight von 0 lässt
	// semaphore.Weighted(0) entstehen, TryAcquire schlägt dann IMMER fehl —
	// 0 wird als "alles ablehnen" durchgesetzt, nie als "unbegrenzt"
	// gelesen; internal/tools/write_documents.go übernimmt dieselbe
	// Konvention ausdrücklich für FILEEE_MAX_UPLOAD_BYTES). Ein ttl <= 0
	// wird hier spiegelbildlich als "sofort verfallen" durchgesetzt: jede
	// aufgenommene ID gilt ab dem Moment ihrer Aufnahme als abgelaufen,
	// unabhängig davon, wie viel Zeit seither vergangen ist (siehe
	// isExpired). Fail-closed: eine falsch konfigurierte oder vergessene
	// Ttl darf nie stillschweigend zu "jede ID bleibt für immer gültig"
	// werden — das würde genau die Schutzwirkung unterlaufen, die dieses
	// Paket laut Paket-Doc-Kommentar herstellen soll.
	ttl time.Duration

	// maxPerIdentity begrenzt, wie viele IDs der Eimer einer einzelnen
	// Identität gleichzeitig halten darf. Record wertet maxPerIdentity aus
	// (recordLocked): übersteigt ein Eimer nach der Aufnahme diese Grenze,
	// verdrängt Record so lange die jeweils älteste (kleinste Aufnahmezeit)
	// verbleibende ID, bis der Eimer wieder innerhalb der Grenze liegt. Die
	// Grenze gilt JE IDENTITÄT, nicht global — der Eimer einer Identität
	// verdrängt nie den einer anderen (siehe
	// TestDerDeckelGiltJeIdentitaetNichtGlobal).
	//
	// Grenzfall maxPerIdentity <= 0: wie bei ttl (siehe dort) bewusst NICHT
	// als "unbegrenzt" gelesen, sondern als reale, durchgesetzte Grenze —
	// dieselbe FILEEE_MAX_INFLIGHT-Konvention. Effektiv wird jeder Wert
	// <= 0 wie 0 behandelt (effectiveMax): nach jedem Record-Aufruf bleibt
	// für die betroffene Identität kein Eintrag übrig, d.h. es wird
	// nichts gemerkt. Fail-closed aus demselben Grund wie bei ttl.
	maxPerIdentity int

	// now liefert die von Record und Check verwendete "aktuelle" Zeit. New
	// setzt now auf time.Now — jeder Store, den ein Nicht-Test je baut,
	// behält das für seine gesamte Lebensdauer bei. SetClock überschreibt
	// now testweise mit einer steuerbaren Uhr (siehe dessen eigenen
	// Doc-Kommentar) — ohne diesen Seam bräuchte jeder Verfalls- oder
	// Verdrängungstest echtes Warten und wäre zeitabhängig-flaky.
	now func() time.Time

	mu sync.Mutex
	// byIdent bildet das Subject einer verifizierten Identität auf die
	// Menge der für sie aufgenommenen IDs ab, jede mit der time.Time ihrer
	// Aufnahme. Record schreibt diese Zeit über now(); Check liest sie
	// über isExpired, um eine verfallene ID wie eine nie aufgenommene zu
	// behandeln, und entfernt einen so erkannten Eintrag dabei gleich mit
	// (siehe Checks eigenen Doc-Kommentar für die Begründung, warum ein
	// eigener Aufräum-Durchlauf in Record nicht zusätzlich nötig ist).
	byIdent map[string]map[string]time.Time
}

// New baut einen Store. ttl begrenzt, wie lange eine aufgenommene ID gültig
// bleibt, maxPerIdentity, wie viele IDs der Eimer einer einzelnen Identität
// gleichzeitig halten darf — beide Grenzen wertet der gebaute Store
// tatsächlich aus (siehe deren eigene Feld-Doc-Kommentare für die
// Grenzfälle ttl <= 0 und maxPerIdentity <= 0, beide bewusst fail-closed,
// nie als "unbegrenzt" gelesen). now startet auf time.Now; SetClock ist der
// einzige Weg, das zu ändern, und ausschließlich für Tests gedacht (siehe
// dessen eigenen Doc-Kommentar).
func New(ttl time.Duration, maxPerIdentity int) *Store {
	return &Store{
		ttl:            ttl,
		maxPerIdentity: maxPerIdentity,
		now:            time.Now,
		byIdent:        map[string]map[string]time.Time{},
	}
}

// SetClock überschreibt die Uhr, die Record und Check befragen, um eine
// ID-Aufnahmezeit zu setzen bzw. gegen ttl zu vergleichen.
//
// Das ist AUSSCHLIESSLICH ein Test-Seam. Produktionscode ruft SetClock nie
// auf — New verdrahtet now bereits auf time.Now, und genau das behält jeder
// Store, den ein Nicht-Test baut, für seine gesamte Lebensdauer bei. Ein
// Test, der Verfall (ttl) oder die Verdrängungsreihenfolge des Deckels
// (maxPerIdentity) deterministisch prüfen will, ruft SetClock einmal,
// direkt nach New, mit einer selbst steuerbaren Uhr auf (siehe
// issued_test.go, testUhr) — ohne diesen Seam bräuchte ein solcher Test
// echtes time.Sleep und wäre zeitabhängig-flaky.
//
// now darf nicht nil sein: SetClock prüft das nicht, weil es ein reiner
// Test-Seam ist und ein nil-now beim nächsten Record/Check ohnehin sofort
// mit einer Nil-Pointer-Panik auffällt — eine zusätzliche Guard-Klausel
// hier würde nur einen Fehler verschleiern, den ein Test sofort selbst
// bemerkt.
func (s *Store) SetClock(now func() time.Time) {
	s.now = now
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
// gelingt — bis zu ttl lang (Store.ttl) und höchstens für die jüngsten
// maxPerIdentity IDs dieser Identität (Store.maxPerIdentity); übersteigt
// der Eimer nach dieser Aufnahme die Grenze, verdrängt Record die jeweils
// älteste verbleibende ID, bis er wieder innerhalb der Grenze liegt (siehe
// recordLocked).
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

	evicted := s.recordLocked(subject, ids)
	if evicted > 0 {
		// Debug-Protokolleintrag OHNE die verdrängte(n) ID(s) selbst —
		// internal/diag's Maskierungsgarantie (siehe dessen Paket-
		// Doc-Kommentar) greift nur auf Attribute, die tatsächlich durch
		// den dort gebauten Logger laufen; dieser hier läuft bewusst
		// durch log/slog's eigenen paketweiten Standard-Logger
		// (slog.DebugContext), nicht durch einen dieser Datei explizit
		// mitgegebenen — Store.New ändert dafür keine Signatur (siehe
		// Paket-Doc-Kommentar). Damit die ID selbst unter keinen
		// Umständen im Protokoll landet, wird sie hier erst gar nicht als
		// Attribut übergeben: "wie viele" statt "welche". Zweck laut
		// Auftrag: im Betrieb sichtbar machen, ob der Deckel je greift,
		// bevor die Ttl greift.
		slog.DebugContext(ctx, "issued: per-identity cap evicted the oldest recorded id(s)",
			"evicted_count", evicted,
			"max_per_identity", s.maxPerIdentity,
		)
	}
}

// recordLocked erledigt Records eigentliche Arbeit unter s.mu: id-Menge
// aufnehmen, dann so lange die jeweils älteste (kleinste Aufnahmezeit)
// verbleibende ID verdrängen, bis der Eimer die effektive Obergrenze
// (effectiveMax) nicht mehr übersteigt. Gibt zurück, wie viele Einträge
// dabei verdrängt wurden — 0, wenn keiner verdrängt werden musste.
//
// Eigene Funktion statt inline in Record: das Sperren soll NICHT den
// Protokollaufruf umschließen (ein slog-Aufruf ist ein möglicher
// Syscall/IO — ihn unter s.mu zu halten würde jeden gleichzeitigen
// Record/Check-Aufrufer unnötig blockieren). Record ruft diese Funktion
// auf, wertet erst danach — außerhalb jeder Sperre — aus, ob protokolliert
// werden muss.
func (s *Store) recordLocked(subject string, ids []string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	bucket := s.byIdent[subject]
	if bucket == nil {
		bucket = map[string]time.Time{}
		s.byIdent[subject] = bucket
	}
	now := s.now()
	for _, id := range ids {
		if id == "" {
			continue
		}
		bucket[id] = now
	}

	max := s.effectiveMax()
	var evicted int
	for len(bucket) > max {
		oldestID := evictionCandidate(bucket)
		delete(bucket, oldestID)
		evicted++
	}
	return evicted
}

// effectiveMax liest maxPerIdentity so, dass jeder Wert <= 0 wie 0
// behandelt wird — siehe Store.maxPerIdentitys eigenen Doc-Kommentar für
// die Begründung (fail-closed, wie FILEEE_MAX_INFLIGHT). Ohne diese
// Klammerung würde recordLockeds Verdrängungsschleife bei einem negativen
// maxPerIdentity nie terminieren: "len(bucket) > max" bliebe auch bei
// leerem Eimer (len 0) wahr, sobald max selbst negativ ist.
func (s *Store) effectiveMax() int {
	if s.maxPerIdentity < 0 {
		return 0
	}
	return s.maxPerIdentity
}

// evictionCandidate findet in bucket die ID mit der kleinsten Aufnahmezeit
// (die älteste) und gibt ihre ID zurück. Nur von recordLocked aufgerufen,
// selbst unter s.mu — bucket wird hier nur gelesen, nicht verändert.
//
// bucket ist an dieser Stelle garantiert nicht leer: recordLocked ruft
// diese Funktion nur innerhalb von "for len(bucket) > max", was len(bucket)
// >= 1 voraussetzt (max ist über effectiveMax nie negativ). Ein leeres
// bucket würde die Schleife über den nil-Anfangswert von oldestTime
// (time.Time{}, der Nullwert) verlassen — das tritt hier nie ein, ist
// aber, sollte sich das je ändern, keine Panik, sondern eine leere
// Rückgabe-ID, deren delete(bucket, "") ein No-op wäre.
func evictionCandidate(bucket map[string]time.Time) string {
	oldestID := ""
	var oldestTime time.Time
	first := true
	for id, recorded := range bucket {
		if first || recorded.Before(oldestTime) {
			oldestID, oldestTime = id, recorded
			first = false
		}
	}
	return oldestID
}

// Check meldet, ob id als an ctx' verifizierten Aufrufer ausgeliefert
// gilt — nil, wenn ein vorheriger Record-Aufruf id für genau diese
// Identität aufgenommen hat UND diese Aufnahme noch innerhalb von ttl
// liegt (siehe isExpired), sonst ein Fehler, der ErrNotIssued einwickelt.
//
// Der Fehler ist über "nicht ausgeliefert" hinaus bewusst nichtssagend: er
// gibt id nie zurück, und er unterscheidet nie "diese id wurde für
// niemanden je aufgenommen" von "sie wurde aufgenommen, aber für eine
// andere Identität", von "sie wurde aufgenommen, ist inzwischen aber
// verfallen" oder von "sie wurde durch den Deckel je Identität verdrängt".
// Jede dieser Unterscheidungen würde einem Aufrufer erlauben, nach der
// Existenz von IDs zu forschen, die jemand anderem gehören, oder nach dem
// zeitlichen Verlauf fremder Aufnahmen — genau das, wovor diese Whitelist
// schützen soll (ADR-0013 Punkt 3; siehe den Doc-Kommentar dieses Pakets).
// Ohne verifizierte Identität in ctx, oder für eine leere id, scheitert
// Check auf demselben Weg: fail-closed, derselbe Fehler, dieselbe fehlende
// Detailtiefe.
//
// Eine als verfallen erkannte ID wird IM VORBEIGEHEN aus dem Eimer
// entfernt (dieselbe Sperre, derselbe Aufruf) — ein eigener,
// zeitgesteuerter Aufräum-Durchlauf über byIdent ist bewusst NICHT
// zusätzlich gebaut: jeder Eimer ist durch maxPerIdentity ohnehin
// beschränkt (recordLocked verdrängt bei Überlauf sofort die älteste ID,
// verfallen hin oder her), und die Menge möglicher Identitäten ist über
// die Konfiguration beschränkt (spiegelt internal/server/ratelimit.go's
// eigene Begründung für toolCallLimiter.perSubject: MCP_ALLOWED_SUBJECTS
// im Single-Konto- bzw. die Vereinigung der FILEEE_ACCOUNT_<KEY>_SUBJECTS
// im Multi-Konto-Modus). Ein verfallener, aber noch nicht abgefragter
// Eintrag bleibt also höchstens bis zum nächsten Record-Aufruf für
// dieselbe Identität liegen (der ihn ggf. verdrängt) oder bis zum
// nächsten Check dafür (der ihn dann entfernt) — unbegrenztes Wachstum
// wäre nur möglich, wenn eine Identität unbegrenzt viele NEUE ids
// aufnehmen ließe, und genau das verhindert maxPerIdentity bereits
// unabhängig vom Verfall.
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

	bucket := s.byIdent[subject]
	recorded, wasRecorded := bucket[id]
	if !wasRecorded {
		return errNotIssuedFor()
	}
	if s.isExpired(recorded) {
		delete(bucket, id)
		return errNotIssuedFor()
	}
	return nil
}

// isExpired meldet, ob eine bei recorded aufgenommene ID inzwischen
// verfallen ist. Nur von Check aufgerufen, selbst unter s.mu.
//
// Grenzfall s.ttl <= 0: siehe Store.ttls eigenen Doc-Kommentar — wird
// bewusst als "sofort verfallen" durchgesetzt, unabhängig vom
// tatsächlichen Zeitabstand. Ohne diesen expliziten Vorab-Guard würde ein
// ttl von genau 0 einen Check, der im selben Moment wie der zugehörige
// Record läuft (s.now().Sub(recorded) == 0), fälschlich als NICHT
// verfallen werten ("0 > 0" ist false) — der Guard schließt genau diese
// Lücke, statt sich auf einen Zufall der Vergleichsrichtung zu verlassen.
func (s *Store) isExpired(recorded time.Time) bool {
	if s.ttl <= 0 {
		return true
	}
	return s.now().Sub(recorded) > s.ttl
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
