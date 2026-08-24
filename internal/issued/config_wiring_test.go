package issued

import (
	"errors"
	"testing"
	"time"

	"github.com/strausmann/fileee-mcp-server/internal/config"
)

// --- Aufgabe 3, Schritt 5: die zwei Konfigurationswerte governieren wirklich

// minimalTokenEnv ist die kleinstmoegliche gueltige config.LoadConfig-Umgebung
// (Token-Modus, ein Konto, kein Identity Provider) — dieselben Pflichtfelder
// wie internal/config/config_test.go's minimalToken(), hier noch einmal
// nachgebaut statt importiert: minimalToken selbst ist unexportiert und lebt
// in der _test.go-Datei des config-Pakets, also fuer dieses Paket nicht
// erreichbar.
func minimalTokenEnv() map[string]string {
	return map[string]string{
		"MCP_API_TOKEN":   "t0ken",
		"FILEEE_USERNAME": "nutzer@example.com",
		"FILEEE_PASSWORD": "geheim",
	}
}

func envOf(m map[string]string) config.Env {
	return func(key string) string { return m[key] }
}

// TestKonfigurierterDeckelGoverniertDenAusDerConfigGebautenStore ist der in
// Aufgabe 3, Schritt 5 verlangte Beleg: dass FILEEE_ISSUED_ID_MAX_PER_IDENTITY
// nicht nur geladen (config.LoadConfig), sondern auch tatsaechlich durchgesetzt
// wird, wenn genau der Wert, den config.LoadConfig geladen hat, unveraendert in
// New() einfliesst — die exakte Formel, die internal/server.New verwendet
// (issued.New(time.Duration(cfg.IssuedIDTTLSeconds)*time.Second,
// int(cfg.IssuedIDMaxPerIdentity))). Das ist der Unterschied zu
// TestDerDeckelVerdraengtDieAeltestenEintraege in issued_test.go: jener Test
// belegt, dass Store.recordLocked bei einem HARTCODIERTEN Deckel (3) korrekt
// verdraengt — ein Test, der bereits bestand, bevor diese Aufgabe je einen
// Konfigurationswert las. Er haette PR #68s Fehlerbild (FILEEE_MAX_UPLOAD_BYTES
// wurde geladen, dokumentiert, aber nie an eine Aufrufstelle durchgereicht)
// NICHT gefangen — genau deshalb schreibt Schritt 5 diesen zusaetzlichen Test
// vor: er baut den Store bewusst ueber config.LoadConfig, nicht ueber einen
// von Hand gewaehlten Literalwert.
//
// Gegenprobe (im Task-Report protokolliert, hier nicht dauerhaft im Code): mit
// "1000" statt "int(cfg.IssuedIDMaxPerIdentity)" unten haertcodiert faerbt
// dieser Test rot — 3 Records ueberstehen einen Deckel von 1000 anstandslos,
// die vierte Check-Pruefung unten (dass "a" verdraengt wurde) schlaegt fehl.
func TestKonfigurierterDeckelGoverniertDenAusDerConfigGebautenStore(t *testing.T) {
	env := minimalTokenEnv()
	env["FILEEE_ISSUED_ID_MAX_PER_IDENTITY"] = "2"

	cfg, err := config.LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("config.LoadConfig: %v", err)
	}
	if cfg.IssuedIDMaxPerIdentity != 2 {
		t.Fatalf("Vorbedingung: cfg.IssuedIDMaxPerIdentity = %d, erwartet 2 — Test misst sonst nichts",
			cfg.IssuedIDMaxPerIdentity)
	}

	// Exakt die Formel aus internal/server.New — dieselbe Umrechnung
	// Sekunden->time.Duration, derselbe int64->int-Cast fuer maxPerIdentity.
	s := New(time.Duration(cfg.IssuedIDTTLSeconds)*time.Second, int(cfg.IssuedIDMaxPerIdentity))
	ctx := ctxMitIdentitaet(t, "alice")

	for _, id := range []string{"a", "b", "c"} {
		s.Record(ctx, id)
	}

	if err := s.Check(ctx, "a"); !errors.Is(err, ErrNotIssued) {
		t.Fatalf("erste (aelteste) ID nach drei Records bei konfiguriertem Deckel 2: %v, want ErrNotIssued — "+
			"der Deckel aus der Umgebung wurde nicht durchgesetzt", err)
	}
	for _, id := range []string{"b", "c"} {
		if err := s.Check(ctx, id); err != nil {
			t.Fatalf("ID %q nach Ueberlauf: %v, want nil", id, err)
		}
	}
}

// TestKonfigurierteTtlGoverniertDenAusDerConfigGebautenStore ist das
// Gegenstueck fuer FILEEE_ISSUED_ID_TTL_SECONDS — dieselbe Formel, diesmal
// gegen den Verfall statt gegen den Deckel geprueft: eine ID bleibt bis
// GENAU zur konfigurierten Ttl gueltig und ist eine Sekunde danach verfallen.
func TestKonfigurierteTtlGoverniertDenAusDerConfigGebautenStore(t *testing.T) {
	env := minimalTokenEnv()
	env["FILEEE_ISSUED_ID_TTL_SECONDS"] = "60"

	cfg, err := config.LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("config.LoadConfig: %v", err)
	}
	if cfg.IssuedIDTTLSeconds != 60 {
		t.Fatalf("Vorbedingung: cfg.IssuedIDTTLSeconds = %d, erwartet 60 — Test misst sonst nichts",
			cfg.IssuedIDTTLSeconds)
	}

	u := &testUhr{jetzt: time.Unix(2_000_000, 0)}
	s := New(time.Duration(cfg.IssuedIDTTLSeconds)*time.Second, int(cfg.IssuedIDMaxPerIdentity))
	s.SetClock(u.Now)
	ctx := ctxMitIdentitaet(t, "alice")

	s.Record(ctx, "doc-1")

	u.Vor(60 * time.Second) // exakt an der konfigurierten Ttl
	if err := s.Check(ctx, "doc-1"); err != nil {
		t.Fatalf("Check nach genau der konfigurierten Ttl (60s): %v, want nil (noch gueltig)", err)
	}

	u.Vor(1 * time.Second) // eine Sekunde ueber der konfigurierten Ttl
	if err := s.Check(ctx, "doc-1"); !errors.Is(err, ErrNotIssued) {
		t.Fatalf("Check eine Sekunde nach der konfigurierten Ttl: %v, want ErrNotIssued — "+
			"die Ttl aus der Umgebung wurde nicht durchgesetzt", err)
	}
}
