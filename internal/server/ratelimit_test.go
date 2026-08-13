package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/gangway/identity/testidp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
	"github.com/strausmann/fileee-mcp-server/internal/config"
	"github.com/strausmann/fileee-mcp-server/internal/tools"
)

// testUnlimitedLimiter baut einen toolCallLimiter mit so grosszuegigen
// Grenzen, dass er in Tests, die NICHT die Ratenbegrenzung selbst pruefen
// (TestEveryReachableCapabilitySetHasAnInstance,
// TestBuildInstancesRegistersReadToolsOnlyWhenCapRead), niemals eingreift.
// buildInstances braucht seit dieser Aufgabe immer einen Limiter — dieser
// hier hat praktisch unbegrenztes Kontingent.
func testUnlimitedLimiter() *toolCallLimiter {
	return newToolCallLimiter(&config.Config{
		RateRPS: 1000, RateBurst: 1000,
		RateGlobalRPS: 1000, RateGlobalBurst: 1000,
		MaxInflight: 1000,
	})
}

// --- toolCallLimiter.acquire: die reine Entscheidung, ohne Netzwerk -------

// TestToolCallLimiterEnforcesGlobalBurst belegt, dass das globale Kontingent
// erschoepft werden kann, unabhaengig vom Subject — der dritte Aufruf bei
// globalBurst=2 wird abgelehnt, obwohl das Subject wechselt.
func TestToolCallLimiterEnforcesGlobalBurst(t *testing.T) {
	l := newToolCallLimiter(&config.Config{
		RateRPS: 1000, RateBurst: 1000, // Subject-Achse grosszuegig, nicht die zu pruefende
		RateGlobalRPS: 0, RateGlobalBurst: 2, // 0 RPS: kein Nachfuellen, deterministisch
		MaxInflight: 1000,
	})

	if _, kind := l.acquire("a"); kind != errNone {
		t.Fatalf("1. Aufruf (Subject a): kind = %v, want errNone", kind)
	}
	if _, kind := l.acquire("b"); kind != errNone {
		t.Fatalf("2. Aufruf (Subject b): kind = %v, want errNone", kind)
	}
	if _, kind := l.acquire("c"); kind != errGlobalRate {
		t.Fatalf("3. Aufruf (Subject c, globalBurst=2 erschoepft): kind = %v, want errGlobalRate", kind)
	}
}

// TestToolCallLimiterEnforcesPerSubjectBurstIndependently belegt die
// Kernentscheidung dieser Aufgabe: das Kontingent haengt am Subject, nicht
// an einer gemeinsamen Ressource, die ein Anrufer fuer alle anderen
// verbrauchen koennte. Subject a erschoepft sein eigenes Kontingent
// (burst=1); Subject b ist davon unberuehrt.
func TestToolCallLimiterEnforcesPerSubjectBurstIndependently(t *testing.T) {
	l := newToolCallLimiter(&config.Config{
		RateRPS: 0, RateBurst: 1, // 0 RPS: kein Nachfuellen, deterministisch
		RateGlobalRPS: 1000, RateGlobalBurst: 1000, // global grosszuegig, nicht die zu pruefende Achse
		MaxInflight: 1000,
	})

	if _, kind := l.acquire("a"); kind != errNone {
		t.Fatalf("a, 1. Aufruf: kind = %v, want errNone", kind)
	}
	if _, kind := l.acquire("a"); kind != errSubjectRate {
		t.Fatalf("a, 2. Aufruf (eigenes Kontingent erschoepft): kind = %v, want errSubjectRate", kind)
	}
	if _, kind := l.acquire("b"); kind != errNone {
		t.Fatalf("b, 1. Aufruf (eigenes, unberuehrtes Kontingent): kind = %v, want errNone", kind)
	}
}

