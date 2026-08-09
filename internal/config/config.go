package config

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/strausmann/gangway/origin"
)

// Env liest eine Umgebungsvariable. Der Umweg ueber diesen Typ statt ueber
// os.Getenv haelt LoadConfig frei von globalem Zustand und macht die
// Konfiguration ohne t.Setenv parallel testbar.
type Env func(key string) string

// AuthMode bestimmt, wie sich Clients gegenueber diesem Server ausweisen.
type AuthMode string

// Die drei Authentifizierungs-Modi.
const (
	// AuthOIDC prueft Bearer-Tokens eines externen Identity Providers.
	AuthOIDC AuthMode = "oidc"
	// AuthToken prueft ein statisches Bearer-Token aus der Konfiguration.
	AuthToken AuthMode = "token"
	// AuthBoth erlaubt beides; der JWT-Pfad hat Vorrang.
	AuthBoth AuthMode = "both"
)

// AccountMode bestimmt, ob der Server ein oder mehrere Fileee-Konten bedient.
type AccountMode string

// Die zwei Konto-Modi.
const (
	// ModeSingle bedient genau ein Konto aus FILEEE_USERNAME/_PASSWORD.
	ModeSingle AccountMode = "single"
	// ModeMulti bildet Token-Subjects auf mehrere Konten ab.
	ModeMulti AccountMode = "multi"
)

// defaultAccountKey ist der Konto-Key im single-Modus. Der Pool behandelt
// beide Modi damit identisch — single ist ein Pool mit genau einem Eintrag.
const defaultAccountKey = "default"

// accountKeyMuster begrenzt Konto-Keys auf Zeichen, die als Dateiname sicher
// sind. Ohne diese Pruefung waere ein Key wie "../../etc/x" ein Schreibzugriff
// ausserhalb des Session-Verzeichnisses.
var accountKeyMuster = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// Account beschreibt ein Fileee-Konto samt der Identitaeten, die darauf zeigen.
type Account struct {
	// Key ist der interne Bezeichner, zugleich Name der Session-Datei.
	Key string
	// Username, Password und TOTPSeed sind die Fileee-Zugangsdaten.
	Username string
	Password string
	TOTPSeed string
	// Subjects sind die Claim-Werte, die auf dieses Konto abbilden.
	Subjects []string
	// Capabilities schraenkt den Funktionsumfang dieses Kontos ein.
	Capabilities Set
	// HasCapabilities gibt an, ob ueberhaupt eine Einschraenkung konfiguriert ist.
	HasCapabilities bool
}

// OIDCProvider waehlt den Identity Provider. Jeder Wert hat einen eigenen
// Variablen-Namensraum — die Anforderungen der Anbieter werden bewusst NICHT
// vermischt, damit jede Anleitung nur ihre eigenen Variablen nennt.
type OIDCProvider string

const (
	ProviderEntra     OIDCProvider = "entra"
	ProviderAuthentik OIDCProvider = "authentik"
	ProviderGeneric   OIDCProvider = "generic"
)

