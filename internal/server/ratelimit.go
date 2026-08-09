package server

import (
	"context"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/semaphore"
	"golang.org/x/time/rate"

	"github.com/strausmann/fileee-mcp-server/internal/config"
	"github.com/strausmann/gangway/serve"
)

// methodCallTool ist die JSON-RPC-Methode eines Werkzeugaufrufs. Gangway
// haelt dieselbe Zeichenkette in einer eigenen, unexportierten Konstante
// (gangway/serve, methodCallTool) — dort mit derselben Begruendung: es ist
// Teil des Wire-Protokolls, keine SDK-Interna, das Duplizieren ist sicher.
const methodCallTool = "tools/call"

// CodeRateLimited ist der JSON-RPC-Fehlercode, mit dem toolCallLimiter einen
// abgelehnten Werkzeugaufruf meldet. -32010 ist Gangways CodeForbidden
// (gangway/serve, CodeForbidden) — dieser Code liegt bewusst daneben, im
// selben implementierungsdefinierten Bereich (-32000 bis -32019, siehe dort),
// aber unterscheidbar: "abgelehnt, weil zu viele Anfragen" ist ein anderer
// Sachverhalt als "abgelehnt, weil keine Berechtigung".
const CodeRateLimited int64 = -32011

// toolCallLimiter begrenzt Werkzeugaufrufe nach drei unabhaengigen, ALLE
// erfuellten Kriterien (UND-Verknuepfung, siehe acquire): eine globale
// Anfragerate (RateGlobalRPS/RateGlobalBurst), eine Anfragerate je Anrufer
// (RateRPS/RateBurst, geschluesselt auf das verifizierte Token-Subject —
// NICHT auf die Client-Adresse, siehe unten) und eine globale Obergrenze
// gleichzeitig laufender Aufrufe (MaxInflight).
//
// Warum das Subject und nicht die Adresse: Dieser Server laeuft hinter
// Pangolin und Traefik; welche Adresse ankommt, haengt von
// FILEEE_TRUSTED_PROXIES ab und ist bei falscher Einstellung sogar vom
// Anrufer selbst waehlbar (ein Weiterleitungs-Header laesst sich faelschen,
// solange der unmittelbare Absender als vertrauenswuerdig gilt). Eine
// Begrenzung auf so einem Schluessel waere wertlos — ein Anrufer koennte sie
// durch einen anderen Header-Wert je Anfrage umgehen. Das verifizierte
// Subject aus dem Token (serve.IdentityFrom) ist dagegen von Gangway bereits
// gegen die Signatur des Identity Providers geprueft und liegt ausserhalb
// der Kontrolle des Anrufers.
//
// Warum MaxInflight OHNE Anrufer-Bezug (anders als das Rate-Paar): Der
// Konto-Pool (clientpool.Pool) haelt genau eine Fileee-Verbindung je
// aufgeloestem Fileee-Konto vor, geteilt von jedem Anrufer, der auf dieses
// Konto abbildet (siehe clientpool-Dokumentation). Was diese gemeinsame
// Verbindung vor Ueberlastung schuetzt, ist die Gesamtzahl gleichzeitig
// laufender Aufrufe UEBER ALLE Anrufer hinweg, nicht wie viele ein
// einzelner Anrufer davon beitraegt — deshalb keine "MaxInflightGlobal"/
// "MaxInflightPerCaller"-Unterscheidung wie beim Rate-Paar, sondern ein
// einzelner, globaler Wert (Namensgebung in config.go: FILEEE_MAX_INFLIGHT
// hat kein Gegenstueck, anders als FILEEE_RATE_RPS/FILEEE_RATE_GLOBAL_RPS).
//
// Alle drei Gates lehnen SOFORT ab (Allow/TryAcquire, nie Wait/Acquire) statt
// die Anfrage bis zu einem freien Kontingent zu blockieren. Ein rufender
// Client — typischerweise ein Sprachmodell in einer Werkzeugaufruf-Schleife —
// bekommt damit umgehend ein auswertbares Ergebnis (CodeRateLimited) statt
// unbestimmt lange auf eine MCP-Antwort zu warten, was auf Client-Seite
// ebenso zu Zeitüberschreitungen fuehren koennte wie das eigentliche Problem,
// das die Begrenzung vermeiden soll.
type toolCallLimiter struct {
	global   *rate.Limiter
	inflight *semaphore.Weighted

	perSubjectRPS   rate.Limit
	perSubjectBurst int

	mu         sync.Mutex
	perSubject map[string]*rate.Limiter
}