// TestToolCallLimiterEnforcesMaxInflight belegt die dritte, unabhaengige
// Achse direkt: zwei gleichzeitig laufende Aufrufe bei MaxInflight=2 sind
// erlaubt, ein dritter wird abgelehnt, solange keiner der ersten beiden
// freigegeben wurde — und nach einer Freigabe geht der naechste wieder
// durch.
func TestToolCallLimiterEnforcesMaxInflight(t *testing.T) {
	l := newToolCallLimiter(&config.Config{
		RateRPS: 1000, RateBurst: 1000,
		RateGlobalRPS: 1000, RateGlobalBurst: 1000,
		MaxInflight: 2,
	})

	release1, kind1 := l.acquire("a")
	if kind1 != errNone {
		t.Fatalf("1. Aufruf: kind = %v, want errNone", kind1)
	}
	release2, kind2 := l.acquire("b")
	if kind2 != errNone {
		t.Fatalf("2. Aufruf: kind = %v, want errNone", kind2)
	}
	if _, kind3 := l.acquire("c"); kind3 != errInflight {
		t.Fatalf("3. Aufruf (MaxInflight=2 erschoepft): kind = %v, want errInflight", kind3)
	}

	release1()
	if release4, kind4 := l.acquire("d"); kind4 != errNone {
		t.Fatalf("4. Aufruf nach Freigabe eines Platzes: kind = %v, want errNone", kind4)
	} else {
		release4()
	}
	release2()
}

// TestToolCallLimiterGlobalGateIsCheckedBeforeSubjectGate belegt die in
// acquire dokumentierte Reihenfolge (global vor Subject vor Inflight): sind
// sowohl das globale als auch das Subject-Kontingent erschoepft, muss die
// Fehlerursache errGlobalRate lauten — nicht errSubjectRate. Das ist keine
// beobachtbare Aussenwirkung (die Wire-Antwort ist fuer beide identisch,
// siehe middleware), aber acquire selbst legt die Reihenfolge als
// Vertrag fest; dieser Test haelt sie fest, damit sie nicht unbemerkt
// vertauscht wird.
func TestToolCallLimiterGlobalGateIsCheckedBeforeSubjectGate(t *testing.T) {
	l := newToolCallLimiter(&config.Config{
		RateRPS: 0, RateBurst: 0, // Subject-Kontingent von Anfang an leer
		RateGlobalRPS: 0, RateGlobalBurst: 0, // globales Kontingent von Anfang an leer
		MaxInflight: 1000,
	})

	if _, kind := l.acquire("a"); kind != errGlobalRate {
		t.Fatalf("kind = %v, want errGlobalRate (global wird zuerst geprueft)", kind)
	}
}

// --- End-zu-Ende: die Grenzen greifen ueber den echten New()-Weg ----------

// rateLimitEnv buendelt die Rate-/Groessen-Umgebungsvariablen fuer
// testConfigWithRateLimits.
type rateLimitEnv struct {
	rateRPS, rateGlobalRPS     float64
	rateBurst, rateGlobalBurst int
	maxInflight                int
	allowedSubjects            string // kommasepariert; leer -> "abc123"
}

// testConfigWithRateLimits ist testConfigWithIDP, erlaubt aber zusaetzlich,
// die Rate-/Inflight-Einstellungen und die Liste der erlaubten Subjects zu
// setzen -- fuer die Tests, die pruefen, dass diese Einstellungen (Aufgabe
// #42, Pruefbefund: gelesen, aber nirgends ausgewertet) tatsaechlich wirken.
// Mehrere Subjects im single-Modus bilden alle auf dasselbe eine Konto ab
// (config.go, loadAccounts) — ausreichend, um mehrere verifizierte, aber
// UNTERSCHIEDLICHE Identitaeten fuer die Isolations-Tests zu bekommen, ohne
// den multi-Modus mit mehreren Konten aufsetzen zu muessen.
func testConfigWithRateLimits(t *testing.T, rl rateLimitEnv) (*config.Config, *testidp.IDP) {
	t.Helper()
	idp := testidp.New(t)

	subjects := rl.allowedSubjects
	if subjects == "" {
		subjects = "abc123"
	}

	env := map[string]string{
		"MCP_AUTH_MODE":                  "oidc",
		"MCP_OIDC_PROVIDER":              "generic",
		"MCP_OIDC_ISSUER":                idp.URL(),
		"MCP_OIDC_CLIENT_ID":             "fileee-mcp-server",
		"MCP_RESOURCE_URL":               "https://mcp.example.com/mcp",
		"MCP_ALLOWED_SUBJECTS":           subjects,
		"FILEEE_ALLOWED_ORIGIN_PREFIXES": "0.0.0.0/0, ::/0",
		"FILEEE_USERNAME":                "nutzer@example.com",
		"FILEEE_PASSWORD":                "geheim",
		"FILEEE_RATE_RPS":                strconv.FormatFloat(rl.rateRPS, 'f', -1, 64),
		"FILEEE_RATE_BURST":              strconv.Itoa(rl.rateBurst),
		"FILEEE_RATE_GLOBAL_RPS":         strconv.FormatFloat(rl.rateGlobalRPS, 'f', -1, 64),
		"FILEEE_RATE_GLOBAL_BURST":       strconv.Itoa(rl.rateGlobalBurst),
		"FILEEE_MAX_INFLIGHT":            strconv.Itoa(rl.maxInflight),
	}

	cfg, err := config.LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("testConfigWithRateLimits: LoadConfig = %v", err)
	}
	return cfg, idp
}