// Config buendelt die gesamte Laufzeitkonfiguration. Sie entsteht ausschliesslich
// in LoadConfig — keine andere Stelle im Server liest Umgebungsvariablen.
type Config struct {
	AuthMode            AuthMode
	OIDCProvider        OIDCProvider
	OIDCIssuer          string
	OIDCClientID        string
	OIDCSubjectClaim    string
	OIDCCapabilityClaim string
	OIDCRequiredScopes  []string
	ResourceURL         string
	APIToken            string
	AllowedSubjects     []string

	AccountMode AccountMode
	Accounts    []Account

	Capabilities Set
	// AllowDestructive ist bereits vollstaendig verdrahtet, nicht bloss
	// geladen: LoadConfig selbst weist den Start ab, wenn Capabilities
	// CapDestructive enthaelt, aber AllowDestructive nicht true ist (siehe
	// unten, direkt nach dem Einlesen) — anders als die anderen in dieser
	// Aufgabe (#42) geprueften Einstellungen ist hier also nichts offen.
	AllowDestructive bool

	// MaxDownloadBytes und MaxUploadBytes sind fuer kuenftige Download-/
	// Upload-Werkzeuge vorgesehen (Capability-Gruppen read/write, siehe
	// README "Funktionsumfang festlegen"). Die Bibliotheksfaehigkeit dafuer
	// EXISTIERT bereits in go-fileee — DocumentService.Upload(ctx, r
	// io.Reader, meta UploadMetadata), DocumentService.DownloadPDF(ctx, id,
	// mode) io.ReadCloser, DocumentService.DownloadPageImage(...)
	// io.ReadCloser, dazu die Freigabe-seitigen Gegenstuecke
	// ShareClient.DownloadSharedPDF/DownloadSharedPage/DownloadPageImage —
	// ein fruehere Fassung dieses Kommentars behauptete faelschlich das
	// Gegenteil. Was fehlt, ist ausschliesslich die AUFRUFSTELLE: dieser
	// Server registriert heute keinerlei Werkzeug, das eine dieser
	// Methoden je aufruft (nur list_documents/search_documents, beide rein
	// lesend ueber Documents.Query/Search, kein Binaertransfer) — deshalb
	// gibt es fuer diese beiden Werte noch keinen Code, um den sie
	// herumgelegt werden koennten. Ihr natuerlicher Ort ist absehbar: um
	// den io.Reader/io.ReadCloser herum, den die jeweilige Methode
	// entgegennimmt bzw. zurueckliefert (io.LimitReader bzw. ein
	// limitierender io.ReadCloser-Wrapper) — NICHT die HTTP-Ebene dieses
	// Servers. Das ist die Aufgabe des kuenftigen Werkzeug-Handlers, nicht
	// dieser Konfiguration.
	//
	// MaxRequestBodyBytes ist davon unabhaengig abgeleitet
	// (ladeZahlenwerte) und WUERDE den 4-MiB-Default des MCP-SDK
	// ueberschreiben, sobald ein Aufrufer sie liest — aber Gangway v0.2.0
	// baut den Streamable-HTTP-Handler intern (serve.AttachMCP/
	// AttachMCPSelector) mit einem fest verdrahteten
	// &mcp.StreamableHTTPOptions{Stateless: true} und bietet keinen Weg,
	// dessen MaxRequestBodyBytes-Feld zu setzen. Dieser dritte Wert bleibt
	// deshalb aus einem GANZ ANDEREN, unabhaengigen Grund unverdrahtet als
	// die beiden obigen — nicht wegen einer fehlenden Aufrufstelle,
	// sondern wegen einer fehlenden Erweiterungsmoeglichkeit in Gangway.
	// Kandidat fuer eine Gangway-Erweiterung (siehe Nachtrag zu ADR-0015).
	MaxDownloadBytes    int64
	MaxUploadBytes      int64
	MaxRequestBodyBytes int64
	// MaxInflight, RateRPS/RateBurst und RateGlobalRPS/RateGlobalBurst
	// werden von internal/server.newToolCallLimiter durchgesetzt — siehe
	// dort (internal/server/ratelimit.go) fuer die Bedeutung jedes einzelnen
	// Werts und die Begruendung, warum RateRPS/RateBurst am verifizierten
	// Token-Subject haengen, nicht an der Client-Adresse.
	MaxInflight int

	ListenAddr        string
	SessionDir        string
	KeepaliveInterval time.Duration
	RateRPS           float64
	RateBurst         int
	RateGlobalRPS     float64
	RateGlobalBurst   int

	// TrustedProxies sind die Netze, deren Weiterleitungs-Header (siehe
	// ClientIPHeaderMode) als tatsaechliche Client-Adresse geglaubt werden.
	// Leer bedeutet: kein Proxy davor, es zaehlt nur die Peer-Adresse.
	TrustedProxies []netip.Prefix
	// AllowedOriginPrefixes ist die Adress-Freigabeliste, die Gangway vor
	// jeden Zugriff auf /mcp schaltet (siehe ADR-0015) — ohne sie startet
	// New im Modus oidc gar nicht erst, ein Server ohne Filter darf nicht
	// hochkommen.
	AllowedOriginPrefixes []netip.Prefix
	// ClientIPHeaderMode waehlt den EINEN Weiterleitungs-Header, der als
	// Quelle der Client-Adresse gilt (github.com/strausmann/gangway/origin).
	// Gangway wertet bewusst nicht mehrere Header der Reihe nach aus — das
	// wuerde einem Aufrufer erlauben, sich den Header auszusuchen, der dem
	// Server gerade passt. Default cf-connecting-ip folgt der bisherigen
	// Priorisierung dieses Servers; er MUSS gegen die tatsaechliche
	// Proxy-Kette (Pangolin/Traefik vs. direktes Cloudflare-Terminieren)
	// geprueft werden, bevor TrustedProxies produktiv gesetzt wird.
	ClientIPHeaderMode origin.HeaderMode

	// LogLevel wird geladen, hat aber heute keinen Konsumenten: dieser
	// Server hat noch keine eigene, levelgesteuerte Logging-Schicht (siehe
	// cmd/fileee-mcp-server/main.go — Start-/Fehlermeldungen laufen ueber
	// schlichtes fmt.Fprintf auf stdout/stderr, Gangways Zugriffsprotokoll
	// (accesslog) kennt kein Level). go-fileee selbst akzeptiert zwar einen
	// *slog.Logger (fileee.WithLogger), aber auch das wird von
	// internal/server.New heute nicht verdrahtet. Ein Level ohne
	// Logging-Schicht zu erzwingen waere Scheinarbeit — das gehoert zu
	// einer bewussten Entscheidung fuer eine Logging-Architektur, nicht in
	// einen Nebensatz einer anderen Aufgabe.
	LogLevel string

	// Warnings sind Hinweise, die den Start nicht verhindern, aber beim Boot
	// protokolliert werden sollen.
	Warnings []string

	subjectIndex map[string]string
}

