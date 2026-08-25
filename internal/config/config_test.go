package config

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/strausmann/fileee-mcp-server/internal/diag"
	"github.com/strausmann/gangway/origin"
)

// minimalToken ist die kleinstmoegliche gueltige Konfiguration: ein Konto, ein
// statisches Token, kein Identity Provider.
func minimalToken() map[string]string {
	return map[string]string{
		"MCP_API_TOKEN":   "t0ken",
		"FILEEE_USERNAME": "nutzer@example.com",
		"FILEEE_PASSWORD": "geheim",
	}
}

// minimalOIDC ist die kleinstmoegliche gueltige Konfiguration mit Identity Provider.
func minimalOIDC() map[string]string {
	return map[string]string{
		"MCP_AUTH_MODE":                  "oidc",
		"MCP_OIDC_PROVIDER":              "generic",
		"MCP_OIDC_ISSUER":                "https://idp.example.com/application/o/mcp/",
		"MCP_OIDC_CLIENT_ID":             "mcp-client",
		"MCP_RESOURCE_URL":               "https://mcp.example.com/mcp",
		"MCP_ALLOWED_SUBJECTS":           "abc123",
		"FILEEE_ALLOWED_ORIGIN_PREFIXES": "0.0.0.0/0",
		"FILEEE_USERNAME":                "nutzer@example.com",
		"FILEEE_PASSWORD":                "geheim",
	}
}

func envOf(m map[string]string) Env {
	return func(key string) string { return m[key] }
}

func TestLoadConfigDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := LoadConfig(envOf(minimalToken()))
	if err != nil {
		t.Fatalf("LoadConfig = Fehler %v, erwartet kein Fehler", err)
	}

	if cfg.AuthMode != AuthToken {
		t.Errorf("AuthMode = %q, erwartet %q", cfg.AuthMode, AuthToken)
	}
	if cfg.AccountMode != ModeSingle {
		t.Errorf("AccountMode = %q, erwartet %q", cfg.AccountMode, ModeSingle)
	}
	if cfg.OIDCSubjectClaim != "sub" {
		t.Errorf("OIDCSubjectClaim = %q, erwartet %q — sub ist der stabile Identitaetsanker",
			cfg.OIDCSubjectClaim, "sub")
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, erwartet %q", cfg.ListenAddr, ":8080")
	}
	if cfg.SessionDir != "/home/nonroot/sessions" {
		t.Errorf("SessionDir = %q, erwartet %q — der Pfad gehoert im distroless-Image bereits uid 65532",
			cfg.SessionDir, "/home/nonroot/sessions")
	}
	if cfg.KeepaliveInterval != 15*time.Minute {
		t.Errorf("KeepaliveInterval = %v, erwartet %v", cfg.KeepaliveInterval, 15*time.Minute)
	}
	if len(cfg.Accounts) != 1 || cfg.Accounts[0].Key != defaultAccountKey {
		t.Fatalf("erwartet genau ein Konto mit Key %q, bekommen %+v", defaultAccountKey, cfg.Accounts)
	}
}

// Das abgeleitete Transport-Limit muss ueber dem Upload-Limit liegen: Base64
// blaeht um Faktor 4/3, und der JSON-Rahmen kommt obendrauf. Ohne diese
// Ableitung wuerde der 4-MiB-Default des SDK groessere Uploads mit 413
// abweisen, bevor der Tool-Handler ueberhaupt laeuft.
func TestLoadConfigLeitetTransportLimitAusUploadLimitAb(t *testing.T) {
	t.Parallel()

	env := minimalToken()
	env["FILEEE_MAX_UPLOAD_BYTES"] = "3000000"

	cfg, err := LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("LoadConfig = Fehler %v", err)
	}

	mindestens := int64(3000000*4/3) + 65536
	if cfg.MaxRequestBodyBytes < mindestens {
		t.Fatalf("MaxRequestBodyBytes = %d, erwartet mindestens %d (Base64-Aufschlag plus Rahmen)",
			cfg.MaxRequestBodyBytes, mindestens)
	}
}

// TestLoadConfigMaxRequestBodyBytesSaettigtBeiSehrGrossemUploadLimit belegt
// den Ueberlauf-Fund (Review, 23.08.2026): FILEEE_MAX_UPLOAD_BYTES
// akzeptiert jeden nicht-negativen int64-Wert, bis math.MaxInt64. Mit der
// urspruenglichen Inline-Formel maxUploadBytes*4/3 + 64<<10 lief bereits
// die Multiplikation *4 bei einem Wert nahe math.MaxInt64 ueber int64
// hinaus und kippte MaxRequestBodyBytes in einen NEGATIVEN Wert — genau
// der Zustand, den intWerts eigene Negativ-Pruefung fuer den EINGABEWERT
// verhindern soll ("ein negatives Upload-Limit ergaebe ein negatives
// MaxRequestBodyBytes"), hier aber ueber einen positiven, von intWert
// als gueltig akzeptierten Wert erreicht.
//
// Getestet ueber LoadConfig (nicht nur den nackten Helfer
// maxRequestBodyBytesFor direkt), weil das der tatsaechlich verdrahtete
// Pfad ist: FILEEE_MAX_UPLOAD_BYTES als String aus der Umgebung, geparst
// von intWert, abgeleitet zu MaxRequestBodyBytes.
//
// Gegenprobe (Kommentar, nicht ausfuehrbar, da die alte Inline-Formel
// entfernt wurde): mit maxUploadBytes*4/3 + 64<<10 direkt in int64
// gerechnet wird dieser Test ROT — bei maxUploadBytes nahe math.MaxInt64
// lief die Multiplikation ueber und lieferte ein negatives
// MaxRequestBodyBytes, was die Test-Bedingung "MaxRequestBodyBytes > 0"
// verletzt.
func TestLoadConfigMaxRequestBodyBytesSaettigtBeiSehrGrossemUploadLimit(t *testing.T) {
	t.Parallel()

	env := minimalToken()
	env["FILEEE_MAX_UPLOAD_BYTES"] = strconv.FormatInt(math.MaxInt64, 10)

	cfg, err := LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("LoadConfig = Fehler %v", err)
	}

	if cfg.MaxRequestBodyBytes <= 0 {
		t.Fatalf("MaxRequestBodyBytes = %d, wollte einen POSITIVEN Wert (die alte Formel maxUploadBytes*4/3 + 64<<10 lief hier ueber int64 und lieferte einen negativen Wert)",
			cfg.MaxRequestBodyBytes)
	}
	if cfg.MaxRequestBodyBytes != math.MaxInt64 {
		t.Errorf("MaxRequestBodyBytes = %d, want %d (math.MaxInt64, gesaettigt) fuer FILEEE_MAX_UPLOAD_BYTES = math.MaxInt64",
			cfg.MaxRequestBodyBytes, int64(math.MaxInt64))
	}
}

