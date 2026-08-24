package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/fileee-mcp-server/internal/accounts"
	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
	"github.com/strausmann/fileee-mcp-server/internal/config"
	"github.com/strausmann/fileee-mcp-server/internal/diag"
	"github.com/strausmann/fileee-mcp-server/internal/issued"
	"github.com/strausmann/fileee-mcp-server/internal/tools"
	"github.com/strausmann/gangway/access"
	"github.com/strausmann/gangway/identity"
	"github.com/strausmann/gangway/serve"
	"github.com/strausmann/go-fileee/fileee"
)

// mcpSuffix ist der Pfad, unter dem Gangway den MCP-Endpunkt fest mountet
// (serve.go, dortige unexportierte Konstante mcpPath). ResourceURL muss genau
// darauf enden, damit PublicBaseURL korrekt abgeleitet werden kann.
const mcpSuffix = "/mcp"

// Server ist der MCP-Server, aufgesetzt auf Gangway (ADR-0015). Gangway
// uebernimmt Anmeldung, Adress-Freigabeliste, Freigabe je Werkzeug und
// Zugriffsprotokoll — dieser Typ uebersetzt nur unsere Config in Gangways
// eigene, verdrahtet den Konto-Pool und haengt den MCP-Server mit seinen
// Tools ein.
type Server struct {
	cfg  *config.Config
	gw   *serve.Server
	pool *clientpool.Pool

	// issuedStore ist der aus cfg.IssuedIDTTLSeconds/IssuedIDMaxPerIdentity
	// gebaute internal/issued.Store (siehe deren eigene Doc-Kommentare) —
	// gehalten, aber noch NICHT an tools.RegisterAll durchgereicht: dessen
	// Signatur erweitert erst eine Folgeaufgabe um einen *issued.Store-
	// Parameter, den die Lese-/Schreib-Werkzeuge dann tatsaechlich
	// befragen (Record beim Ausliefern, Check vor einer destruktiven
	// Operation). Bis dahin ist issuedStore ausschliesslich ueber
	// TestNewWiresConfiguredIssuedIDLimitsIntoTheStore (white-box,
	// gleiches Paket) beobachtbar — dieser Test ist bewusst KEIN
	// Ueberbleibsel, sondern der Beleg, dass die beiden Konfigurationswerte
	// tatsaechlich (nicht nur geladen) im gebauten Store ankommen; siehe
	// dessen eigenen Doc-Kommentar fuer den Bezug zum PR-#68-Fehlerbild.
	issuedStore *issued.Store
}

// buildOptions buendelt, was Option anpasst.
type buildOptions struct {
	poolOptions []clientpool.Option
	logOutput   io.Writer
}

// Option passt Verdrahtung an, die kein produktiver Aufrufer je braucht —
// zusaetzliche clientpool.Option-Werte fuer den Konto-Pool, und wohin der
// diagnostische Logger schreibt. Sie existiert, damit ein Test diesen
// Server ueber den echten New()-Weg gegen ein lokales Test-Double statt
// gegen das echte https://my.fileee.com verdrahten kann (siehe
// TestNewRegistersReadToolsUsableThroughTheRealWiring) — ohne sie muesste
// ein End-zu-Ende-Test entweder echte Netzwerkzugriffe machen oder die
// Verdrahtung aus New() ein zweites Mal nachbauen, was genau das Problem
// waere, das dieser Test beheben soll (Pruefbefund: eine parallele
// Test-Verdrahtung deckt die tatsaechlich produktiv laufende nicht ab).
// Kein produktiver Aufruf von New() uebergibt eine Option.
type Option func(*buildOptions)

// WithPoolOptions reicht zusaetzliche clientpool.Option-Werte an den
// Konto-Pool durch, den New() baut — siehe Option.
func WithPoolOptions(opts ...clientpool.Option) Option {
	return func(o *buildOptions) { o.poolOptions = append(o.poolOptions, opts...) }
}