// newRateLimitTestServer baut *Server ueber den echten New()-Weg (gegen
// fileeeMock statt des echten my.fileee.com, wie in server_test.go) und
// liefert einen bereits verbundenen httptest.Server dazu.
func newRateLimitTestServer(t *testing.T, cfg *config.Config, fileeeMock string) *httptest.Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg, WithPoolOptions(
		clientpool.WithClientOptions(fileee.WithBaseURL(fileeeMock), fileee.WithRateLimit(1000, 1000)),
		clientpool.WithSessionStore(func(accountKey string) fileee.SessionStore {
			return fileee.NewFileSessionStore(filepath.Join(t.TempDir(), accountKey+".json"))
		}),
	))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	httpSrv := httptest.NewServer(s.Handler())
	t.Cleanup(httpSrv.Close)
	return httpSrv
}

// connectAs (server_test.go) verbindet eine neue, eigene MCP-Sitzung,
// authentifiziert als ein bestimmtes Subject — genau das, was die Tests
// hier fuer "zwei unabhaengige Anrufer" brauchen, deshalb hier wiederverwendet
// statt dupliziert.

// callListDocuments ruft list_documents auf und meldet nur, ob der Aufruf
// insgesamt (Transport- bzw. Protokollebene) fehlschlug — ein von
// toolCallLimiter.middleware abgelehnter Aufruf ist ein JSON-RPC-Protokoll-
// fehler (jsonrpc.Error), keine gewoehnliche CallToolResult.IsError=true
// Werkzeugantwort (siehe ratelimit.go, middleware — derselbe Mechanismus
// wie Gangways eigene toolMiddleware fuer CodeForbidden), und kommt daher
// bei der SDK-Gegenseite als Go-error aus CallTool zurueck, nicht als
// IsError-Flag im Ergebnis.
func callListDocuments(t *testing.T, ctx context.Context, session *mcp.ClientSession) error {
	t.Helper()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tools.ToolListDocuments})
	if err != nil {
		return err
	}
	if res.IsError {
		t.Fatalf("CallTool(list_documents): IsError = true, content = %+v — unerwartet, "+
			"ein abgelehnter Aufruf muesste ein Protokollfehler sein, kein Werkzeug-Fehlerergebnis", res.Content)
	}
	return nil
}

// TestNewRateLimitPerSubjectRejectsTheSecondCallAfterTheBurst ist die
// End-zu-Ende-Probe der Subject-Achse: RateBurst=1, RateRPS=0 (kein
// Nachfuellen) -- der erste Aufruf eines Subjects gelingt, der zweite
// unmittelbar folgende wird abgelehnt.
func TestNewRateLimitPerSubjectRejectsTheSecondCallAfterTheBurst(t *testing.T) {
	fileeeMock := newFileeeMock(t)
	cfg, idp := testConfigWithRateLimits(t, rateLimitEnv{
		rateRPS: 0, rateBurst: 1,
		rateGlobalRPS: 1000, rateGlobalBurst: 1000, // global grosszuegig -- nicht die zu pruefende Achse
		maxInflight: 1000,
	})
	httpSrv := newRateLimitTestServer(t, cfg, fileeeMock)
	ctx := context.Background()

	session := connectAs(t, ctx, httpSrv, idp, "abc123")

	if err := callListDocuments(t, ctx, session); err != nil {
		t.Fatalf("1. Aufruf (innerhalb des Burst): %v", err)
	}
	if err := callListDocuments(t, ctx, session); err == nil {
		t.Fatal("2. Aufruf (Burst=1 erschoepft, RPS=0): gelang, FILEEE_RATE_RPS/FILEEE_RATE_BURST " +
			"wurden geladen, aber nicht durchgesetzt (der Pruefbefund zu dieser Aufgabe)")
	}
}