// TestMaxRequestBodyBytesForGrenzfaelle prueft maxRequestBodyBytesFor direkt
// (nicht nur ueber LoadConfig) an den drei Stellen, an denen die alte
// Inline-Formel ueberlaufen konnte: der Multiplikation *4, der Addition
// des Rahmen-Zuschlags, und einem normalen, kleinen Wert als
// Kontrollfall (keine Regression fuer den Alltagsfall).
func TestMaxRequestBodyBytesForGrenzfaelle(t *testing.T) {
	t.Parallel()

	// Kontrollfall: ein normaler Wert veraendert sich durch den Fix nicht
	// gegenueber der alten Formel.
	if got, want := maxRequestBodyBytesFor(3000000), int64(3000000*4/3)+64<<10; got != want {
		t.Errorf("maxRequestBodyBytesFor(3000000) = %d, want %d (unveraendert gegenueber der alten Formel)", got, want)
	}

	// TestMaxRequestBodyBytesForKeinVerfruehtesSaettigen (Codex-Review,
	// 23.08.2026) ist der eigentliche Regressionstest fuer den
	// Nachbesserungs-Fund unten -- als eigener Test, damit er unabhaengig
	// von den anderen Grenzfaellen hier gelesen und (bei einer
	// Gegenprobe) isoliert rot werden kann.

	// maxUploadBytes*4 selbst liefe hier ueber, noch bevor /3 greift --
	// UND das wahre, unbeschraenkte Endergebnis liegt bereits weit
	// jenseits von math.MaxInt64: Saettigen ist hier korrekt.
	if got := maxRequestBodyBytesFor(math.MaxInt64); got != math.MaxInt64 {
		t.Errorf("maxRequestBodyBytesFor(math.MaxInt64) = %d, want %d (math.MaxInt64)", got, int64(math.MaxInt64))
	}
	if got := maxRequestBodyBytesFor(math.MaxInt64); got <= 0 {
		t.Errorf("maxRequestBodyBytesFor(math.MaxInt64) = %d, wollte einen POSITIVEN Wert", got)
	}

	// Der zweite Saettigungspfad: (maxUploadBytes/3)*4 liegt hier NOCH
	// unter der math.MaxInt64/4-Schranke (der erste Guard greift also
	// NICHT), aber die anschliessende Addition des Rahmen-Zuschlags
	// (+64<<10) selbst wuerde ueberlaufen. n so gewaehlt, dass
	// quotient = math.MaxInt64/4 (die groesstmoegliche noch zulaessige
	// Quotienten-Groesse) und rest = 2 (der groesstmoegliche Rest) --
	// aufgeblaeht liegt dann bei math.MaxInt64-1, was innerhalb von
	// 64<<10 an math.MaxInt64 liegt und die zweite Pruefung ausloest.
	quotientAnDerSchranke := int64(math.MaxInt64 / 4)
	nAnDerZweitenSchranke := 3*quotientAnDerSchranke + 2
	if got := maxRequestBodyBytesFor(nAnDerZweitenSchranke); got != math.MaxInt64 {
		t.Errorf("maxRequestBodyBytesFor(%d) = %d, want %d (math.MaxInt64) -- der erste Guard (quotient*4) greift hier NICHT, nur die Addition des Rahmen-Zuschlags liefe ueber",
			nAnDerZweitenSchranke, got, int64(math.MaxInt64))
	}
}

// TestMaxRequestBodyBytesForKeinVerfruehtesSaettigen belegt den
// Nachbesserungs-Fund (Codex-Review, 23.08.2026): die urspruengliche
// Fassung dieser Funktion pruefte den EINGABEWERT direkt gegen
// math.MaxInt64/4, bevor ueberhaupt gerechnet wurde -- das saettigte
// fuer Upload-Limits zwischen math.MaxInt64/4 und rund
// 3*math.MaxInt64/4 deutlich zu FRUEH: die naive Zwischenrechnung
// maxUploadBytes*4 lief zwar ueber, das tatsaechlich gewuenschte
// Endergebnis maxUploadBytes*4/3 + 64<<10 passte aber noch bequem in
// int64. Konkretes Beispiel aus dem Review: maxUploadBytes =
// 3000000000000000000 muss GENAU 4000000000000065536 ergeben (nicht
// gesaettigt) -- die alte Fassung lieferte hier math.MaxInt64.
func TestMaxRequestBodyBytesForKeinVerfruehtesSaettigen(t *testing.T) {
	t.Parallel()

	const maxUploadBytes = 3000000000000000000
	const want = 4000000000000065536

	if got := maxRequestBodyBytesFor(maxUploadBytes); got != want {
		t.Errorf("maxRequestBodyBytesFor(%d) = %d, want %d (exaktes, NICHT gesaettigtes Ergebnis)", int64(maxUploadBytes), got, int64(want))
	}
}

// TestLoadConfigMaxRequestBodyBytesNichtVerfruehtGesaettigt ist derselbe
// Fund wie TestMaxRequestBodyBytesForKeinVerfruehtesSaettigen, aber ueber
// den tatsaechlich verdrahteten Pfad (LoadConfig liest
// FILEEE_MAX_UPLOAD_BYTES als String aus der Umgebung) statt nur ueber
// den nackten Helfer -- dieselbe Begruendung wie bei
// TestLoadConfigMaxRequestBodyBytesSaettigtBeiSehrGrossemUploadLimit
// oben.
func TestLoadConfigMaxRequestBodyBytesNichtVerfruehtGesaettigt(t *testing.T) {
	t.Parallel()

	env := minimalToken()
	env["FILEEE_MAX_UPLOAD_BYTES"] = "3000000000000000000"

	cfg, err := LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("LoadConfig = Fehler %v", err)
	}

	const want = 4000000000000065536
	if cfg.MaxRequestBodyBytes != want {
		t.Errorf("MaxRequestBodyBytes = %d, want %d (exaktes, NICHT gesaettigtes Ergebnis) fuer FILEEE_MAX_UPLOAD_BYTES = 3000000000000000000",
			cfg.MaxRequestBodyBytes, int64(want))
	}
}

