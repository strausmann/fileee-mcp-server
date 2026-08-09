package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/fileee-mcp-server/internal/accounts"
	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
	"github.com/strausmann/fileee-mcp-server/internal/config"
	"github.com/strausmann/fileee-mcp-server/internal/tools"
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
}

// buildOptions buendelt, was Option anpasst.
type buildOptions struct {
	poolOptions []clientpool.Option
}

// Option passt Verdrahtung an, die kein produktiver Aufrufer je braucht —
// heute ausschliesslich zusaetzliche clientpool.Option-Werte fuer den
// Konto-Pool. Sie existiert, damit ein Test diesen Server ueber den echten
// New()-Weg gegen ein lokales Test-Double statt gegen das echte
// https://my.fileee.com verdrahten kann (siehe
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

	// tools.ReadToolKinds() muss hier — beim Bau von *serve.Server — ueber
	// serve.WithToolKinds gesetzt werden, NICHT irgendwann spaeter: Gangways
	// Tool-Autorisierungs-Middleware behandelt jedes Werkzeug, das in dieser
	// Zuordnung fehlt, als "write" (siehe gangway/serve, toolMiddleware) —
	// und der Default-Decider (access.NewGrid) verweigert "write" ohne
	// eigens konfigurierte Schreibrolle kategorisch. Ohne diese Zeile waeren
	// list_documents/search_documents fuer JEDEN Aufrufer gesperrt, obwohl
	// beide rein lesend sind (Pruefbefund zu dieser Aufgabe).
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
	}

	gw, err := serve.New(ctx, gwCfg, serve.WithToolKinds(tools.ReadToolKinds()))
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
	poolOptions := append([]clientpool.Option{
		clientpool.WithSessionStore(func(accountKey string) fileee.SessionStore {
			return fileee.NewFileSessionStore(sessionFilePath(cfg.SessionDir, accountKey))
		}),
		clientpool.WithKeepalive(cfg.KeepaliveInterval),
	}, o.poolOptions...)
	pool := clientpool.New(resolver, poolOptions...)

	// Ein Katalog je Berechtigungsstufe statt eines einzigen *mcp.Server
	// (ADR-0011): instances haelt genau eine, im Voraus gebaute Instanz je
	// erreichbarer Menge (siehe reachableCapabilitySets) — die feste,
	// langlebige Menge, aus der AttachMCPSelectors selector waehlen muss
	// (siehe MCPSelectors Doc-Kommentar in gangway/serve). Eine Instanz je
	// Aufrufer oder je Anfrage zu bauen liefe in Gangways
	// maxWiredInstances-Obergrenze und Ablehnungen fuer alle.
	//
	// AttachMCPSelector (statt AttachMCP) ist trotzdem Pflicht, unabhaengig
	// davon wie viele Werkzeuge registriert sind: ohne einen angehaengten
	// *mcp.Server (bzw. Selector) routet Gangways Handler() den Pfad /mcp
	// ueberhaupt nicht, eine unauthentifizierte Anfrage liefe dann in ein
	// 404 vom inneren ServeMux statt in die 401-Challenge der
	// Authentifizierung — siehe TestUnauthenticatedRequestReachesTheChallengeNotA404.
	// EIN toolCallLimiter fuer alle Instanzen, gebaut hier und nicht je
	// Instanz — siehe dessen Doc-Kommentar (newToolCallLimiter) fuer den
	// Grund: globale Rate und MaxInflight muessen serverweit gelten, nicht
	// je Berechtigungsmenge neu anfangen.
	limiter := newToolCallLimiter(cfg)
	instances := buildInstances(pool, cfg.Capabilities, limiter)
	accountsByKey := make(map[string]config.Account, len(cfg.Accounts))
	for _, acc := range cfg.Accounts {
		accountsByKey[acc.Key] = acc
	}
	gw.AttachMCPSelector(func(_ context.Context, id *identity.Identity) *mcp.Server {
		// scopesSatisfied ist die einzige Stelle, die MCP_OIDC_REQUIRED_SCOPES
		// auswertet (siehe scopes.go) — davor wurde die Einstellung zwar aus
		// der Umgebung gelesen (config.go, LoadConfig), aber nirgends geprueft
		// (Pruefbefund zu dieser Aufgabe). Ein fehlender Scope liefert nil und
		// damit Gangways 400, aus demselben Grund wie unten bei einer
		// unerreichbaren Berechtigungsmenge: eine leere Instanz mit
		// abgeschnittenem Werkzeugkatalog waere hier die falsche Antwort — der
		// Aufrufer hat ein gueltiges Token, ihm fehlt nur der geforderte
		// Scope, das ist eine Ablehnung, keine eingeschraenkte Sicht.
		if !scopesSatisfied(cfg, id) {
			return nil
		}
		// Liefert nil (und damit Gangways 400, niemals eine Standard-Instanz),
		// wenn capabilitiesFor eine Menge zurueckgibt, fuer die instances keine
		// Instanz haelt — nach Konstruktion unerreichbar, siehe
		// reachableCapabilitySets: jede von config.Resolve moegliche Ausgabe ist
		// eine Teilmenge von cfg.Capabilities, und instances deckt jede
		// Teilmenge ab.
		return instances[capabilitiesFor(cfg, accountsByKey, id)]
	})

	return &Server{cfg: cfg, gw: gw, pool: pool}, nil
}