// AccountBySubject loest einen Claim-Wert auf einen Konto-Key auf. Ein
// unbekanntes Subject liefert bewusst kein Ergebnis — es gibt keinen Fallback
// auf ein Standardkonto.
func (c *Config) AccountBySubject(subject string) (string, bool) {
	key, ok := c.subjectIndex[subject]
	return key, ok
}

// LoadConfig liest die vollstaendige Konfiguration und validiert sie
// vollstaendig, bevor der Server startet. Jede Verletzung ist ein Abbruch mit
// einer Meldung, die die betroffene Variable benennt.
func LoadConfig(env Env) (*Config, error) {
	cfg := &Config{
		AuthMode:            AuthMode(orDefault(env("MCP_AUTH_MODE"), string(AuthToken))),
		OIDCProvider:        OIDCProvider(strings.TrimSpace(env("MCP_OIDC_PROVIDER"))),
		OIDCSubjectClaim:    orDefault(env("MCP_OIDC_SUBJECT_CLAIM"), "sub"),
		OIDCCapabilityClaim: strings.TrimSpace(env("MCP_OIDC_CAPABILITY_CLAIM")),
		OIDCRequiredScopes:  splitListe(env("MCP_OIDC_REQUIRED_SCOPES")),
		ResourceURL:         strings.TrimSpace(env("MCP_RESOURCE_URL")),
		APIToken:            env("MCP_API_TOKEN"),
		AllowedSubjects:     splitListe(env("MCP_ALLOWED_SUBJECTS")),
		AccountMode:         AccountMode(orDefault(env("FILEEE_MODE"), string(ModeSingle))),
		ListenAddr:          orDefault(env("MCP_LISTEN_ADDR"), ":8080"),
		SessionDir:          orDefault(env("FILEEE_SESSION_DIR"), "/home/nonroot/sessions"),
		ClientIPHeaderMode:  origin.HeaderMode(orDefault(env("FILEEE_CLIENT_IP_HEADER_MODE"), string(origin.ModeCFConnectingIP))),
		LogLevel:            orDefault(env("FILEEE_LOG_LEVEL"), "info"),
	}

	switch cfg.ClientIPHeaderMode {
	case origin.ModeXForwardedFor, origin.ModeXRealIP, origin.ModeCFConnectingIP:
	default:
		return nil, fmt.Errorf("FILEEE_CLIENT_IP_HEADER_MODE = %q — erlaubt sind %s, %s, %s",
			cfg.ClientIPHeaderMode, origin.ModeXForwardedFor, origin.ModeXRealIP, origin.ModeCFConnectingIP)
	}

	switch cfg.AuthMode {
	case AuthOIDC, AuthToken, AuthBoth:
	default:
		return nil, fmt.Errorf("MCP_AUTH_MODE = %q — erlaubt sind oidc, token, both", cfg.AuthMode)
	}
	switch cfg.AccountMode {
	case ModeSingle, ModeMulti:
	default:
		return nil, fmt.Errorf("FILEEE_MODE = %q — erlaubt sind single, multi", cfg.AccountMode)
	}

	var err error
	if cfg.Capabilities, err = ParseCapabilities(orDefault(env("FILEEE_CAPABILITIES"), string(CapRead))); err != nil {
		return nil, fmt.Errorf("FILEEE_CAPABILITIES: %w", err)
	}
	cfg.AllowDestructive = env("FILEEE_ALLOW_DESTRUCTIVE") == "true"
	if cfg.Capabilities.Has(CapDestructive) && !cfg.AllowDestructive {
		return nil, fmt.Errorf("FILEEE_CAPABILITIES enthaelt destructive, aber FILEEE_ALLOW_DESTRUCTIVE " +
			"ist nicht true — Fileees Hard-DELETE ist unwiderruflich und braucht zwei bewusste Schalter")
	}

	if err := ladeZahlenwerte(cfg, env); err != nil {
		return nil, err
	}
	if err := ladeNetzwerk(cfg, env); err != nil {
		return nil, err
	}
	if err := ladeAuth(cfg, env); err != nil {
		return nil, err
	}
	if err := ladeKonten(cfg, env); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ladeNetzwerk liest die beiden IP-Praefixlisten. Beide werden hier und nicht
// erst beim Bau des Gangway-Unterbaus geparst — ein unbrauchbares Praefix
// soll den Start mit einer benannten Variable abbrechen, nicht irgendwo tief
// in einer fremden Bibliothek.
func ladeNetzwerk(cfg *Config, env Env) error {
	var err error
	if cfg.TrustedProxies, err = praefixListe(env, "FILEEE_TRUSTED_PROXIES"); err != nil {
		return err
	}
	if cfg.AllowedOriginPrefixes, err = praefixListe(env, "FILEEE_ALLOWED_ORIGIN_PREFIXES"); err != nil {
		return err
	}
	return nil
}

// praefixListe liest eine kommaseparierte Liste aus CIDR-Praefixen oder
// einzelnen IP-Adressen. Eine einzelne Adresse wird als Praefix mit voller
// Bitlaenge behandelt (/32 bei IPv4, /128 bei IPv6) — wer eine einzelne
// Maschine meint, tippt selten eine Maske dazu, und TestLoadConfigListenUndZahlenwerte
// aus Aufgabe 1 verlangt genau das bereits fuer FILEEE_TRUSTED_PROXIES.
func praefixListe(env Env, key string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, teil := range splitListe(env(key)) {
		if p, err := netip.ParsePrefix(teil); err == nil {
			out = append(out, p)
			continue
		}
		addr, err := netip.ParseAddr(teil)
		if err != nil {
			return nil, fmt.Errorf("%s: %q ist weder eine IP-Adresse noch ein CIDR-Praefix", key, teil)
		}
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

func ladeZahlenwerte(cfg *Config, env Env) error {
	var err error
	if cfg.MaxDownloadBytes, err = intWert(env, "FILEEE_MAX_DOWNLOAD_BYTES", 1<<20); err != nil {
		return err
	}
	if cfg.MaxUploadBytes, err = intWert(env, "FILEEE_MAX_UPLOAD_BYTES", 2<<20); err != nil {
		return err
	}
	inflight, err := intWert(env, "FILEEE_MAX_INFLIGHT", 8)
	if err != nil {
		return err
	}
	cfg.MaxInflight = int(inflight)

	burst, err := intWert(env, "FILEEE_RATE_BURST", 3)
	if err != nil {
		return err
	}
	cfg.RateBurst = int(burst)
	globalBurst, err := intWert(env, "FILEEE_RATE_GLOBAL_BURST", 3)
	if err != nil {
		return err
	}
	cfg.RateGlobalBurst = int(globalBurst)

	if cfg.RateRPS, err = floatWert(env, "FILEEE_RATE_RPS", 1); err != nil {
		return err
	}
	if cfg.RateGlobalRPS, err = floatWert(env, "FILEEE_RATE_GLOBAL_RPS", 1); err != nil {
		return err
	}
	if cfg.KeepaliveInterval, err = dauerWert(env, "FILEEE_KEEPALIVE_INTERVAL", 15*time.Minute); err != nil {
		return err
	}

	// Base64 blaeht den Nutzinhalt um Faktor 4/3 auf, dazu kommt der
	// JSON-RPC-Rahmen. Ohne diese Ableitung wuerde der 4-MiB-Default des SDK
	// groessere Uploads mit 413 abweisen, bevor der Tool-Handler laeuft.
	cfg.MaxRequestBodyBytes = cfg.MaxUploadBytes*4/3 + 64<<10
	return nil
}

// guidPattern beschreibt die Mandanten-Kennung, wie Entra sie in der
// Portal-Uebersicht als „Verzeichnis-ID (Mandant)" anzeigt.
var guidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// providerVariables listet je Anbieter die Variablen, die ausschliesslich ihm
// gehoeren. Daraus entstehen sowohl die Pflichtpruefung als auch die Meldung,
// wenn jemand Variablen zweier Anbieter mischt.
var providerVariables = map[OIDCProvider][]string{
	ProviderEntra:     {"MCP_ENTRA_TENANT_ID", "MCP_ENTRA_CLIENT_ID"},
	ProviderAuthentik: {"MCP_AUTHENTIK_BASE_URL", "MCP_AUTHENTIK_APP_SLUG", "MCP_AUTHENTIK_CLIENT_ID"},
	ProviderGeneric:   {"MCP_OIDC_ISSUER", "MCP_OIDC_CLIENT_ID"},
}

// resolveProvider fuellt OIDCIssuer und OIDCClientID aus den Variablen des
// gewaehlten Anbieters. Jeder Anbieter hat einen eigenen Variablen-Namensraum:
// Ein Betreiber, der die Entra-Anleitung liest, begegnet nie einer
// Authentik-Variablen und umgekehrt.
func resolveProvider(cfg *Config, env Env) error {
	if cfg.OIDCProvider == "" {
		return fmt.Errorf("MCP_OIDC_PROVIDER ist im Modus %q Pflicht — erlaubt sind %q, %q, %q",
			cfg.AuthMode, ProviderEntra, ProviderAuthentik, ProviderGeneric)
	}
	if _, ok := providerVariables[cfg.OIDCProvider]; !ok {
		return fmt.Errorf("MCP_OIDC_PROVIDER = %q — erlaubt sind %q, %q, %q",
			cfg.OIDCProvider, ProviderEntra, ProviderAuthentik, ProviderGeneric)
	}
	if err := rejectForeignProviderVariables(cfg.OIDCProvider, env); err != nil {
		return err
	}

	switch cfg.OIDCProvider {
	case ProviderEntra:
		return resolveEntra(cfg, env)
	case ProviderAuthentik:
		return resolveAuthentik(cfg, env)
	default:
		return resolveGeneric(cfg, env)
	}
}

// rejectForeignProviderVariables bricht ab, wenn Variablen eines anderen
// Anbieters gesetzt sind. Ohne diese Pruefung wuerden sie stillschweigend
// ignoriert — der Betreiber sucht dann den Fehler an einer Einstellung, die
// gar nicht gelesen wird.
func rejectForeignProviderVariables(active OIDCProvider, env Env) error {
	var stray []string
	for provider, names := range providerVariables {
		if provider == active {
			continue
		}
		for _, name := range names {
			if strings.TrimSpace(env(name)) != "" {
				stray = append(stray, name)
			}
		}
	}
	if len(stray) == 0 {
		return nil
	}
	sort.Strings(stray)
	return fmt.Errorf("MCP_OIDC_PROVIDER = %q, aber gesetzt sind auch Variablen anderer "+
		"Anbieter: %s — diese werden nicht gelesen. Entweder entfernen oder den "+
		"passenden Anbieter waehlen", active, strings.Join(stray, ", "))
}

// resolveEntra baut die Aussteller-URL aus der Verzeichnis-ID.
//
// Warum nur eine GUID zulaessig ist: Der Aussteller im ausgestellten Token
// traegt immer die Verzeichnis-GUID. Eine verifizierte Domain liefert im
// Discovery-Dokument zwar eine Antwort, der darin genannte Aussteller ist aber
// wieder die GUID — die aus der Domain gebaute URL passt also nie zum Token.
// `common`/`organizations` liefern als Aussteller die Vorlage „{tenantid}", die
// sich gegen kein Token pruefen laesst. Beides scheitert sonst erst zur Laufzeit
// als 401-Schleife, die im Client nur als „Authorization failed" ankommt (am
// 09.08.2026 gegen die echten Discovery-Dokumente nachgemessen).
func resolveEntra(cfg *Config, env Env) error {
	tenant := strings.TrimSpace(env("MCP_ENTRA_TENANT_ID"))
	clientID := strings.TrimSpace(env("MCP_ENTRA_CLIENT_ID"))

	if tenant == "" {
		return fmt.Errorf("MCP_ENTRA_TENANT_ID ist bei MCP_OIDC_PROVIDER=%q Pflicht — die "+
			"Verzeichnis-ID (Mandant) aus der Uebersicht der App-Registrierung", ProviderEntra)
	}
	if !guidPattern.MatchString(tenant) {
		return fmt.Errorf("MCP_ENTRA_TENANT_ID = %q ist keine Verzeichnis-ID — erwartet wird "+
			"die GUID aus der Entra-Portal-Uebersicht (Form "+
			"xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx). Eine Domain oder "+
			"`common`/`organizations` funktioniert hier NICHT: der Aussteller im Token "+
			"traegt immer die GUID, `common` liefert nur die Vorlage `{tenantid}`. Die "+
			"Verzeichnis-ID steht im Entra-Portal unter Uebersicht der App-Registrierung", tenant)
	}
	if clientID == "" {
		return fmt.Errorf("MCP_ENTRA_CLIENT_ID ist bei MCP_OIDC_PROVIDER=%q Pflicht — die "+
			"Anwendungs-ID (Client) aus derselben Uebersicht", ProviderEntra)
	}

	cfg.OIDCIssuer = "https://login.microsoftonline.com/" + tenant + "/v2.0"
	cfg.OIDCClientID = clientID
	return nil
}

// resolveAuthentik baut die Aussteller-URL aus Host und Anwendungs-Kuerzel.
// Das Format `https://<host>/application/o/<slug>/` inklusive abschliessendem
// Schraegstrich ist Authentiks Vorgabe (siehe docs/idp/authentik.md).
func resolveAuthentik(cfg *Config, env Env) error {
	baseURL := strings.TrimSpace(env("MCP_AUTHENTIK_BASE_URL"))
	slug := strings.TrimSpace(env("MCP_AUTHENTIK_APP_SLUG"))
	clientID := strings.TrimSpace(env("MCP_AUTHENTIK_CLIENT_ID"))

	if baseURL == "" {
		return fmt.Errorf("MCP_AUTHENTIK_BASE_URL ist bei MCP_OIDC_PROVIDER=%q Pflicht — die "+
			"Adresse der Authentik-Instanz, z. B. https://auth.example.com", ProviderAuthentik)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("MCP_AUTHENTIK_BASE_URL = %q ist keine https-Adresse — erwartet wird "+
			"Schema und Host ohne Pfad, z. B. https://auth.example.com", baseURL)
	}
	if slug == "" {
		return fmt.Errorf("MCP_AUTHENTIK_APP_SLUG ist bei MCP_OIDC_PROVIDER=%q Pflicht — das "+
			"Kuerzel der Anwendung, wie es in ihrer Authentik-URL steht", ProviderAuthentik)
	}
	if strings.ContainsAny(slug, "/?#") {
		return fmt.Errorf("MCP_AUTHENTIK_APP_SLUG = %q darf keine Pfad- oder Query-Zeichen "+
			"enthalten — nur das Kuerzel selbst", slug)
	}
	if clientID == "" {
		return fmt.Errorf("MCP_AUTHENTIK_CLIENT_ID ist bei MCP_OIDC_PROVIDER=%q Pflicht — die "+
			"Client-ID des OAuth2/OIDC-Providers", ProviderAuthentik)
	}

	cfg.OIDCIssuer = strings.TrimSuffix(baseURL, "/") + "/application/o/" + slug + "/"
	cfg.OIDCClientID = clientID
	return nil
}

// resolveGeneric bedient jeden standardkonformen OpenID-Connect-Anbieter, fuer
// den es hier keinen eigenen Zweig gibt — etwa GitLab oder Keycloak. Er ist ein
// gleichrangiger Weg, kein Ausweichventil fuer Sonderfaelle der beiden anderen:
// Wer Entra nutzt, waehlt entra und bekommt dessen Pruefungen; wer Authentik
// nutzt, waehlt authentik.
func resolveGeneric(cfg *Config, env Env) error {
	issuer := strings.TrimSpace(env("MCP_OIDC_ISSUER"))
	clientID := strings.TrimSpace(env("MCP_OIDC_CLIENT_ID"))

	if issuer == "" {
		return fmt.Errorf("MCP_OIDC_ISSUER ist bei MCP_OIDC_PROVIDER=%q Pflicht — der "+
			"`issuer`-Wert aus dem Discovery-Dokument des Anbieters", ProviderGeneric)
	}
	if clientID == "" {
		return fmt.Errorf("MCP_OIDC_CLIENT_ID ist bei MCP_OIDC_PROVIDER=%q Pflicht", ProviderGeneric)
	}

	cfg.OIDCIssuer = issuer
	cfg.OIDCClientID = clientID
	return nil
}

func ladeAuth(cfg *Config, env Env) error {
	brauchtOIDC := cfg.AuthMode == AuthOIDC || cfg.AuthMode == AuthBoth
	brauchtToken := cfg.AuthMode == AuthToken || cfg.AuthMode == AuthBoth

	if brauchtOIDC {
		if err := resolveProvider(cfg, env); err != nil {
			return err
		}
		// Aussteller und Client-ID sind an dieser Stelle garantiert gesetzt:
		// jeder Anbieter-Zweig in resolveProvider bricht ohne sie ab. Die
		// Client-ID ist zugleich die erwartete Audience — ohne sie wuerde jedes
		// fuer den Aussteller gueltige Token akzeptiert, egal fuer welche
		// Anwendung es ausgestellt wurde. Bei Entra waere das jede beliebige
		// Anwendung desselben Mandanten.
		if cfg.ResourceURL == "" {
			return fmt.Errorf("MCP_RESOURCE_URL ist im Modus %q Pflicht — der Wert muss exakt der "+
				"URL entsprechen, die im Client eingetragen wird", cfg.AuthMode)
		}
		// Gangway (siehe internal/server, ADR-0015) mountet den MCP-Endpunkt
		// fest unter /mcp. PublicBaseURL wird aus ResourceURL abgeleitet,
		// indem genau dieses Suffix wieder abgeschnitten wird — passt
		// ResourceURL nicht dazu, driften die RFC-9728-Metadaten
		// (Resource-URI, WWW-Authenticate-Pointer) von der tatsaechlichen
		// Route auseinander, ohne dass ein Client das laut meldet.
		if !strings.HasSuffix(cfg.ResourceURL, "/mcp") {
			return fmt.Errorf("MCP_RESOURCE_URL = %q muss auf /mcp enden — Gangway mountet den "+
				"MCP-Endpunkt fest unter diesem Pfad (ADR-0015)", cfg.ResourceURL)
		}
		// Im single-Modus ist die Allowlist die einzige Autorisierungsstufe.
		// Ohne sie duerfte jeder Account des IdP auf die Dokumente zugreifen.
		if cfg.AccountMode == ModeSingle && len(cfg.AllowedSubjects) == 0 {
			return fmt.Errorf("MCP_ALLOWED_SUBJECTS ist im Modus %q zusammen mit FILEEE_MODE=single "+
				"Pflicht — leer hiesse: jeder authentifizierte Benutzer des IdP darf zugreifen", cfg.AuthMode)
		}
		// Gangways Server.New verweigert den Start ganz ohne Adress-Freigabeliste
		// (buildAllowList: "no allowlist configured") — ein Server, der niemanden
		// filtern kann, darf nicht hochkommen.
		if len(cfg.AllowedOriginPrefixes) == 0 {
			return fmt.Errorf("FILEEE_ALLOWED_ORIGIN_PREFIXES ist im Modus %q Pflicht — ohne "+
				"Adress-Freigabeliste verweigert Gangway den Start (ADR-0015)", cfg.AuthMode)
		}
	}
	if brauchtToken && cfg.APIToken == "" {
		return fmt.Errorf("MCP_API_TOKEN ist im Modus %q Pflicht", cfg.AuthMode)
	}

	if brauchtToken && cfg.ResourceURL != "" && !istLoopback(cfg.ResourceURL) {
		cfg.Warnings = append(cfg.Warnings, fmt.Sprintf(
			"MCP_AUTH_MODE=%q auf der oeffentlich erreichbaren URL %s — der Zugriff auf die Dokumente "+
				"haengt damit an einem einzigen statischen String. Fuer Produktion ist oidc vorgesehen.",
			cfg.AuthMode, cfg.ResourceURL))
	}
	return nil
}

func ladeKonten(cfg *Config, env Env) error {
	cfg.subjectIndex = map[string]string{}

	if cfg.AccountMode == ModeSingle {
		user, pass := env("FILEEE_USERNAME"), env("FILEEE_PASSWORD")
		if user == "" || pass == "" {
			return fmt.Errorf("FILEEE_USERNAME und FILEEE_PASSWORD sind im Modus single Pflicht")
		}
		cfg.Accounts = []Account{{
			Key:      defaultAccountKey,
			Username: user,
			Password: pass,
			TOTPSeed: env("FILEEE_TOTP_SEED"),
			Subjects: cfg.AllowedSubjects,
		}}
		for _, s := range cfg.AllowedSubjects {
			cfg.subjectIndex[s] = defaultAccountKey
		}
		return nil
	}

	// Ein statisches Token traegt kein Subject — im multi-Modus gaebe es nichts
	// aufzuloesen. Bei both bleibt der JWT-Pfad nutzbar, der Token-Pfad nicht.
	if cfg.AuthMode == AuthToken {
		return fmt.Errorf("FILEEE_MODE=multi zusammen mit MCP_AUTH_MODE=token ist nicht aufloesbar: " +
			"ein statisches Token traegt kein Subject, das auf ein Konto zeigen koennte")
	}

	keys := splitListe(env("FILEEE_ACCOUNTS"))
	if len(keys) == 0 {
		return fmt.Errorf("FILEEE_ACCOUNTS ist im Modus multi Pflicht")
	}

	// Zwei Pruefungen auf Eindeutigkeit: der Key selbst wird zum Dateinamen der Session,
	// und das daraus abgeleitete Env-Praefix bestimmt, welche Variablen gelesen werden.
	// "foo-bar" und "foo_bar" ergeben dasselbe Praefix und wuerden sich sonst
	// unbemerkt dieselben Zugangsdaten teilen.
	gesehen := map[string]bool{}
	praefixe := map[string]string{}

	for _, key := range keys {
		if gesehen[key] {
			return fmt.Errorf("der Konto-Key %q steht mehrfach in FILEEE_ACCOUNTS", key)
		}
		gesehen[key] = true

		if !accountKeyMuster.MatchString(key) {
			return fmt.Errorf("der Konto-Key %q ist unzulaessig — erlaubt sind 1 bis 32 Zeichen aus "+
				"[a-z0-9_-]; der Key wird als Dateiname der Session verwendet", key)
		}
		praefix := "FILEEE_ACCOUNT_" + strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
		if anderer, kollision := praefixe[praefix]; kollision {
			return fmt.Errorf("die Konto-Keys %q und %q lesen dieselben Variablen (%s_*) — "+
				"Bindestrich und Unterstrich werden im Praefix gleich behandelt", anderer, key, praefix)
		}
		praefixe[praefix] = key

		konto := Account{
			Key:      key,
			Username: env(praefix + "_USERNAME"),
			Password: env(praefix + "_PASSWORD"),
			TOTPSeed: env(praefix + "_TOTP_SEED"),
			Subjects: splitListe(env(praefix + "_SUBJECTS")),
		}
		if konto.Username == "" || konto.Password == "" {
			return fmt.Errorf("%s_USERNAME und %s_PASSWORD sind Pflicht", praefix, praefix)
		}

		if roh := env(praefix + "_CAPABILITIES"); roh != "" {
			caps, err := ParseCapabilities(roh)
			if err != nil {
				return fmt.Errorf("%s_CAPABILITIES: %w", praefix, err)
			}
			if caps.Intersect(cfg.Capabilities) != caps {
				return fmt.Errorf("%s_CAPABILITIES = %q ueberschreitet die Obergrenze %q aus "+
					"FILEEE_CAPABILITIES — ein Konto kann nur einschraenken, nie erweitern",
					praefix, caps.String(), cfg.Capabilities.String())
			}
			konto.Capabilities = caps
			konto.HasCapabilities = true
		}

		for _, subject := range konto.Subjects {
			if vorhanden, doppelt := cfg.subjectIndex[subject]; doppelt {
				return fmt.Errorf("das Subject %q zeigt auf zwei Konten (%q und %q) — bei zwei plausiblen "+
					"Zuordnungen gibt es keine richtige Wahl, deshalb kein first-match-wins",
					subject, vorhanden, key)
			}
			cfg.subjectIndex[subject] = key
		}
		cfg.Accounts = append(cfg.Accounts, konto)
	}
	return nil
}

func orDefault(wert, fallback string) string {
	if strings.TrimSpace(wert) == "" {
		return fallback
	}
	return strings.TrimSpace(wert)
}

func splitListe(roh string) []string {
	var out []string
	for _, teil := range strings.Split(roh, ",") {
		if t := strings.TrimSpace(teil); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func intWert(env Env, key string, fallback int64) (int64, error) {
	roh := strings.TrimSpace(env(key))
	if roh == "" {
		return fallback, nil
	}
	wert, err := strconv.ParseInt(roh, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s = %q ist keine ganze Zahl", key, roh)
	}
	// Negative Werte sind fuer jeden Konsumenten dieser Funktion unsinnig — Byte-Grenzen,
	// Burst-Groessen, Nebenlaeufigkeit. Ohne diese Pruefung ergaebe ein negatives
	// Upload-Limit ein negatives MaxRequestBodyBytes, und der Server startete damit.
	if wert < 0 {
		return 0, fmt.Errorf("%s = %q darf nicht negativ sein", key, roh)
	}
	return wert, nil
}

func floatWert(env Env, key string, fallback float64) (float64, error) {
	roh := strings.TrimSpace(env(key))
	if roh == "" {
		return fallback, nil
	}
	wert, err := strconv.ParseFloat(roh, 64)
	if err != nil {
		return 0, fmt.Errorf("%s = %q ist keine Zahl", key, roh)
	}
	if wert < 0 {
		return 0, fmt.Errorf("%s = %q darf nicht negativ sein", key, roh)
	}
	return wert, nil
}

func dauerWert(env Env, key string, fallback time.Duration) (time.Duration, error) {
	roh := strings.TrimSpace(env(key))
	if roh == "" {
		return fallback, nil
	}
	wert, err := time.ParseDuration(roh)
	if err != nil {
		return 0, fmt.Errorf("%s = %q ist keine Dauer (erwartet z. B. 15m, 30s)", key, roh)
	}
	if wert < 0 {
		return 0, fmt.Errorf("%s = %q darf nicht negativ sein", key, roh)
	}
	return wert, nil
}

// istLoopback erkennt lokale Adressen, bei denen der token-Modus unbedenklich ist.
//
// Die Auswertung laeuft ueber url.Parse und Hostname(), nicht ueber eigenes
// Zerschneiden: nur so wird die Klammer-Schreibweise von IPv6 ("http://[::1]:8080/")
// korrekt aufgeloest. Eine selbstgebaute Trennung am ersten Doppelpunkt haette
// dort "[" ergeben und faelschlich vor einer oeffentlichen URL gewarnt.
func istLoopback(roh string) bool {
	u, err := url.Parse(roh)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