func TestLoadConfigFailFast(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		env      func() map[string]string
		erwartet string // Teilstring der Fehlermeldung
	}{
		{
			name: "oidc ohne issuer",
			env: func() map[string]string {
				e := minimalOIDC()
				delete(e, "MCP_OIDC_ISSUER")
				return e
			},
			erwartet: "MCP_OIDC_ISSUER",
		},
		{
			name: "oidc ohne resource-url",
			env: func() map[string]string {
				e := minimalOIDC()
				delete(e, "MCP_RESOURCE_URL")
				return e
			},
			erwartet: "MCP_RESOURCE_URL",
		},
		{
			name: "oidc mit single ohne allowlist",
			env: func() map[string]string {
				e := minimalOIDC()
				delete(e, "MCP_ALLOWED_SUBJECTS")
				return e
			},
			erwartet: "MCP_ALLOWED_SUBJECTS",
		},
		{
			name: "oidc ohne client-id",
			env: func() map[string]string {
				e := minimalOIDC()
				delete(e, "MCP_OIDC_CLIENT_ID")
				return e
			},
			erwartet: "MCP_OIDC_CLIENT_ID",
		},
		{
			name: "oidc mit resource-url ohne /mcp-suffix",
			env: func() map[string]string {
				e := minimalOIDC()
				e["MCP_RESOURCE_URL"] = "https://mcp.example.com/"
				return e
			},
			erwartet: "/mcp",
		},
		{
			name: "oidc ohne herkunfts-allowlist",
			env: func() map[string]string {
				e := minimalOIDC()
				delete(e, "FILEEE_ALLOWED_ORIGIN_PREFIXES")
				return e
			},
			erwartet: "FILEEE_ALLOWED_ORIGIN_PREFIXES",
		},
		{
			name: "unbrauchbares praefix in der herkunfts-allowlist",
			env: func() map[string]string {
				e := minimalOIDC()
				e["FILEEE_ALLOWED_ORIGIN_PREFIXES"] = "nicht-eine-adresse"
				return e
			},
			erwartet: "FILEEE_ALLOWED_ORIGIN_PREFIXES",
		},
		{
			name: "unbrauchbares praefix bei trusted proxies",
			env: func() map[string]string {
				e := minimalToken()
				e["FILEEE_TRUSTED_PROXIES"] = "nicht-eine-adresse"
				return e
			},
			erwartet: "FILEEE_TRUSTED_PROXIES",
		},
		{
			name: "unbekannter client-ip-header-modus",
			env: func() map[string]string {
				e := minimalToken()
				e["FILEEE_CLIENT_IP_HEADER_MODE"] = "x-forwarded-host"
				return e
			},
			erwartet: "FILEEE_CLIENT_IP_HEADER_MODE",
		},
		{
			name: "unbekannte log-stufe",
			env: func() map[string]string {
				e := minimalToken()
				e["FILEEE_LOG_LEVEL"] = "verbose"
				return e
			},
			erwartet: "FILEEE_LOG_LEVEL",
		},
		{
			name: "token-modus ohne token",
			env: func() map[string]string {
				e := minimalToken()
				delete(e, "MCP_API_TOKEN")
				return e
			},
			erwartet: "MCP_API_TOKEN",
		},
		{
			name: "multi ohne kontenliste",
			env: func() map[string]string {
				e := minimalOIDC()
				e["FILEEE_MODE"] = "multi"
				return e
			},
			erwartet: "FILEEE_ACCOUNTS",
		},
		{
			name: "multi zusammen mit dem token-modus",
			env: func() map[string]string {
				return map[string]string{
					"MCP_AUTH_MODE":                 "token",
					"MCP_API_TOKEN":                 "t0ken",
					"FILEEE_MODE":                   "multi",
					"FILEEE_ACCOUNTS":               "anna",
					"FILEEE_ACCOUNT_ANNA_USERNAME":  "anna@example.com",
					"FILEEE_ACCOUNT_ANNA_PASSWORD":  "geheim",
					"FILEEE_ACCOUNT_ANNA_SUBJECTS":  "anna",
					"FILEEE_ACCOUNT_ANNA_TOTP_SEED": "",
				}
			},
			erwartet: "kein Subject",
		},
		{
			name: "konto ohne passwort",
			env: func() map[string]string {
				e := minimalOIDC()
				e["FILEEE_MODE"] = "multi"
				e["FILEEE_ACCOUNTS"] = "anna"
				e["FILEEE_ACCOUNT_ANNA_USERNAME"] = "anna@example.com"
				e["FILEEE_ACCOUNT_ANNA_SUBJECTS"] = "anna"
				return e
			},
			erwartet: "_PASSWORD",
		},
		{
			name: "unzulaessiger konto-key",
			env: func() map[string]string {
				e := minimalOIDC()
				e["FILEEE_MODE"] = "multi"
				e["FILEEE_ACCOUNTS"] = "../../etc/x"
				return e
			},
			erwartet: "Konto-Key",
		},
		{
			name: "ein subject zeigt auf zwei konten",
			env: func() map[string]string {
				e := minimalOIDC()
				e["FILEEE_MODE"] = "multi"
				e["FILEEE_ACCOUNTS"] = "anna,bob"
				for _, k := range []string{"ANNA", "BOB"} {
					e["FILEEE_ACCOUNT_"+k+"_USERNAME"] = "u@example.com"
					e["FILEEE_ACCOUNT_"+k+"_PASSWORD"] = "geheim"
					e["FILEEE_ACCOUNT_"+k+"_SUBJECTS"] = "gemeinsam"
				}
				return e
			},
			erwartet: "zwei Konten",
		},
		{
			name: "unbekannter auth-modus",
			env: func() map[string]string {
				e := minimalToken()
				e["MCP_AUTH_MODE"] = "basic"
				return e
			},
			erwartet: "MCP_AUTH_MODE",
		},
		{
			name: "unbekannter konto-modus",
			env: func() map[string]string {
				e := minimalToken()
				e["FILEEE_MODE"] = "viele"
				return e
			},
			erwartet: "FILEEE_MODE",
		},
		{
			name: "nicht numerisches limit",
			env: func() map[string]string {
				e := minimalToken()
				e["FILEEE_MAX_UPLOAD_BYTES"] = "viel"
				return e
			},
			erwartet: "FILEEE_MAX_UPLOAD_BYTES",
		},
		{
			name: "unbrauchbare dauer",
			env: func() map[string]string {
				e := minimalToken()
				e["FILEEE_KEEPALIVE_INTERVAL"] = "bald"
				return e
			},
			erwartet: "FILEEE_KEEPALIVE_INTERVAL",
		},
		{
			name: "doppelter konto-key",
			env: func() map[string]string {
				e := minimalOIDC()
				e["FILEEE_MODE"] = "multi"
				e["FILEEE_ACCOUNTS"] = "anna,anna"
				e["FILEEE_ACCOUNT_ANNA_USERNAME"] = "anna@example.com"
				e["FILEEE_ACCOUNT_ANNA_PASSWORD"] = "geheim"
				e["FILEEE_ACCOUNT_ANNA_SUBJECTS"] = "anna"
				return e
			},
			erwartet: "mehrfach",
		},
		{
			name: "konto-keys kollidieren nach der praefix-normalisierung",
			env: func() map[string]string {
				e := minimalOIDC()
				e["FILEEE_MODE"] = "multi"
				e["FILEEE_ACCOUNTS"] = "foo-bar,foo_bar"
				e["FILEEE_ACCOUNT_FOO_BAR_USERNAME"] = "u@example.com"
				e["FILEEE_ACCOUNT_FOO_BAR_PASSWORD"] = "geheim"
				return e
			},
			erwartet: "dieselben Variablen",
		},
		{
			name: "negatives byte-limit",
			env: func() map[string]string {
				e := minimalToken()
				e["FILEEE_MAX_UPLOAD_BYTES"] = "-1"
				return e
			},
			erwartet: "FILEEE_MAX_UPLOAD_BYTES",
		},
		{
			name: "negative rate",
			env: func() map[string]string {
				e := minimalToken()
				e["FILEEE_RATE_RPS"] = "-0.5"
				return e
			},
			erwartet: "FILEEE_RATE_RPS",
		},
		{
			name: "negative issued-id-ttl",
			env: func() map[string]string {
				e := minimalToken()
				e["FILEEE_ISSUED_ID_TTL_SECONDS"] = "-1"
				return e
			},
			erwartet: "FILEEE_ISSUED_ID_TTL_SECONDS",
		},
		{
			name: "negativer issued-id-deckel",
			env: func() map[string]string {
				e := minimalToken()
				e["FILEEE_ISSUED_ID_MAX_PER_IDENTITY"] = "-1"
				return e
			},
			erwartet: "FILEEE_ISSUED_ID_MAX_PER_IDENTITY",
		},
		{
			name: "nulldauer beim keepalive",
			env: func() map[string]string {
				e := minimalToken()
				e["FILEEE_KEEPALIVE_INTERVAL"] = "-5m"
				return e
			},
			erwartet: "FILEEE_KEEPALIVE_INTERVAL",
		},
		{
			name: "single-modus ohne credentials",
			env: func() map[string]string {
				return map[string]string{"MCP_API_TOKEN": "t0ken"}
			},
			erwartet: "FILEEE_USERNAME",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := LoadConfig(envOf(tt.env()))
			if err == nil {
				t.Fatalf("LoadConfig = kein Fehler, erwartet Abbruch mit %q im Text", tt.erwartet)
			}
			if !strings.Contains(err.Error(), tt.erwartet) {
				t.Fatalf("Fehlermeldung %q enthaelt %q nicht — die Meldung muss die betroffene "+
					"Variable benennen, sonst sucht der Betreiber im Dunkeln", err.Error(), tt.erwartet)
			}
		})
	}
}