// capabilityGroups sind die vier Gruppen aus ADR-0011, in der dort
// festgelegten Reihenfolge (config.canonical ist unexportiert, deshalb hier
// eine eigene, mit ihr uebereinstimmende Liste).
var capabilityGroups = []config.Capability{config.CapRead, config.CapWrite, config.CapShare, config.CapDestructive}

// reachableCapabilitySets zaehlt jede config.Set auf, die config.Resolve fuer
// global JEMALS liefern koennte: jede Teilmenge von global selbst. Alle drei
// Zweige von Resolve enden in einem Intersect(r.Global) (siehe dessen
// Doc-Kommentar) — nichts, was Resolve zurueckgibt, kann also ausserhalb
// dieser Liste liegen. Das macht die Liste vollstaendig UND von jedem
// einzelnen Aufrufer, Konto oder IdP-Claim-Wert unabhaengig: sie haengt
// ausschliesslich von der beim Start festgelegten Obergrenze
// FILEEE_CAPABILITIES ab, nie von etwas, das ein Aufrufer beeinflussen
// koennte. Begrenzt auf hoechstens 2^4 = 16 Eintraege — weit unterhalb von
// Gangways maxWiredInstances (1024), siehe buildInstances.
func reachableCapabilitySets(global config.Set) []config.Set {
	var present []string
	for _, c := range capabilityGroups {
		if global.Has(c) {
			present = append(present, string(c))
		}
	}

	sets := make([]config.Set, 0, 1<<len(present))
	for mask := 0; mask < 1<<len(present); mask++ {
		var namen []string
		for i, name := range present {
			if mask&(1<<i) != 0 {
				namen = append(namen, name)
			}
		}
		// Jeder Name in present stammt aus capabilityGroups und ist damit
		// garantiert bekannt — ParseCapabilities kann hier nie fehlschlagen.
		s, _ := config.ParseCapabilities(strings.Join(namen, ","))
		sets = append(sets, s)
	}
	return sets
}

// buildInstances baut die feste Instanzmenge: je erreichbarer Berechtigungsmenge
// (reachableCapabilitySets) genau einen *mcp.Server, mit den lesenden
// Werkzeugen registriert, wenn und nur wenn die Menge config.CapRead enthaelt.
// Kuenftige Werkzeuggruppen (write/share/destructive) haetten hier ihre
// eigene, analoge Bedingung.
//
// Jede Instanz bekommt zusaetzlich limiter.middleware() als
// Receiving-Middleware, VOR der Rueckgabe an New() (das die Instanzen
// anschliessend an gw.AttachMCPSelector uebergibt, welches Gangways eigene
// Autorisierungs-Middleware erst beim ersten Request je Instanz installiert,
// siehe gangway/serve.Server.ensureWired). Diese Reihenfolge ist Absicht,
// nicht Zufall — siehe toolCallLimiter.middleware fuer die Begruendung.
func buildInstances(pool *clientpool.Pool, global config.Set, limiter *toolCallLimiter) map[config.Set]*mcp.Server {
	instances := make(map[config.Set]*mcp.Server)
	for _, caps := range reachableCapabilitySets(global) {
		mcpServer := mcp.NewServer(&mcp.Implementation{
			Name:    "fileee-mcp-server",
			Version: config.Version(),
		}, nil)
		if caps.Has(config.CapRead) {
			tools.RegisterRead(mcpServer, pool)
		}
		mcpServer.AddReceivingMiddleware(limiter.middleware())
		instances[caps] = mcpServer
	}
	return instances
}