// newToolCallLimiter baut den Limiter aus cfg. Er MUSS genau einmal in New()
// gebaut und an jede von buildInstances gebaute *mcp.Server-Instanz
// weitergereicht werden, NIE einer je Instanz: die globale Rate und
// MaxInflight sind serverweite Zusicherungen — mit einem Limiter je Instanz
// haette ein Anrufer, dessen Berechtigungsmenge zwischen zwei Aufrufen
// wechselt (z. B. weil sich der Rollen-Claim im Token aendert), bei jedem
// Wechsel ein frisches Kontingent, und die globale Rate waere in Wahrheit
// so viele getrennte globale Raten wie es Berechtigungsmengen gibt.
func newToolCallLimiter(cfg *config.Config) *toolCallLimiter {
	return &toolCallLimiter{
		global:          rate.NewLimiter(rate.Limit(cfg.RateGlobalRPS), cfg.RateGlobalBurst),
		inflight:        semaphore.NewWeighted(int64(cfg.MaxInflight)),
		perSubjectRPS:   rate.Limit(cfg.RateRPS),
		perSubjectBurst: cfg.RateBurst,
		perSubject:      make(map[string]*rate.Limiter),
	}
}

// limiterFor liefert den Rate-Limiter fuer subject, legt ihn beim ersten
// Zugriff an. Die Menge moeglicher Subjects ist durch die Konfiguration
// beschraenkt (MCP_ALLOWED_SUBJECTS im single-Modus, die Vereinigung aller
// FILEEE_ACCOUNT_<KEY>_SUBJECTS im multi-Modus — siehe config.go, ladeKonten)
// — diese Map waechst also nicht unbeschraenkt mit jedem neuen Anrufer, wie
// es bei einer Begrenzung auf Basis der Client-Adresse der Fall waere.
func (l *toolCallLimiter) limiterFor(subject string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	lim, ok := l.perSubject[subject]
	if !ok {
		lim = rate.NewLimiter(l.perSubjectRPS, l.perSubjectBurst)
		l.perSubject[subject] = lim
	}
	return lim
}

// errKind unterscheidet, WELCHES der drei Kriterien einen Aufruf abgelehnt
// hat — nur fuer Tests und Diagnose, die Wire-Antwort ist fuer alle drei
// identisch (CodeRateLimited).
type errKind int

const (
	errNone errKind = iota
	errGlobalRate
	errSubjectRate
	errInflight
)

// acquire prueft alle drei Kriterien der Reihe nach und lehnt beim ersten
// Fehlschlag sofort ab (kein Teil-Erwerb, der wieder freigegeben werden
// muesste): Die beiden Rate-Pruefungen sind zustandslose Allow()-Aufrufe
// ohne Aufraeumbedarf, MaxInflight wird deshalb bewusst zuletzt erworben.
// Bei Erfolg liefert acquire eine release-Funktion, die der Aufrufer IMMER
// aufrufen muss (per defer), sobald der Werkzeugaufruf fertig ist — sonst
// bleibt der Inflight-Platz belegt.
func (l *toolCallLimiter) acquire(subject string) (release func(), kind errKind) {
	if !l.global.Allow() {
		return nil, errGlobalRate
	}
	if !l.limiterFor(subject).Allow() {
		return nil, errSubjectRate
	}
	if !l.inflight.TryAcquire(1) {
		return nil, errInflight
	}
	return func() { l.inflight.Release(1) }, errNone
}

// middleware liefert die MCP-Receiving-Middleware, die jeden Werkzeugaufruf
// (tools/call) durch acquire schickt. Andere Methoden (initialize,
// tools/list, ...) sind unbegrenzt durch — sie loesen selbst keinen
// Fileee-Zugriff aus, eine Begrenzung dort schuetzt nichts.
//
// Muss VOR Gangways eigener Autorisierungs-Middleware auf der Instanz
// registriert werden (siehe New(): buildInstances laeuft vor
// AttachMCPSelector) — mcp.Server.AddReceivingMiddleware wickelt jede
// spaeter registrierte Middleware AUSSEN um die vorherigen (siehe deren
// Doc-Kommentar: "Middleware is applied from right to left, so that the
// first one is executed first" gilt fuer EINEN Aufruf mit mehreren
// Argumenten; bei mehreren AUFRUFEN wickelt jeder neue Aufruf um den
// bisherigen Stand). Mit dieser Reihenfolge laeuft Gangways
// Autorisierungspruefung ZUERST — ein wegen fehlender Berechtigung
// abgelehnter Aufruf verbraucht dann kein Kontingent dieses Limiters, weil
// er ihn nie erreicht.
func (l *toolCallLimiter) middleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodCallTool {
				return next(ctx, method, req)
			}

			// Zum Zeitpunkt dieser Middleware hat Gangways eigene
			// authenticate()-Stufe (vor dem MCP-Handler, siehe
			// gangway/serve.Server.Handler Reihenfolge) bereits eine
			// verifizierte Identitaet in ctx abgelegt — ein leeres Subject
			// ist praktisch unerreichbar. Der leere String bleibt trotzdem
			// ein gueltiger, wenn auch entwerteter Map-Schluessel: reine
			// Verteidigung, kein regulaerer Pfad (derselbe Grundsatz wie
			// bei capabilitiesFor/scopesSatisfied fuer id == nil).
			var subject string
			if id, ok := serve.IdentityFrom(ctx); ok && id != nil {
				subject = id.Subject
			}

			release, kind := l.acquire(subject)
			if kind != errNone {
				return nil, &jsonrpc.Error{Code: CodeRateLimited, Message: "rate limit exceeded"}
			}
			defer release()
			return next(ctx, method, req)
		}
	}
}