func TestLoadConfigMultiAccount(t *testing.T) {
	t.Parallel()

	env := minimalOIDC()
	delete(env, "MCP_ALLOWED_SUBJECTS") // im multi-Modus nicht noetig
	delete(env, "FILEEE_USERNAME")
	delete(env, "FILEEE_PASSWORD")
	env["FILEEE_MODE"] = "multi"
	env["FILEEE_ACCOUNTS"] = "anna, bob"
	env["FILEEE_ACCOUNT_ANNA_USERNAME"] = "anna@example.com"
	env["FILEEE_ACCOUNT_ANNA_PASSWORD"] = "geheim"
	env["FILEEE_ACCOUNT_ANNA_SUBJECTS"] = "anna-sub, anna-zweit"
	env["FILEEE_ACCOUNT_BOB_USERNAME"] = "bob@example.com"
	env["FILEEE_ACCOUNT_BOB_PASSWORD"] = "geheim"
	env["FILEEE_ACCOUNT_BOB_SUBJECTS"] = "bob-sub"

	cfg, err := LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("LoadConfig = Fehler %v", err)
	}
	if len(cfg.Accounts) != 2 {
		t.Fatalf("erwartet 2 Konten, bekommen %d", len(cfg.Accounts))
	}

	anna := cfg.Accounts[0]
	if anna.Key != "anna" {
		t.Errorf("Key = %q, erwartet %q — Leerraum um den Key muss getrimmt werden", anna.Key, "anna")
	}
	if len(anna.Subjects) != 2 {
		t.Errorf("Subjects = %v, erwartet zwei Eintraege", anna.Subjects)
	}

	if key, ok := cfg.AccountBySubject("anna-zweit"); !ok || key != "anna" {
		t.Errorf("AccountBySubject(anna-zweit) = %q/%v, erwartet anna/true", key, ok)
	}
	if _, ok := cfg.AccountBySubject("unbekannt"); ok {
		t.Error("AccountBySubject(unbekannt) = true, erwartet false — kein Fallback auf ein Standardkonto")
	}
}

// Der token-Modus auf einem oeffentlich erreichbaren Endpunkt ist zulaessig,
// aber erklaerungsbeduerftig: der Zugriff haengt dann an einem einzigen String.
// Das ist eine Warnung, kein Abbruch — sonst braeche man legitime Setups.
func TestLoadConfigWarntBeiTokenModusMitOeffentlicherURL(t *testing.T) {
	t.Parallel()

	env := minimalToken()
	env["MCP_RESOURCE_URL"] = "https://mcp.example.com/mcp"

	cfg, err := LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("LoadConfig = Fehler %v, erwartet nur eine Warnung", err)
	}
	if len(cfg.Warnings) == 0 {
		t.Fatal("keine Warnung erzeugt, erwartet einen Hinweis zum token-Modus auf oeffentlicher URL")
	}

	for _, lokal := range []string{
		"http://127.0.0.1:8080/mcp",
		"http://localhost:8080/mcp",
		"http://[::1]:8080/mcp",
	} {
		env["MCP_RESOURCE_URL"] = lokal
		cfg, err = LoadConfig(envOf(env))
		if err != nil {
			t.Fatalf("LoadConfig(%s) = Fehler %v", lokal, err)
		}
		for _, w := range cfg.Warnings {
			if strings.Contains(w, "MCP_AUTH_MODE") {
				t.Fatalf("Warnung %q bei Loopback-Adresse %s, erwartet keine", w, lokal)
			}
		}
	}
}