// WithLogOutput leitet das diagnostische Protokoll (internal/diag) an out
// statt an os.Stdout um — fuer Tests, die dessen Zeilen einlesen wollen,
// ohne den Prozess-Standardausgabestrom mitzuschneiden. Kein produktiver
// Aufruf von New() uebergibt diese Option — siehe Option.
func WithLogOutput(out io.Writer) Option {
	return func(o *buildOptions) { o.logOutput = out }
}

// New uebersetzt unsere Konfiguration in Gangways und bereitet jede Schicht
// vor. Es schlaegt fehl, wenn der Identity Provider nicht erreichbar ist oder
// keine Adress-Freigabeliste zustande kommt — ein Server, der niemanden
// authentifizieren oder filtern kann, darf nicht hochkommen (siehe
// serve.New).
//
// New unterstuetzt heute ausschliesslich AuthMode oidc. Gangway v0.2.0 baut
// intern unbedingt einen identity.NewOIDC-Verifier (serve.go, New) — es gibt
// keinen Weg, stattdessen einen anderen identity.Verifier (etwa fuer ein
// statisches Bearer-Token) einzuhaengen. cfg.AuthMode token/both aus Aufgabe 1
// sind damit vorerst nicht ueber diesen Server erreichbar; siehe den
// Nachtrag zu ADR-0015 und den Bericht zu dieser Aufgabe.
//
// opts ist fuer produktive Aufrufer immer leer — siehe Option.
func New(ctx context.Context, cfg *config.Config, opts ...Option) (*Server, error) {
	if cfg.AuthMode != config.AuthOIDC {
		return nil, fmt.Errorf("fileee-mcp: MCP_AUTH_MODE=%q wird von diesem Server (noch) nicht "+
			"unterstuetzt — Gangway v0.2.0 baut intern immer einen OIDC-Verifier auf und bietet keinen "+
			"Weg, einen anderen identity.Verifier einzuhaengen (siehe ADR-0015-Nachtrag)", cfg.AuthMode)
	}

	// Gangway mountet /mcp fest (serve.go, mcpPath) und baut daraus sowohl die
	// RFC-9728-Resource-URI als auch den WWW-Authenticate-Pointer als
	// PublicBaseURL+"/mcp". ResourceURL ist bei uns bereits genau diese volle
	// Endpunkt-URL (LoadConfig erzwingt das "/mcp"-Suffix) — das Suffix muss
	// deshalb hier wieder abgeschnitten werden, sonst driftet z. B. der
	// WWW-Authenticate-Header von der tatsaechlichen /mcp-Route auseinander,
	// ohne dass ein Test, der nur auf "resource_metadata" prueft, das je
	// bemerken wuerde.
	//
	// New() ist exportiert und nimmt jede *Config entgegen — LoadConfig
	// erzwingt das Suffix nur auf dem eigenen Weg, ein Aufrufer kann eine
	// *Config auch von Hand bauen oder nachtraeglich aendern (in diesem Repo
	// tun das bereits mehrere Tests). Deshalb wird das Suffix hier erneut
	// geprueft, statt sich auf LoadConfig zu verlassen: ohne diese Prüfung
	// liefe die folgende Ableitung bei einer zu kurzen oder falsch endenden
	// ResourceURL aus dem gueltigen Bereich (Absturz statt Fehlermeldung).
	if !strings.HasSuffix(cfg.ResourceURL, mcpSuffix) {
		return nil, fmt.Errorf("fileee-mcp: MCP_RESOURCE_URL = %q muss auf %q enden — Gangway mountet "+
			"den MCP-Endpunkt fest unter diesem Pfad (ADR-0015)", cfg.ResourceURL, mcpSuffix)
	}
	publicBaseURL := strings.TrimSuffix(cfg.ResourceURL, mcpSuffix)

	gwCfg := &serve.Config{
		Addr:            cfg.ListenAddr,
		PublicBaseURL:   publicBaseURL,
		IssuerURL:       cfg.OIDCIssuer,
		Audience:        cfg.OIDCClientID,
		SubjectClaim:    cfg.OIDCSubjectClaim,
		HeaderMode:      cfg.ClientIPHeaderMode,
		TrustedProxies:  cfg.TrustedProxies,
		AllowedPrefixes: cfg.AllowedOriginPrefixes,
		// RequiredScopes (gangway >= v0.4.0) ist die einzige Stelle, die
		// cfg.OIDCRequiredScopes VOR einem Token-Austausch ankuendigt --
		// als "scope"-Parameter der WWW-Authenticate-Challenge (RFC 6750
		// §3) und als scopes_supported im RFC-9728-Metadatendokument
		// (siehe gangway/serve, Server.challenge und Handler). Ohne diese
		// Zeile kennt scopesSatisfied (scopes.go) zwar den geforderten
		// Scope und weist einen Aufrufer OHNE ihn ab -- ein Connector ohne
		// vorhandenes Token (etwa claude.ai beim allerersten
		// Verbindungsaufbau) erfaehrt den Scope aber gar nicht erst und
		// fordert beim IdP einen falschen Standard-Scope an (z. B. "openid
		// profile offline_access"). Bei Entra scheitert der
		// Token-Austausch dann mit AADSTS9010010 ("resource parameter
		// provided in the request doesn't match with the requested
		// scopes"), bevor dieser Server je erreicht wird -- ausserhalb
		// jeder Kontrolle von scopesSatisfied, das nur ein bereits
		// ausgestelltes Token prueft.
		RequiredScopes: cfg.OIDCRequiredScopes,
		// AdvertisedScopes (gangway >= v0.5.0) uebernimmt die Ankuendigung
		// (WWW-Authenticate "scope" und RFC-9728 scopes_supported), WENN
		// gesetzt -- RequiredScopes bleibt daneben unveraendert das, wogegen
		// scopesSatisfied (scopes.go) den scp/scope-Claim des Tokens prueft.
		// Der Grund fuer die Trennung ist Entra: RequiredScopes/AADSTS9010010
		// oben loeste die fehlende Ankuendigung fuer Anbieter, bei denen der
		// beim IdP angeforderte Scope-Name mit dem im Token ausgestellten
		// scp-Claim identisch ist (Authentik). Bei Entra sind beide Werte
		// NICHT identisch: angekuendigt werden muss die vollqualifizierte
		// Form "https://<host>/mcp/<scope>", waehrend scp weiterhin nur den
		// kurzen Namen traegt -- ein Connector, der RequiredScopes als
		// Ankuendigung naehme, fordert dort den falschen (kurzen) Namen an
		// und scheitert mit AADSTS650053, wieder bevor dieser Server je
		// erreicht wird. cfg.OIDCAdvertisedScopes ist bei jedem anderen
		// Anbieter leer (Default) -- Handler/challenge fallen dann in
		// gangway selbst auf RequiredScopes zurueck (serve.Server.advertisedScopes),
		// das Verhalten bleibt fuer diese Deployments unveraendert.
		AdvertisedScopes: cfg.OIDCAdvertisedScopes,
	}

	// Gangway authorizes tool calls with access.AllowAll(): every authenticated caller may
	// call every tool. Per-tool gating is the client's job via each tool's ToolAnnotations
	// (ADR-0018) — the server exposes and annotates, it does not decide who may call.
	gw, err := serve.New(ctx, gwCfg, serve.WithDecider(access.AllowAll()))
	if err != nil {
		return nil, fmt.Errorf("fileee-mcp: gangway: %w", err)
	}

	resolver, err := buildResolver(cfg)
	if err != nil {
		return nil, fmt.Errorf("fileee-mcp: %w", err)
	}
	var o buildOptions
	for _, opt := range opts {
		opt(&o)
	}

	// logger is this server's ONE diagnostic logger — level-gated and
	// masked (internal/diag.New) — shared by every tool call
	// (tools.RegisterAll below), by go-fileee's own transport-level
	// request log (fileee.WithLogger, passed into every account's client
	// via clientpool.WithClientOptions — see that package's own doc
	// comment on WithClientOptions for why this applies to every account
	// this pool ever builds a client for), and by the capability-selector
	// logging further down. o.logOutput is nil for every production
	// call (see Option) and defaults to os.Stdout; only a test (via
	// WithLogOutput) redirects it.
	logOutput := o.logOutput
	if logOutput == nil {
		logOutput = os.Stdout
	}
	logger := diag.New(cfg.LogLevel, logOutput)

	// slog.SetDefault(logger) makes the package-level default logger (what
	// slog.InfoContext/DebugContext etc. — called with NO explicit logger —
	// resolve to) the SAME level-gated, masked logger every other part of
	// this process already uses. Without this, log/slog's own zero-value
	// default (an unmasked slog.TextHandler writing to os.Stderr at
	// LevelInfo, ignoring cfg.LogLevel entirely) is a silent second logging
	// path that never passes through diag's redactingHandler.
	//
	// This exists for exactly one caller today: internal/issued.Store.Record
	// logs its per-identity-cap eviction count via slog.DebugContext — the
	// package-level default, not a logger New() takes as a parameter (see
	// that package's own doc comment on WHY: Store.New's signature is
	// Task 1/2's already-reviewed, 100%-covered contract, and changing it
	// here — to thread a *slog.Logger through Record and grow every caller,
	// test, and mock accordingly — is a bigger and riskier edit than the one
	// line below, for a single debug-level line that never carries the ID
	// itself (see Record's doc comment). Two rejected alternatives, for the
	// record: (a) grow issued.New's signature to take a logger — rejected
	// per the above; (c) leave the eviction line on the untouched default —
	// rejected because that default is stderr/unmasked/always-Info, so
	// FILEEE_LOG_LEVEL=debug would never actually surface this line, and
	// FILEEE_LOG_LEVEL=info would still leak it to stderr unmasked — exactly
	// the "loaded but not enforced" defect this task exists to close, just
	// for a logger setting instead of a config value.
	//
	// Side effects, checked (grep for slog.Info/Debug/Warn/Error/Default
	// across the module before this change): internal/issued.Record is the
	// ONLY call site anywhere in this codebase that touches the package-level
	// default logger — nothing else in this process reads or writes it, so
	// there is no other package whose behavior this call could silently
	// change. cmd/fileee-mcp-server/main.go's own startup/shutdown lines use
	// plain fmt.Fprintf, not log/slog, and stay untouched either way.
	//
	// Known limitation for tests, not production: SetDefault mutates
	// process-global state. New() is called once in the real binary, so this
	// is inert there. Several tests in this package call New() repeatedly
	// (and some run t.Parallel()) without redirecting logOutput — each call
	// re-points the global default at ITS OWN logger. slog.SetDefault itself
	// is safe under the race detector (an atomic pointer swap), but a test
	// that both (a) triggers an eviction AND (b) asserts on the resulting
	// log line's content would be flaky if another parallel test's New()
	// call raced the default out from under it in between. No existing test
	// does both — server_diag_test.go asserts on its own `logger` variable
	// directly (not the package default), and issued.Store's own eviction
	// tests build a *Store directly with issued.New/SetClock and never go
	// through server.New at all. If a future test needs to assert on this
	// specific line's output, it must NOT run t.Parallel() with any other
	// test that also calls New() without WithLogOutput.
	slog.SetDefault(logger)

	// issuedStore ist der aus den beiden Task-3-Einstellungen gebaute
	// internal/issued.Store — siehe Server.issuedStore's eigenen
	// Doc-Kommentar dafuer, warum er hier zwar gebaut, aber noch nicht an
	// tools.RegisterAll durchgereicht wird.
	issuedStore := issued.New(
		time.Duration(cfg.IssuedIDTTLSeconds)*time.Second,
		int(cfg.IssuedIDMaxPerIdentity),
	)

	poolOptions := append([]clientpool.Option{
		clientpool.WithSessionStore(func(accountKey string) fileee.SessionStore {
			return fileee.NewFileSessionStore(sessionFilePath(cfg.SessionDir, accountKey))
		}),
		clientpool.WithKeepalive(cfg.KeepaliveInterval),
		clientpool.WithClientOptions(fileee.WithLogger(logger)),
	}, o.poolOptions...)
	pool := clientpool.New(resolver, poolOptions...)

	// ONE *mcp.Server mounts every tool (ADR-0018 replaces ADR-0011's
	// per-capability-set instance catalog): the decider is access.AllowAll()
	// now, so there is no longer a per-caller tool CATALOG to choose between
	// — every authenticated caller who satisfies the required scope gets
	// this same instance. Gating a caller's effective tool set is the
	// client's job via each tool's ToolAnnotations.
	//
	// AttachMCPSelector (instead of AttachMCP) is still required, regardless
	// of there being only one instance: without an attached *mcp.Server (or
	// selector), Gangway's Handler() does not route /mcp at all — an
	// unauthenticated request would then hit a 404 from the inner ServeMux
	// instead of the 401 challenge from authentication — see
	// TestUnauthenticatedRequestReachesTheChallengeNotA404.
	limiter := newToolCallLimiter(cfg)
	instance := mcp.NewServer(&mcp.Implementation{Name: "fileee-mcp-server", Version: config.Version()}, nil)
	tools.RegisterAll(instance, pool, tools.ServerInfo{Mode: string(cfg.AccountMode), MaxUploadBytes: cfg.MaxUploadBytes}, logger)
	instance.AddReceivingMiddleware(limiter.middleware())
	gw.AttachMCPSelector(func(ctx context.Context, id *identity.Identity) *mcp.Server {
		// scopesSatisfied ist die einzige Stelle, die MCP_OIDC_REQUIRED_SCOPES
		// auswertet (siehe scopes.go) — davor wurde die Einstellung zwar aus
		// der Umgebung gelesen (config.go, LoadConfig), aber nirgends geprueft
		// (Pruefbefund zu dieser Aufgabe). Ein fehlender Scope liefert nil und
		// damit Gangways 400: der Aufrufer hat ein gueltiges Token, ihm fehlt
		// nur der geforderte Scope, das ist eine Ablehnung, keine
		// eingeschraenkte Sicht.
		//
		// Protokolliert (info): missingScopes() ist ausschliesslich fuer
		// dieses Protokoll da, nicht Teil der Entscheidung selbst — siehe
		// deren Doc-Kommentar in scopes.go.
		if !scopesSatisfied(cfg, id) {
			logger.InfoContext(ctx, "mcp selector: caller rejected: required scope missing",
				"missing_scopes", missingScopes(cfg, id))
			return nil
		}
		return instance
	})

	return &Server{cfg: cfg, gw: gw, pool: pool, issuedStore: issuedStore}, nil
}