// TestNewRateLimitPerSubjectDoesNotAffectOtherSubjects ist die Isolations-
// Probe: zwei verschiedene, verifizierte Subjects -- Subject a erschoepft
// sein eigenes Kontingent (Burst=1), Subject b ist davon unberuehrt und
// bekommt trotzdem seinen ersten, eigenen Aufruf durch. Das ist die im
// Auftrag verlangte Design-Entscheidung selbst: das Kontingent haengt am
// verifizierten Subject, nicht an einer geteilten Ressource.
func TestNewRateLimitPerSubjectDoesNotAffectOtherSubjects(t *testing.T) {
	fileeeMock := newFileeeMock(t)
	cfg, idp := testConfigWithRateLimits(t, rateLimitEnv{
		rateRPS: 0, rateBurst: 1,
		rateGlobalRPS: 1000, rateGlobalBurst: 1000,
		maxInflight:     1000,
		allowedSubjects: "abc123,def456",
	})
	httpSrv := newRateLimitTestServer(t, cfg, fileeeMock)
	ctx := context.Background()

	sessionA := connectAs(t, ctx, httpSrv, idp, "abc123")
	if err := callListDocuments(t, ctx, sessionA); err != nil {
		t.Fatalf("Subject a, 1. Aufruf: %v", err)
	}
	if err := callListDocuments(t, ctx, sessionA); err == nil {
		t.Fatal("Subject a, 2. Aufruf: gelang trotz erschoepften eigenen Kontingents")
	}

	sessionB := connectAs(t, ctx, httpSrv, idp, "def456")
	if err := callListDocuments(t, ctx, sessionB); err != nil {
		t.Fatalf("Subject b, 1. Aufruf (eigenes, unberuehrtes Kontingent): %v — Subject a's "+
			"erschoepftes Kontingent haette Subject b nicht betreffen duerfen", err)
	}
}

// TestNewGlobalRateLimitAppliesAcrossSubjects ist das Gegenstueck: das
// globale Kontingent ist geteilt -- Subject a verbraucht das einzige
// globale Kontingent (RateGlobalBurst=1), Subject b (das SELBST noch nie
// aufgerufen hat) wird trotzdem abgelehnt, weil das globale Kontingent
// leer ist. Belegt, dass RateGlobalRPS/RateGlobalBurst tatsaechlich
// serverweit gelten und nicht bloss eine zweite, groessere Subject-Grenze
// sind.
func TestNewGlobalRateLimitAppliesAcrossSubjects(t *testing.T) {
	fileeeMock := newFileeeMock(t)
	cfg, idp := testConfigWithRateLimits(t, rateLimitEnv{
		rateRPS: 1000, rateBurst: 1000, // Subject-Achse grosszuegig -- nicht die zu pruefende
		rateGlobalRPS: 0, rateGlobalBurst: 1,
		maxInflight:     1000,
		allowedSubjects: "abc123,def456",
	})
	httpSrv := newRateLimitTestServer(t, cfg, fileeeMock)
	ctx := context.Background()

	sessionA := connectAs(t, ctx, httpSrv, idp, "abc123")
	if err := callListDocuments(t, ctx, sessionA); err != nil {
		t.Fatalf("Subject a, 1. Aufruf (verbraucht das einzige globale Kontingent): %v", err)
	}

	sessionB := connectAs(t, ctx, httpSrv, idp, "def456")
	if err := callListDocuments(t, ctx, sessionB); err == nil {
		t.Fatal("Subject b, 1. Aufruf: gelang trotz erschoepften GLOBALEN Kontingents -- " +
			"FILEEE_RATE_GLOBAL_RPS/FILEEE_RATE_GLOBAL_BURST wurden geladen, aber nicht durchgesetzt")
	}
}