func TestLoadConfigListenUndZahlenwerte(t *testing.T) {
	t.Parallel()

	env := minimalToken()
	env["FILEEE_TRUSTED_PROXIES"] = "10.0.0.0/8,192.168.0.1"
	env["FILEEE_MAX_INFLIGHT"] = "3"
	env["FILEEE_RATE_RPS"] = "2"
	env["FILEEE_RATE_GLOBAL_RPS"] = "5"

	cfg, err := LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("LoadConfig = Fehler %v", err)
	}
	if len(cfg.TrustedProxies) != 2 {
		t.Errorf("TrustedProxies = %v, erwartet zwei Eintraege", cfg.TrustedProxies)
	}
	// Der zweite Eintrag ist eine nackte Adresse ohne Maske — sie muss als
	// Praefix mit voller Bitlaenge ankommen, nicht verworfen oder als /0
	// interpretiert werden (das waere "jede Adresse", das Gegenteil der Absicht).
	if bits := cfg.TrustedProxies[1].Bits(); bits != 32 {
		t.Errorf("TrustedProxies[1].Bits() = %d, erwartet 32 (nackte IPv4-Adresse als /32)", bits)
	}
	if cfg.MaxInflight != 3 {
		t.Errorf("MaxInflight = %d, erwartet 3", cfg.MaxInflight)
	}
	if cfg.RateRPS != 2 || cfg.RateGlobalRPS != 5 {
		t.Errorf("RateRPS/RateGlobalRPS = %v/%v, erwartet 2/5", cfg.RateRPS, cfg.RateGlobalRPS)
	}
}

func TestLoadConfigClientIPHeaderModeDefault(t *testing.T) {
	t.Parallel()

	cfg, err := LoadConfig(envOf(minimalToken()))
	if err != nil {
		t.Fatalf("LoadConfig = Fehler %v", err)
	}
	if cfg.ClientIPHeaderMode != origin.ModeCFConnectingIP {
		t.Errorf("ClientIPHeaderMode = %q, erwartet Default %q", cfg.ClientIPHeaderMode, origin.ModeCFConnectingIP)
	}
}

// TestLoadConfigLogLevelDefault belegt den Vorgabewert von FILEEE_LOG_LEVEL —
// diag.LevelInfo, die leisere der beiden Stufen (siehe internal/diag).
func TestLoadConfigLogLevelDefault(t *testing.T) {
	t.Parallel()

	cfg, err := LoadConfig(envOf(minimalToken()))
	if err != nil {
		t.Fatalf("LoadConfig = Fehler %v", err)
	}
	if cfg.LogLevel != diag.LevelInfo {
		t.Errorf("LogLevel = %q, erwartet Default %q", cfg.LogLevel, diag.LevelInfo)
	}
}

// TestLoadConfigLogLevelDebug belegt, dass FILEEE_LOG_LEVEL=debug tatsaechlich
// ankommt — das Gegenstueck zum Default-Test oben.
func TestLoadConfigLogLevelDebug(t *testing.T) {
	t.Parallel()

	env := minimalToken()
	env["FILEEE_LOG_LEVEL"] = "debug"

	cfg, err := LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("LoadConfig = Fehler %v", err)
	}
	if cfg.LogLevel != diag.LevelDebug {
		t.Errorf("LogLevel = %q, erwartet %q", cfg.LogLevel, diag.LevelDebug)
	}
}

// TestLoadConfigIssuedIDDefaults belegt die Vorgabewerte von
// FILEEE_ISSUED_ID_TTL_SECONDS/FILEEE_ISSUED_ID_MAX_PER_IDENTITY — 30
// Minuten bzw. 1000 IDs je Identität (Config.IssuedIDTTLSeconds/
// IssuedIDMaxPerIdentity eigene Doc-Kommentare).
func TestLoadConfigIssuedIDDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := LoadConfig(envOf(minimalToken()))
	if err != nil {
		t.Fatalf("LoadConfig = Fehler %v", err)
	}
	if cfg.IssuedIDTTLSeconds != 1800 {
		t.Errorf("IssuedIDTTLSeconds = %d, erwartet Default 1800", cfg.IssuedIDTTLSeconds)
	}
	if cfg.IssuedIDMaxPerIdentity != 1000 {
		t.Errorf("IssuedIDMaxPerIdentity = %d, erwartet Default 1000", cfg.IssuedIDMaxPerIdentity)
	}
}

// TestLoadConfigIssuedIDWerteAusDerUmgebung belegt, dass beide Werte aus der
// Umgebung tatsächlich ankommen — das Gegenstück zum Default-Test oben.
func TestLoadConfigIssuedIDWerteAusDerUmgebung(t *testing.T) {
	t.Parallel()

	env := minimalToken()
	env["FILEEE_ISSUED_ID_TTL_SECONDS"] = "60"
	env["FILEEE_ISSUED_ID_MAX_PER_IDENTITY"] = "5"

	cfg, err := LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("LoadConfig = Fehler %v", err)
	}
	if cfg.IssuedIDTTLSeconds != 60 || cfg.IssuedIDMaxPerIdentity != 5 {
		t.Errorf("IssuedIDTTLSeconds/IssuedIDMaxPerIdentity = %d/%d, erwartet 60/5",
			cfg.IssuedIDTTLSeconds, cfg.IssuedIDMaxPerIdentity)
	}
}

func TestLoadConfigNetzwerkPraefixe(t *testing.T) {
	t.Parallel()

	env := minimalOIDC()
	env["FILEEE_CLIENT_IP_HEADER_MODE"] = "x-forwarded-for"
	env["FILEEE_ALLOWED_ORIGIN_PREFIXES"] = "10.0.0.0/8, ::1"

	cfg, err := LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("LoadConfig = Fehler %v", err)
	}
	if cfg.ClientIPHeaderMode != origin.ModeXForwardedFor {
		t.Errorf("ClientIPHeaderMode = %q, erwartet %q", cfg.ClientIPHeaderMode, origin.ModeXForwardedFor)
	}
	if len(cfg.AllowedOriginPrefixes) != 2 {
		t.Fatalf("AllowedOriginPrefixes = %v, erwartet zwei Eintraege", cfg.AllowedOriginPrefixes)
	}
	if bits := cfg.AllowedOriginPrefixes[1].Bits(); bits != 128 {
		t.Errorf("AllowedOriginPrefixes[1].Bits() = %d, erwartet 128 (nackte IPv6-Adresse als /128)", bits)
	}
}

