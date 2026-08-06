package main

import "testing"

// Bewusst NICHT parallel: die beiden folgenden Tests schreiben die paketweite
// Variable version. Aktuell entsteht daraus kein Race, weil ein parallel
// markierter Test erst weiterlaeuft, wenn die seriellen Tests durch sind — aber
// sobald jemand einem der schreibenden Tests ein t.Parallel() hinzufuegt, waere
// es eines. Der Nebenlaeufigkeitsgewinn bei drei trivialen Tests ist null.
func TestVersionDefault(t *testing.T) {
	if got := Version(); got != "dev" {
		t.Fatalf("Version() = %q, erwartet %q — ohne ldflags-Override muss der Platzhalter greifen", got, "dev")
	}
}

func TestVersionUsesLdflagsOverride(t *testing.T) {
	// Nicht parallel: die Testfunktion schreibt die paketweite Variable, die
	// TestVersionDefault liest. t.Cleanup stellt den Ausgangswert wieder her.
	original := version
	t.Cleanup(func() { version = original })

	version = "1.2.3"
	if got := Version(); got != "1.2.3" {
		t.Fatalf("Version() = %q, erwartet %q — der ldflags-Override muss durchschlagen", got, "1.2.3")
	}
}

func TestVersionNeverEmpty(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	version = ""
	if got := Version(); got != "unknown" {
		t.Fatalf("Version() = %q, erwartet %q — ein leerer ldflags-Wert darf nicht als leerer String durchgereicht werden", got, "unknown")
	}
}
