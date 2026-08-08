package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/strausmann/fileee-mcp-server/internal/config"
)

// envOf baut ein config.Env aus einer Map — dieselbe kleine Hilfsfunktion wie
// in internal/config/config_test.go und internal/server/server_test.go, hier
// lokal dupliziert statt importiert: Test-Helfer eines fremden Pakets sind
// ausserhalb dieses Pakets nicht sichtbar, und ein eigenes Test-Hilfspaket
// waere fuer eine Zeile Logik unverhaeltnismaessig.
func envOf(m map[string]string) config.Env {
	return func(key string) string { return m[key] }
}

func TestRunVersionSubcommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"version"}, envOf(nil), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run(version) = Exit-Code %d, erwartet 0", code)
	}
	if got := strings.TrimSpace(stdout.String()); got != config.Version() {
		t.Errorf("stdout = %q, erwartet %q", got, config.Version())
	}
}

// Ein Start ohne Pflichtangaben muss fehlschlagen, statt offen hochzukommen —
// LoadConfig liefert den Fehler, run() darf ihn nicht verschlucken oder erst
// beim ersten Request bemerken.
func TestRunFailsFastWithoutRequiredConfig(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run(nil, envOf(nil), &stdout, &stderr)

	if code == 0 {
		t.Fatalf("run() = Exit-Code 0 bei leerer Konfiguration, erwartet einen Fehler-Code")
	}
	// Bei leerem Env schlaegt LoadConfig auf der ersten fehlenden Pflichtvariable
	// fehl (Default-Modus ist token, siehe internal/config/config.go) — das ist MCP_API_TOKEN,
	// nicht FILEEE_USERNAME. Entscheidend ist hier nicht WELCHE Variable zuerst
	// benannt wird, sondern DASS run() die von LoadConfig benannte Variable
	// unveraendert durchreicht, statt sie durch eine generische Meldung zu
	// ersetzen.
	if !strings.Contains(stderr.String(), "MCP_API_TOKEN") {
		t.Errorf("stderr = %q, erwartet einen Hinweis auf die fehlende Pflichtvariable MCP_API_TOKEN",
			stderr.String())
	}
}

// Ein AuthMode, den dieser Server (noch) nicht bedienen kann (siehe New in
// internal/server/server.go), muss beim Start scheitern — nicht erst beim
// ersten Request.
func TestRunFailsFastForUnsupportedAuthMode(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"MCP_API_TOKEN":   "t0ken",
		"FILEEE_USERNAME": "nutzer@example.com",
		"FILEEE_PASSWORD": "geheim",
	}

	var stdout, stderr bytes.Buffer
	code := run(nil, envOf(env), &stdout, &stderr)

	if code == 0 {
		t.Fatalf("run() = Exit-Code 0 im token-Modus, erwartet einen Fehler-Code — "+
			"MCP_AUTH_MODE=token wird von Gangway (noch) nicht bedient (stdout=%q)", stdout.String())
	}
	if !strings.Contains(stderr.String(), "MCP_AUTH_MODE") {
		t.Errorf("stderr = %q, erwartet einen Hinweis auf MCP_AUTH_MODE", stderr.String())
	}
}

// Netzwerkfehler-Pfad auf run()-Ebene: eine gueltige, aber zur Laufzeit nicht
// aufbaubare Konfiguration (Issuer unerreichbar) muss ebenso fehlschlagen wie
// eine unvollstaendige — New() ist der zweite Ort, an dem der Start scheitern
// kann, nicht nur LoadConfig().
func TestRunFailsWhenServerCannotBeBuilt(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"MCP_AUTH_MODE":                  "oidc",
		"MCP_OIDC_ISSUER":                "http://127.0.0.1:1", // sofort verweigert, siehe server_test.go
		"MCP_OIDC_AUDIENCE":              "fileee-mcp-server",
		"MCP_RESOURCE_URL":               "https://mcp.example.com/mcp",
		"MCP_ALLOWED_SUBJECTS":           "abc123",
		"FILEEE_ALLOWED_ORIGIN_PREFIXES": "0.0.0.0/0",
		"FILEEE_USERNAME":                "nutzer@example.com",
		"FILEEE_PASSWORD":                "geheim",
	}

	var stdout, stderr bytes.Buffer
	code := run(nil, envOf(env), &stdout, &stderr)

	if code == 0 {
		t.Fatalf("run() = Exit-Code 0, erwartet einen Fehlschlag — der Identity Provider ist "+
			"nicht erreichbar (stdout=%q)", stdout.String())
	}
	if strings.Contains(stdout.String(), "hoert auf") {
		t.Errorf("stdout = %q, enthaelt eine \"hoert auf\"-Meldung, obwohl der Start fehlgeschlagen ist",
			stdout.String())
	}
	// Zugangsdaten duerfen auch auf diesem Weg nie in der Ausgabe landen.
	if strings.Contains(stdout.String()+stderr.String(), "geheim") {
		t.Error("Ausgabe enthaelt das Fileee-Passwort aus der Konfiguration")
	}
}

// Absicherung (Prüfbefund): reportWarnings darf bei cfg == nil nicht
// abstuerzen. LoadConfig liefert heute nie (nil, nil) — run() ruft
// reportWarnings ausschliesslich nach einer erfolgreichen Fehlerpruefung
// auf —, aber die Funktion ist eigenstaendig aufrufbar und soll ihre eigene
// Nachbedingung nicht stillschweigend vom Aufrufer voraussetzen: ein
// kuenftiger Refactor, der die Reihenfolge in run() aendert oder eine zweite
// Aufrufstelle hinzufuegt, darf nicht mit einem Nil-Pointer-Absturz bezahlen.
func TestReportWarningsIsNilSafe(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	reportWarnings(&stderr, nil)

	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, erwartet leer bei cfg == nil", stderr.String())
	}
}