// buildResolver uebersetzt cfg.Accounts (siehe config.LoadConfig, ladeKonten)
// in einen accounts.Resolver — fuer BEIDE Kontomodi ueber denselben Weg,
// accounts.NewMulti: jedes Konto expandiert seine konfigurierte
// Subjects-Liste zu einem Subject->Credentials-Mapping, ein nicht
// zugeordnetes Subject faellt direkt auf accounts.ErrNoAccount, ohne
// Fallback (ADR-0012, Punkt 4/5).
//
// Im Modus single ist cfg.Accounts[0].Subjects identisch mit
// cfg.AllowedSubjects (config.go, ladeKonten) — LoadConfig erzwingt dort
// bereits, dass diese Liste im Modus single nicht leer sein darf, mit der
// Begruendung "leer hiesse: jeder authentifizierte Benutzer des IdP darf
// zugreifen" (config.go, ladeAuth). accounts.NewSingle wuerde genau diese
// Zusicherung unterlaufen: es ist per eigenem Doc-Kommentar bewusst
// subject-blind gebaut ("every caller gangway lets through shares one
// Fileee account") — mit ihm haette die erzwungene Liste keinerlei
// Wirkung, ein Aufrufer mit GUELTIGEM Token, aber einem Subject AUSSERHALB
// der Liste, haette trotzdem Zugriff auf das eine konfigurierte Konto
// bekommen (Pruefbefund: eine erzwungene Konfiguration ohne Wirkung ist
// keine Design-Frage, sondern ein Widerspruch im selben Quelltext — die
// Startmeldung benennt den Zweck der Liste, buildResolver ignorierte ihn).
// accounts.NewSingle selbst bleibt unveraendert (siehe sein eigener
// Doc-Kommentar) — fuer diesen Server ist es seit dieser Korrektur schlicht
// unbenutzt; ein Aufrufer, der die Funktion direkt nutzt (ausserhalb dieses
// Servers), bekommt weiterhin genau das dokumentierte Verhalten.
//
// New() ist exportiert und nimmt jede *config.Config entgegen (siehe die
// Anmerkung oben bei der ResourceURL-Pruefung) — deshalb hier eine echte
// Fehlermeldung statt eines Panics, falls eine von Hand gebaute *Config im
// Modus single kein oder mehr als ein Konto traegt, obwohl LoadConfig selbst
// das nie zuliesse. Aus demselben Grund wird ein Subject, das ueber zwei
// Konten hinweg auftaucht, hier noch einmal abgelehnt (Pruefbefund):
// LoadConfigs eigene ladeKonten haelt dieselbe Regel bereits ueber
// cfg.subjectIndex ein, aber New() erhaelt eine *config.Config und keine rohe
// Env — eine von Hand gebaute Config mit einem doppelten Subject wuerde ohne
// diese Pruefung hier still das letzte Konto gewinnen lassen und einen
// Aufrufer damit unter Umstaenden auf ein fremdes Konto abbilden.
func buildResolver(cfg *config.Config) (accounts.Resolver, error) {
	if cfg.AccountMode == config.ModeSingle && len(cfg.Accounts) != 1 {
		return nil, fmt.Errorf("FILEEE_MODE=single erwartet genau ein konfiguriertes Konto, hat %d", len(cfg.Accounts))
	}

	bySubject := make(map[string]fileee.Credentials, len(cfg.Accounts))
	subjectAccount := make(map[string]string, len(cfg.Accounts))
	for _, acc := range cfg.Accounts {
		creds := fileee.Credentials{Username: acc.Username, Password: acc.Password, TOTPSeed: acc.TOTPSeed}
		for _, subject := range acc.Subjects {
			if other, dup := subjectAccount[subject]; dup {
				return nil, fmt.Errorf("das Subject %q zeigt auf zwei Konten (%q und %q) — bei zwei "+
					"plausiblen Zuordnungen gibt es keine richtige Wahl, deshalb kein first-match-wins",
					subject, other, acc.Key)
			}
			subjectAccount[subject] = acc.Key
			bySubject[subject] = creds
		}
	}
	return accounts.NewMulti(bySubject), nil
}