// --- MCP_OIDC_ADVERTISED_SCOPES ---------------------------------------------
//
// Getrennt von MCP_OIDC_REQUIRED_SCOPES: RequiredScopes bleibt, wogegen
// scopesSatisfied den scp/scope-Claim des Tokens prueft (siehe scopes.go).
// AdvertisedScopes ist NEU und beeinflusst ausschliesslich, was VOR einem
// Token-Austausch angekuendigt wird (WWW-Authenticate "scope"-Parameter und
// RFC-9728 scopes_supported, siehe internal/server/server.go). Der Anlass:
// Entra weist einen nackten Scope-Namen wie "mcp.access" beim Token-Austausch
// mit AADSTS650053 zurueck ("scope ... that doesn't exist on the resource
// 'Microsoft Graph'") -- angekuendigt werden muss dort die vollqualifizierte
// Form (z. B. "https://<host>/mcp/mcp.access"), waehrend das Token im
// scp-Claim weiterhin nur den kurzen Namen traegt. Fuer Authentik (kurze
// Namen auf beiden Seiten) bleibt die Variable ungesetzt.

// TestLoadConfigDefaultsAdvertisedScopesToEmpty ist die
// Abwaertskompatibilitaets-Regression: ohne MCP_OIDC_ADVERTISED_SCOPES darf
// sich nichts aendern -- server.go faellt dann auf OIDCRequiredScopes zurueck
// (siehe dort).
func TestLoadConfigDefaultsAdvertisedScopesToEmpty(t *testing.T) {
	t.Parallel()

	cfg, err := LoadConfig(envOf(minimalOIDC()))
	if err != nil {
		t.Fatalf("LoadConfig = Fehler %v", err)
	}
	if len(cfg.OIDCAdvertisedScopes) != 0 {
		t.Errorf("OIDCAdvertisedScopes = %v, erwartet leer (MCP_OIDC_ADVERTISED_SCOPES nicht gesetzt)",
			cfg.OIDCAdvertisedScopes)
	}
}

// TestLoadConfigParsesAdvertisedScopesFullyQualifiedEntraValue ist der Grund,
// warum diese Variable ueberhaupt existiert: eine vollqualifizierte,
// Entra-taugliche Form mit Doppelpunkt und Schraegstrichen muss unveraendert
// durchgereicht werden -- eine Validierung, die solche Zeichen ablehnen
// wuerde, waere fuer genau den Fall unbrauchbar, den die Variable loesen soll.
func TestLoadConfigParsesAdvertisedScopesFullyQualifiedEntraValue(t *testing.T) {
	t.Parallel()

	env := minimalOIDC()
	env["MCP_OIDC_ADVERTISED_SCOPES"] = "https://fileee-mcp-entra.strausmann.cloud/mcp/mcp.access"

	cfg, err := LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("LoadConfig = Fehler %v", err)
	}
	want := []string{"https://fileee-mcp-entra.strausmann.cloud/mcp/mcp.access"}
	if !slices.Equal(cfg.OIDCAdvertisedScopes, want) {
		t.Errorf("OIDCAdvertisedScopes = %v, erwartet %v", cfg.OIDCAdvertisedScopes, want)
	}
}

// TestLoadConfigParsesMultipleAdvertisedScopesCommaSeparated spiegelt das
// bestehende Verhalten von MCP_OIDC_REQUIRED_SCOPES (splitListe: trimmt
// Leerraum, verwirft leere Eintraege) -- dieselbe Kommaliste-Konvention wie
// jede andere Listen-Variable in dieser Datei.
func TestLoadConfigParsesMultipleAdvertisedScopesCommaSeparated(t *testing.T) {
	t.Parallel()

	env := minimalOIDC()
	env["MCP_OIDC_ADVERTISED_SCOPES"] = " api://app-id/mcp.access , ,api://app-id/offline_access"

	cfg, err := LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("LoadConfig = Fehler %v", err)
	}
	want := []string{"api://app-id/mcp.access", "api://app-id/offline_access"}
	if !slices.Equal(cfg.OIDCAdvertisedScopes, want) {
		t.Errorf("OIDCAdvertisedScopes = %v, erwartet %v", cfg.OIDCAdvertisedScopes, want)
	}
}

// minimalEntra ist die kleinstmoegliche gueltige Entra-Konfiguration.
func minimalEntra() map[string]string {
	e := minimalOIDC()
	delete(e, "MCP_OIDC_ISSUER")
	delete(e, "MCP_OIDC_CLIENT_ID")
	e["MCP_OIDC_PROVIDER"] = "entra"
	e["MCP_ENTRA_TENANT_ID"] = testTenantID
	e["MCP_ENTRA_CLIENT_ID"] = "11111111-2222-3333-4444-555555555555"
	return e
}

// minimalAuthentik ist die kleinstmoegliche gueltige Authentik-Konfiguration.
func minimalAuthentik() map[string]string {
	e := minimalOIDC()
	delete(e, "MCP_OIDC_ISSUER")
	delete(e, "MCP_OIDC_CLIENT_ID")
	e["MCP_OIDC_PROVIDER"] = "authentik"
	e["MCP_AUTHENTIK_BASE_URL"] = "https://auth.example.com"
	e["MCP_AUTHENTIK_APP_SLUG"] = "fileee-mcp"
	e["MCP_AUTHENTIK_CLIENT_ID"] = "client-abc"
	return e
}

const testTenantID = "ba37bc14-c21d-4f90-8d78-27054e628e15"

func TestLoadConfigProviderEntra(t *testing.T) {
	t.Parallel()

	t.Run("baut die Aussteller-URL aus der Verzeichnis-ID", func(t *testing.T) {
		t.Parallel()
		cfg, err := LoadConfig(envOf(minimalEntra()))
		if err != nil {
			t.Fatalf("unerwarteter Fehler: %v", err)
		}
		want := "https://login.microsoftonline.com/" + testTenantID + "/v2.0"
		if cfg.OIDCIssuer != want {
			t.Errorf("Aussteller = %q, erwartet %q", cfg.OIDCIssuer, want)
		}
		if cfg.OIDCClientID != "11111111-2222-3333-4444-555555555555" {
			t.Errorf("Client-ID = %q", cfg.OIDCClientID)
		}
	})

	t.Run("entfernt Leerraum um die Verzeichnis-ID", func(t *testing.T) {
		t.Parallel()
		e := minimalEntra()
		e["MCP_ENTRA_TENANT_ID"] = "  " + testTenantID + "\n"
		cfg, err := LoadConfig(envOf(e))
		if err != nil {
			t.Fatalf("unerwarteter Fehler: %v", err)
		}
		if !strings.Contains(cfg.OIDCIssuer, testTenantID) || strings.Contains(cfg.OIDCIssuer, " ") {
			t.Errorf("Aussteller = %q", cfg.OIDCIssuer)
		}
	})

	// Diese drei Werte liefern eine Aussteller-URL, die NIE zum Token passt —
	// sie muessen beim Start scheitern, nicht in einer 401-Schleife zur Laufzeit.
	for _, bad := range []string{"common", "organizations", "example.com"} {
		t.Run("weist "+bad+" ab", func(t *testing.T) {
			t.Parallel()
			e := minimalEntra()
			e["MCP_ENTRA_TENANT_ID"] = bad
			_, err := LoadConfig(envOf(e))
			if err == nil {
				t.Fatalf("erwartet: Fehler fuer %q", bad)
			}
			if !strings.Contains(err.Error(), "MCP_ENTRA_TENANT_ID") {
				t.Errorf("Meldung nennt die Variable nicht: %v", err)
			}
		})
	}

	for _, missing := range []string{"MCP_ENTRA_TENANT_ID", "MCP_ENTRA_CLIENT_ID"} {
		t.Run("bricht ohne "+missing+" ab", func(t *testing.T) {
			t.Parallel()
			e := minimalEntra()
			delete(e, missing)
			_, err := LoadConfig(envOf(e))
			if err == nil {
				t.Fatalf("erwartet: Fehler ohne %s", missing)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("Meldung nennt %s nicht: %v", missing, err)
			}
		})
	}
}