// capabilitiesFor bestimmt die wirksame Berechtigungsmenge fuer id, nach der
// in ADR-0011 festgelegten Rangfolge — config.Resolve haelt die Rangfolge
// selbst ein, diese Funktion baut nur die dafuer noetige config.Resolution
// zusammen:
//
//  1. Ist MCP_OIDC_CAPABILITY_CLAIM konfiguriert, zaehlt ausschliesslich der
//     Claim aus dem TOKEN (id.Claims) — nie die Konto-Einstellung, selbst wenn
//     der Aufrufer einem Konto zugeordnet ist. Das ist ADR-0011 Punkt 4.2:
//     der IdP gewinnt, weil dort die Benutzerverwaltung stattfindet.
//  2. Sonst wird ueber cfg.AccountBySubject() (bislang ungenutzt — Pruefbefund
//     zu Aufgabe 5, "die Berechtigungen je Konto werden gelesen, geprueft —
//     und nirgends verwendet") das aufgeloeste Konto ermittelt und dessen
//     FILEEE_ACCOUNT_<KEY>_CAPABILITIES (falls gesetzt) ausgewertet.
//  3. Ist id nicht nil, aber das Subject bei keinem Konto gelistet, bleibt
//     Resolution.HasAccount false — config.Resolve faellt dann auf die
//     globale Obergrenze zurueck (nicht auf leer). Ein Aufrufer ohne
//     zugeordnetes Konto sieht damit denselben Werkzeugkatalog wie die
//     Obergrenze erlaubt, kann die Werkzeuge aber beim Aufruf nicht nutzen —
//     das ist die accounts.ErrNoAccount-Ablehnung aus Aufgabe 5
//     (clientFor/"access denied"), eine andere, unabhaengige Schicht von
//     dieser hier. Sichtbarkeit (diese Funktion) und Konto-Zugriff
//     (clientFor) beantworten bewusst unterschiedliche Fragen.
//  4. id == nil ist der einzige Fall, der NICHT bis zu config.Resolve
//     durchlaeuft: er wird vorab mit der leeren Menge beantwortet — anders
//     als Punkt 3 also fail-closed, nicht auf die Obergrenze zurueckfallend
//     (siehe TestCapabilitiesForNilIdentityIsEmpty; Pruefbefund: eine
//     fruehere Fassung dieses Kommentars behauptete hier faelschlich
//     denselben Obergrenze-Fallback wie in Punkt 3). Praktisch sollte
//     Gangway den Selector nie mit einer unverifizierten Identitaet
//     aufrufen, siehe AttachMCPSelector — dieser Zweig ist reine
//     Verteidigung gegen einen theoretischen Fall, kein regulaerer Pfad.
func capabilitiesFor(cfg *config.Config, accountsByKey map[string]config.Account, id *identity.Identity) config.Set {
	if id == nil {
		return config.Set{}
	}

	res := config.Resolution{Global: cfg.Capabilities}
	if cfg.OIDCCapabilityClaim != "" {
		res.ClaimConfigured = true
		res.ClaimValues = claimStrings(id.Claims[cfg.OIDCCapabilityClaim])
	} else if key, ok := cfg.AccountBySubject(id.Subject); ok {
		if acc, ok := accountsByKey[key]; ok {
			res.Account = acc.Capabilities
			res.HasAccount = acc.HasCapabilities
		}
	}
	return config.Resolve(res)
}

// claimStrings liest einen einzelnen Claim-Wert aus den ueber JSON dekodierten
// Token-Claims (id.Claims) als []string — ein IdP-Claim ist je nach Anbieter
// ein einzelner String oder eine Liste (Entra-App-Rollen typischerweise
// ersteres, Authentik-Gruppen typischerweise Letzteres). encoding/json
// dekodiert eine JSON-Liste dabei immer als []any, nie als []string — der
// dritte Zweig ist dennoch nicht tot: id.Claims kann auch von Hand gebaut sein
// (siehe die Tests in diesem Paket).
func claimStrings(v any) []string {
	switch vv := v.(type) {
	case string:
		return []string{vv}
	case []any:
		out := make([]string, 0, len(vv))
		for _, e := range vv {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return vv
	default:
		return nil
	}
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