// sessionFilePath leitet aus dem clientpool-Konto-Key einen dateisystemsicheren
// Dateinamen fuer die Session-Datei ab.
//
// clientpool schluesselt gepoolte Clients ueber den AUFGELOESTEN Fileee-
// Benutzernamen (siehe clientpool.Pool, Doc-Kommentar zu accountKey) — nicht
// ueber den kurzen, per accountKeyMuster dateisystemsicher geprueften
// FILEEE_ACCOUNT_<KEY> aus FILEEE_ACCOUNTS (config.Account.Key). Ein
// Benutzername ist das, was der Betreiber in FILEEE_ACCOUNT_<KEY>_USERNAME
// eingetragen hat — meist eine E-Mail-Adresse, aber nichts erzwingt das.
// Hashing statt direkter Verwendung vermeidet diese Annahme und haelt den
// Benutzernamen im Klartext aus der Verzeichnisliste des Session-Ordners
// heraus.
func sessionFilePath(dir, accountKey string) string {
	sum := sha256.Sum256([]byte(accountKey))
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".json")
}

// Handler liefert den vollstaendig verdrahteten HTTP-Handler (Zugriffsprotokoll,
// Adress-Freigabeliste, Authentifizierung, MCP-Endpunkt — in dieser Reihenfolge,
// siehe serve.Server.Handler).
func (s *Server) Handler() http.Handler { return s.gw.Handler() }

// Run bedient den Server, bis ctx storniert wird, beendet dann geordnet und
// stoppt anschliessend den Konto-Pool (dessen Keepalive-Goroutinen sonst den
// Prozess ueberleben wuerden, siehe clientpool.Pool.Close).
func (s *Server) Run(ctx context.Context) error {
	err := s.gw.Run(ctx)
	s.pool.Close()
	return err
}
