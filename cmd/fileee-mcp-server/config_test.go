package main

import (
	"strings"
	"testing"
	"time"
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
		"MCP_AUTH_MODE":        "oidc",
		"MCP_OIDC_ISSUER":      "https://idp.example.com/application/o/mcp/",
		"MCP_RESOURCE_URL":     "https://mcp.example.com/mcp",
		"MCP_ALLOWED_SUBJECTS": "abc123",
		"FILEEE_USERNAME":      "nutzer@example.com",
		"FILEEE_PASSWORD":      "geheim",
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
	if got := cfg.Capabilities.String(); got != "read" {
		t.Errorf("Capabilities = %q, erwartet %q — der sichere Default ist read", got, "read")
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
	if cfg.AllowDestructive {
		t.Error("AllowDestructive = true, erwartet false")
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
			name: "konto-capability ueberschreitet die obergrenze",
			env: func() map[string]string {
				e := minimalOIDC()
				e["FILEEE_CAPABILITIES"] = "read"
				e["FILEEE_MODE"] = "multi"
				e["FILEEE_ACCOUNTS"] = "anna"
				e["FILEEE_ACCOUNT_ANNA_USERNAME"] = "anna@example.com"
				e["FILEEE_ACCOUNT_ANNA_PASSWORD"] = "geheim"
				e["FILEEE_ACCOUNT_ANNA_SUBJECTS"] = "anna"
				e["FILEEE_ACCOUNT_ANNA_CAPABILITIES"] = "read,write"
				return e
			},
			erwartet: "Obergrenze",
		},
		{
			name: "destructive ohne das zweite gate",
			env: func() map[string]string {
				e := minimalToken()
				e["FILEEE_CAPABILITIES"] = "read,destructive"
				return e
			},
			erwartet: "FILEEE_ALLOW_DESTRUCTIVE",
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
			name: "unbekannte capability",
			env: func() map[string]string {
				e := minimalToken()
				e["FILEEE_CAPABILITIES"] = "read,admin"
				return e
			},
			erwartet: "unbekannte capability",
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
	env["FILEEE_CAPABILITIES"] = "read,write,share"
	env["FILEEE_ACCOUNTS"] = "anna, bob"
	env["FILEEE_ACCOUNT_ANNA_USERNAME"] = "anna@example.com"
	env["FILEEE_ACCOUNT_ANNA_PASSWORD"] = "geheim"
	env["FILEEE_ACCOUNT_ANNA_SUBJECTS"] = "anna-sub, anna-zweit"
	env["FILEEE_ACCOUNT_ANNA_CAPABILITIES"] = "read"
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
	if !anna.HasCapabilities || anna.Capabilities.String() != "read" {
		t.Errorf("Capabilities = %q (gesetzt: %v), erwartet %q",
			anna.Capabilities.String(), anna.HasCapabilities, "read")
	}
	if cfg.Accounts[1].HasCapabilities {
		t.Error("bob hat keine eigene Capability-Einstellung, HasCapabilities muss false sein")
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
	env["MCP_ALLOWED_HOSTS"] = "mcp.example.com, mcp2.example.com"
	env["FILEEE_TRUSTED_PROXIES"] = "10.0.0.0/8,192.168.0.1"
	env["FILEEE_MAX_INFLIGHT"] = "3"
	env["FILEEE_RATE_RPS"] = "2"
	env["FILEEE_RATE_GLOBAL_RPS"] = "5"

	cfg, err := LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("LoadConfig = Fehler %v", err)
	}
	if len(cfg.AllowedHosts) != 2 || cfg.AllowedHosts[1] != "mcp2.example.com" {
		t.Errorf("AllowedHosts = %v, erwartet zwei getrimmte Eintraege", cfg.AllowedHosts)
	}
	if len(cfg.TrustedProxies) != 2 {
		t.Errorf("TrustedProxies = %v, erwartet zwei Eintraege", cfg.TrustedProxies)
	}
	if cfg.MaxInflight != 3 {
		t.Errorf("MaxInflight = %d, erwartet 3", cfg.MaxInflight)
	}
	if cfg.RateRPS != 2 || cfg.RateGlobalRPS != 5 {
		t.Errorf("RateRPS/RateGlobalRPS = %v/%v, erwartet 2/5", cfg.RateRPS, cfg.RateGlobalRPS)
	}
}
