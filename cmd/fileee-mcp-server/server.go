package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/fileee-mcp-server/internal/config"
	"github.com/strausmann/gangway/serve"
)

// mcpSuffix ist der Pfad, unter dem Gangway den MCP-Endpunkt fest mountet
// (serve.go, dortige unexportierte Konstante mcpPath). ResourceURL muss genau
// darauf enden, damit PublicBaseURL korrekt abgeleitet werden kann.
const mcpSuffix = "/mcp"

// Server ist der MCP-Server, aufgesetzt auf Gangway (ADR-0015). Gangway
// uebernimmt Anmeldung, Adress-Freigabeliste, Freigabe je Werkzeug und
// Zugriffsprotokoll — dieser Typ uebersetzt nur unsere Config in Gangways
// eigene und haengt den (heute noch leeren) MCP-Server ein.
type Server struct {
	cfg *config.Config
	gw  *serve.Server
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
func New(ctx context.Context, cfg *config.Config) (*Server, error) {
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
		Audience:        cfg.OIDCAudience,
		SubjectClaim:    cfg.OIDCSubjectClaim,
		HeaderMode:      cfg.ClientIPHeaderMode,
		TrustedProxies:  cfg.TrustedProxies,
		AllowedPrefixes: cfg.AllowedOriginPrefixes,
	}

	gw, err := serve.New(ctx, gwCfg)
	if err != nil {
		return nil, fmt.Errorf("fileee-mcp: gangway: %w", err)
	}

	// Noch kein einziges Werkzeug (Aufgabe 5 registriert die ersten). AttachMCP
	// ist trotzdem Pflicht: ohne einen angehaengten *mcp.Server routet Gangways
	// Handler() den Pfad /mcp ueberhaupt nicht (s.mcp bleibt nil), eine
	// unauthentifizierte Anfrage liefe dann in ein 404 vom inneren ServeMux
	// statt in die 401-Challenge der Authentifizierung — siehe
	// TestUnauthenticatedRequestReachesTheChallengeNotA404.
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "fileee-mcp-server",
		Version: config.Version(),
	}, nil)
	gw.AttachMCP(mcpServer)

	return &Server{cfg: cfg, gw: gw}, nil
}

// Handler liefert den vollstaendig verdrahteten HTTP-Handler (Zugriffsprotokoll,
// Adress-Freigabeliste, Authentifizierung, MCP-Endpunkt — in dieser Reihenfolge,
// siehe serve.Server.Handler).
func (s *Server) Handler() http.Handler { return s.gw.Handler() }

// Run bedient den Server, bis ctx storniert wird, und beendet dann geordnet.
func (s *Server) Run(ctx context.Context) error { return s.gw.Run(ctx) }
