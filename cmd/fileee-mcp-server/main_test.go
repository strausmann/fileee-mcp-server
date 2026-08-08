package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersionSubcommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"version"}, envOf(nil), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run(version) = Exit-Code %d, erwartet 0", code)
	}
	if got := strings.TrimSpace(stdout.String()); got != Version() {
		t.Errorf("stdout = %q, erwartet %q", got, Version())
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
	// fehl (Default-Modus ist token, siehe config.go) — das ist MCP_API_TOKEN,
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
// server.go), muss beim Start scheitern — nicht erst beim ersten Request.
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