func TestLoadConfigProviderAuthentik(t *testing.T) {
	t.Parallel()

	t.Run("baut die Aussteller-URL aus Host und Kuerzel", func(t *testing.T) {
		t.Parallel()
		cfg, err := LoadConfig(envOf(minimalAuthentik()))
		if err != nil {
			t.Fatalf("unerwarteter Fehler: %v", err)
		}
		want := "https://auth.example.com/application/o/fileee-mcp/"
		if cfg.OIDCIssuer != want {
			t.Errorf("Aussteller = %q, erwartet %q", cfg.OIDCIssuer, want)
		}
	})

	t.Run("doppelter Schraegstrich am Host erzeugt keinen doppelten Pfad", func(t *testing.T) {
		t.Parallel()
		e := minimalAuthentik()
		e["MCP_AUTHENTIK_BASE_URL"] = "https://auth.example.com/"
		cfg, err := LoadConfig(envOf(e))
		if err != nil {
			t.Fatalf("unerwarteter Fehler: %v", err)
		}
		want := "https://auth.example.com/application/o/fileee-mcp/"
		if cfg.OIDCIssuer != want {
			t.Errorf("Aussteller = %q, erwartet %q", cfg.OIDCIssuer, want)
		}
	})

	t.Run("weist eine Nicht-https-Adresse ab", func(t *testing.T) {
		t.Parallel()
		e := minimalAuthentik()
		e["MCP_AUTHENTIK_BASE_URL"] = "http://auth.example.com"
		_, err := LoadConfig(envOf(e))
		if err == nil || !strings.Contains(err.Error(), "MCP_AUTHENTIK_BASE_URL") {
			t.Fatalf("erwartet: Fehler zur Basis-Adresse, bekam %v", err)
		}
	})

	t.Run("weist ein Kuerzel mit Pfadanteil ab", func(t *testing.T) {
		t.Parallel()
		e := minimalAuthentik()
		e["MCP_AUTHENTIK_APP_SLUG"] = "fileee/../admin"
		_, err := LoadConfig(envOf(e))
		if err == nil || !strings.Contains(err.Error(), "MCP_AUTHENTIK_APP_SLUG") {
			t.Fatalf("erwartet: Fehler zum Kuerzel, bekam %v", err)
		}
	})

	for _, missing := range []string{"MCP_AUTHENTIK_BASE_URL", "MCP_AUTHENTIK_APP_SLUG", "MCP_AUTHENTIK_CLIENT_ID"} {
		t.Run("bricht ohne "+missing+" ab", func(t *testing.T) {
			t.Parallel()
			e := minimalAuthentik()
			delete(e, missing)
			_, err := LoadConfig(envOf(e))
			if err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("erwartet: Fehler zu %s, bekam %v", missing, err)
			}
		})
	}
}

func TestLoadConfigProviderSelection(t *testing.T) {
	t.Parallel()

	t.Run("fehlender Anbieter nennt alle drei", func(t *testing.T) {
		t.Parallel()
		e := minimalOIDC()
		delete(e, "MCP_OIDC_PROVIDER")
		_, err := LoadConfig(envOf(e))
		if err == nil {
			t.Fatal("erwartet: Fehler ohne Anbieter")
		}
		for _, want := range []string{"entra", "authentik", "generic"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Meldung nennt %q nicht: %v", want, err)
			}
		}
	})

	t.Run("unbekannter Anbieter wird abgewiesen", func(t *testing.T) {
		t.Parallel()
		e := minimalOIDC()
		e["MCP_OIDC_PROVIDER"] = "keycloak"
		_, err := LoadConfig(envOf(e))
		if err == nil || !strings.Contains(err.Error(), "keycloak") {
			t.Fatalf("erwartet: Fehler zum unbekannten Anbieter, bekam %v", err)
		}
	})

	// Eine fremde Variable waere sonst wirkungslos gesetzt — der Betreiber
	// suchte den Fehler an einer Einstellung, die gar nicht gelesen wird.
	t.Run("Variablen eines anderen Anbieters brechen ab", func(t *testing.T) {
		t.Parallel()
		e := minimalEntra()
		e["MCP_AUTHENTIK_APP_SLUG"] = "fileee-mcp"
		_, err := LoadConfig(envOf(e))
		if err == nil {
			t.Fatal("erwartet: Fehler wegen gemischter Anbieter-Variablen")
		}
		if !strings.Contains(err.Error(), "MCP_AUTHENTIK_APP_SLUG") {
			t.Errorf("Meldung nennt die fremde Variable nicht: %v", err)
		}
	})

	t.Run("generic reicht Aussteller und Client-ID durch", func(t *testing.T) {
		t.Parallel()
		cfg, err := LoadConfig(envOf(minimalOIDC()))
		if err != nil {
			t.Fatalf("unerwarteter Fehler: %v", err)
		}
		if cfg.OIDCIssuer != "https://idp.example.com/application/o/mcp/" {
			t.Errorf("Aussteller = %q", cfg.OIDCIssuer)
		}
		if cfg.OIDCClientID != "mcp-client" {
			t.Errorf("Client-ID = %q", cfg.OIDCClientID)
		}
	})
}

// Authentik laesst sich unter einem Unterpfad betreiben. Die Aussteller-URL
// muss diesen Pfad behalten, sonst zeigt sie ins Leere.
func TestLoadConfigAuthentikUnterpfad(t *testing.T) {
	t.Parallel()

	e := minimalAuthentik()
	e["MCP_AUTHENTIK_BASE_URL"] = "https://host.example.com/authentik"

	cfg, err := LoadConfig(envOf(e))
	if err != nil {
		t.Fatalf("unerwarteter Fehler: %v", err)
	}
	want := "https://host.example.com/authentik/application/o/fileee-mcp/"
	if cfg.OIDCIssuer != want {
		t.Errorf("Aussteller = %q, erwartet %q", cfg.OIDCIssuer, want)
	}
}

