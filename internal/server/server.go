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
		Audience:        cfg.OIDCAudience,
		SubjectClaim:    cfg.OIDCSubjectClaim,
		HeaderMode:      cfg.ClientIPHeaderMode,
		TrustedProxies:  cfg.TrustedProxies,
		AllowedPrefixes: cfg.AllowedOriginPrefixes,
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

	// AttachMCP ist Pflicht, unabhaengig davon wie viele Werkzeuge registriert
	// sind: ohne einen angehaengten *mcp.Server routet Gangways Handler() den
	// Pfad /mcp ueberhaupt nicht (s.mcp bleibt nil), eine unauthentifizierte
	// Anfrage liefe dann in ein 404 vom inneren ServeMux statt in die
	// 401-Challenge der Authentifizierung — siehe
	// TestUnauthenticatedRequestReachesTheChallengeNotA404.
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "fileee-mcp-server",
		Version: config.Version(),
	}, nil)
	tools.RegisterRead(mcpServer, pool)
	gw.AttachMCP(mcpServer)

	return &Server{cfg: cfg, gw: gw, pool: pool}, nil
}

// buildResolver uebersetzt cfg.Accounts (siehe config.LoadConfig, ladeKonten)
// in einen accounts.Resolver. Im Modus single existiert genau ein Konto und
// jedes von Gangway durchgelassene Subject zeigt darauf (accounts.NewSingle);
// im Modus multi wird je Konto dessen konfigurierte Subjects-Liste zu einem
// Subject->Credentials-Mapping expandiert (accounts.NewMulti) — ein nicht
// zugeordnetes Subject faellt dann direkt auf accounts.ErrNoAccount, ohne
// Fallback (ADR-0012, Punkt 4/5).
//
// New() ist exportiert und nimmt jede *config.Config entgegen (siehe die
// Anmerkung oben bei der ResourceURL-Pruefung) — deshalb hier eine echte
// Fehlermeldung statt eines Panics, falls eine von Hand gebaute *Config im
// Modus single kein oder mehr als ein Konto traegt, obwohl LoadConfig selbst
// das nie zuliesse. Aus demselben Grund wird im Modus multi ein Subject, das
// ueber zwei Konten hinweg auftaucht, hier noch einmal abgelehnt (Pruefbefund):
// LoadConfigs eigene ladeKonten haelt dieselbe Regel bereits ueber
// cfg.subjectIndex ein, aber New() erhaelt eine *config.Config und keine rohe
// Env — eine von Hand gebaute Config mit einem doppelten Subject wuerde ohne
// diese Pruefung hier still das letzte Konto gewinnen lassen und einen
// Aufrufer damit unter Umstaenden auf ein fremdes Konto abbilden.
func buildResolver(cfg *config.Config) (accounts.Resolver, error) {
	if cfg.AccountMode == config.ModeSingle {
		if len(cfg.Accounts) != 1 {
			return nil, fmt.Errorf("FILEEE_MODE=single erwartet genau ein konfiguriertes Konto, hat %d", len(cfg.Accounts))
		}
		acc := cfg.Accounts[0]
		return accounts.NewSingle(fileee.Credentials{
			Username: acc.Username,
			Password: acc.Password,
			TOTPSeed: acc.TOTPSeed,
		}), nil
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