// --- End-zu-Ende: MaxInflight unter echter Nebenlaeufigkeit ---------------

// newSlowFileeeMock ist newFileeeMock (server_test.go) mit einer
// Abweichung: der ERSTE Aufruf von POST /api/documents/rest/query
// signalisiert ueber started, dass er den Handler erreicht hat, und
// blockiert dann, bis release geschlossen wird. Jeder weitere Aufruf
// (z. B. eine spaetere, nach der Freigabe erneut versuchte Anfrage) geht
// sofort durch (sync.Once) -- so blockiert nur genau der eine Aufruf, den
// der Test bewusst "in der Luft haengen" laesst.
func newSlowFileeeMock(t *testing.T, started chan<- struct{}, release <-chan struct{}) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/f/start", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/f/existent", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"existent":true,"twoFactorAuthEnabled":false}`))
	})
	mux.HandleFunc("POST /api/f/login", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "sess"})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"loggedIn":true}`))
	})
	mux.HandleFunc("GET /api/f/user-session", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"authorized":true,"secondsBlocked":0}`))
	})
	var once sync.Once
	mux.HandleFunc("POST /api/documents/rest/query", func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() {
			started <- struct{}{}
			<-release
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rows":[],"totalRows":0}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestNewMaxInflightRejectsAConcurrentCallBeyondTheLimit ist die
// End-zu-Ende-Probe der dritten Achse -- und die einzige der drei, die
// echte Nebenlaeufigkeit statt reiner Wiederholung braucht: MaxInflight=1,
// ein erster Aufruf haengt absichtlich im (simulierten) Fileee-Zugriff
// fest, ein zweiter, waehrenddessen versuchter Aufruf muss SOFORT
// abgelehnt werden -- nicht warten, bis der erste fertig ist (siehe
// ratelimit.go: TryAcquire, nie Acquire/Wait). Nach der Freigabe des
// ersten Aufrufs gelingt ein neuer Versuch wieder.
func TestNewMaxInflightRejectsAConcurrentCallBeyondTheLimit(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	fileeeMock := newSlowFileeeMock(t, started, release)

	cfg, idp := testConfigWithRateLimits(t, rateLimitEnv{
		rateRPS: 1000, rateBurst: 1000, // Rate-Achsen grosszuegig -- nicht die zu pruefende
		rateGlobalRPS: 1000, rateGlobalBurst: 1000,
		maxInflight: 1,
	})
	httpSrv := newRateLimitTestServer(t, cfg, fileeeMock)
	ctx := context.Background()

	sessionFirst := connectAs(t, ctx, httpSrv, idp, "abc123")
	sessionSecond := connectAs(t, ctx, httpSrv, idp, "abc123")

	firstErrc := make(chan error, 1)
	go func() { firstErrc <- callListDocuments(t, ctx, sessionFirst) }()

	select {
	case <-started:
		// Der erste Aufruf haengt jetzt im (simulierten) Fileee-Zugriff --
		// sein Inflight-Platz ist damit garantiert belegt (die Middleware
		// erwirbt ihn VOR dem Tool-Handler, siehe ratelimit.go).
	case <-time.After(5 * time.Second):
		t.Fatal("der erste Aufruf erreichte den (simulierten) Fileee-Zugriff nicht innerhalb von 5s")
	}

	if err := callListDocuments(t, ctx, sessionSecond); err == nil {
		t.Fatal("zweiter, gleichzeitiger Aufruf gelang trotz MaxInflight=1 und eines noch " +
			"laufenden ersten Aufrufs -- FILEEE_MAX_INFLIGHT wurde geladen, aber nicht durchgesetzt")
	}

	close(release)
	select {
	case err := <-firstErrc:
		if err != nil {
			t.Fatalf("erster Aufruf (nach Freigabe): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("erster Aufruf kehrte nach der Freigabe nicht innerhalb von 5s zurueck")
	}

	// Der Platz ist jetzt wieder frei -- ein neuer Aufruf muss durchgehen.
	if err := callListDocuments(t, ctx, sessionSecond); err != nil {
		t.Fatalf("Aufruf nach Freigabe des Inflight-Platzes: %v", err)
	}
}