// Im Modus token wertet der Server keine Anbieter-Einstellung aus. Sie still
// zu ignorieren waere derselbe Fehler, den rejectForeignProviderVariables
// bereits verhindert: Der Betreiber sucht an einer Stelle, die nicht gelesen
// wird.
func TestLoadConfigProviderImTokenModus(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"MCP_OIDC_PROVIDER", "MCP_ENTRA_TENANT_ID", "MCP_AUTHENTIK_APP_SLUG", "MCP_OIDC_ISSUER"} {
		t.Run(key+" wird abgewiesen", func(t *testing.T) {
			t.Parallel()
			e := minimalToken()
			e[key] = "irgendwas"

			_, err := LoadConfig(envOf(e))
			if err == nil {
				t.Fatalf("erwartet: Fehler, weil %s im Modus token nicht gelesen wird", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("Meldung nennt %s nicht: %v", key, err)
			}
		})
	}

	t.Run("saubere token-Konfiguration bleibt gueltig", func(t *testing.T) {
		t.Parallel()
		if _, err := LoadConfig(envOf(minimalToken())); err != nil {
			t.Fatalf("unerwarteter Fehler: %v", err)
		}
	})
}

// Der sinnvolle Subject-Claim folgt aus dem Anbieter: Bei Entra ist `sub`
// paarweise pseudonymisiert und im Portal nirgends ablesbar, `oid` dagegen
// schon. Wer entra waehlt, soll das nicht zusaetzlich eintragen muessen.
func TestLoadConfigSubjectClaimDefaultProProvider(t *testing.T) {
	t.Parallel()

	faelle := []struct {
		name string
		env  func() map[string]string
		want string
	}{
		{"entra ohne Angabe nutzt oid", minimalEntra, "oid"},
		{"authentik ohne Angabe nutzt sub", minimalAuthentik, "sub"},
		{"generic ohne Angabe nutzt sub", minimalOIDC, "sub"},
	}
	for _, f := range faelle {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			e := f.env()
			delete(e, "MCP_OIDC_SUBJECT_CLAIM")

			cfg, err := LoadConfig(envOf(e))
			if err != nil {
				t.Fatalf("unerwarteter Fehler: %v", err)
			}
			if cfg.OIDCSubjectClaim != f.want {
				t.Errorf("Subject-Claim = %q, erwartet %q", cfg.OIDCSubjectClaim, f.want)
			}
		})
	}

	t.Run("ausdrueckliche Angabe schlaegt den Vorgabewert", func(t *testing.T) {
		t.Parallel()
		e := minimalEntra()
		e["MCP_OIDC_SUBJECT_CLAIM"] = "sub"

		cfg, err := LoadConfig(envOf(e))
		if err != nil {
			t.Fatalf("unerwarteter Fehler: %v", err)
		}
		if cfg.OIDCSubjectClaim != "sub" {
			t.Errorf("Subject-Claim = %q, erwartet sub", cfg.OIDCSubjectClaim)
		}
	})
}

func TestInstanceDescriptionWirdGelesenUndBegrenzt(t *testing.T) {
	basis := map[string]string{
		"MCP_AUTH_MODE":                  "oidc",
		"MCP_OIDC_PROVIDER":              "generic",
		"MCP_OIDC_ISSUER":                "https://idp.example.invalid",
		"MCP_OIDC_CLIENT_ID":             "fileee-mcp-server",
		"MCP_RESOURCE_URL":               "https://mcp.example.invalid/mcp",
		"MCP_ALLOWED_SUBJECTS":           "test-subject",
		"FILEEE_ALLOWED_ORIGIN_PREFIXES": "0.0.0.0/0",
		"FILEEE_USERNAME":                "test@example.invalid",
		"FILEEE_PASSWORD":                "kein-echtes-passwort",
	}
	umgebung := func(zusatz map[string]string) func(string) string {
		werte := map[string]string{}
		for k, v := range basis {
			werte[k] = v
		}
		for k, v := range zusatz {
			werte[k] = v
		}
		return func(schluessel string) string { return werte[schluessel] }
	}

	t.Run("nicht gesetzt ergibt einen leeren Wert", func(t *testing.T) {
		cfg, err := LoadConfig(umgebung(nil))
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if cfg.InstanceDescription != "" {
			t.Errorf("InstanceDescription = %q, erwartet leer", cfg.InstanceDescription)
		}
	})

	t.Run("gesetzter Wert kommt unveraendert an", func(t *testing.T) {
		// Umlaute und ein Zeilenumbruch muessen den Weg ueberstehen.
		will := "Produktives Archiv.\nSchreibende Aufrufe nur auf ausdrückliche Anweisung."
		cfg, err := LoadConfig(umgebung(map[string]string{"MCP_INSTANCE_DESCRIPTION": will}))
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if cfg.InstanceDescription != will {
			t.Errorf("InstanceDescription = %q, erwartet %q", cfg.InstanceDescription, will)
		}
	})

	t.Run("zu langer Wert bricht den Start ab", func(t *testing.T) {
		_, err := LoadConfig(umgebung(map[string]string{
			"MCP_INSTANCE_DESCRIPTION": strings.Repeat("x", maxInstanceDescriptionRunes+1),
		}))
		if err == nil {
			t.Fatal("LoadConfig gab keinen Fehler, erwartet wurde einer")
		}
		if !strings.Contains(err.Error(), "MCP_INSTANCE_DESCRIPTION") {
			t.Errorf("Fehlermeldung nennt die Variable nicht: %v", err)
		}
		if !strings.Contains(err.Error(), "2000") {
			t.Errorf("Fehlermeldung nennt die Grenze nicht: %v", err)
		}
	})

	t.Run("die Grenze zaehlt Zeichen, nicht Bytes", func(t *testing.T) {
		// 2000 Umlaute sind 4000 Bytes. Wuerde len() statt
		// utf8.RuneCountInString() zaehlen, schluege dieser Fall
		// faelschlich fehl.
		cfg, err := LoadConfig(umgebung(map[string]string{
			"MCP_INSTANCE_DESCRIPTION": strings.Repeat("ä", maxInstanceDescriptionRunes),
		}))
		if err != nil {
			t.Fatalf("LoadConfig lehnte 2000 Zeichen ab: %v", err)
		}
		if cfg.InstanceDescription == "" {
			t.Error("InstanceDescription ist leer, erwartet wurden 2000 Zeichen")
		}
	})
}
