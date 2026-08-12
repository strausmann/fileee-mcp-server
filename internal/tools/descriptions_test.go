package tools

import (
	"strings"
	"testing"

	"github.com/strausmann/gangway/access"
)

// minDescriptionLength ist die Untergrenze, ab der eine Beschreibung die vier
// geforderten Angaben (was, Rueckgabe, wann, was nicht) ueberhaupt tragen kann.
const minDescriptionLength = 120

func TestJedesWerkzeugHatEineVollstaendigeBeschreibung(t *testing.T) {
	for _, tool := range registeredReadTools() {
		t.Run(tool.Name, func(t *testing.T) {
			if strings.TrimSpace(tool.Description) == "" {
				t.Fatalf("Werkzeug %q hat keine Beschreibung", tool.Name)
			}
			if len(tool.Description) < minDescriptionLength {
				t.Errorf("Beschreibung von %q ist mit %d Zeichen zu knapp fuer die vier geforderten Angaben (mindestens %d)",
					tool.Name, len(tool.Description), minDescriptionLength)
			}
		})
	}
}

func TestJedesWerkzeugIstAlsLesendEingestuft(t *testing.T) {
	kinds := ReadToolKinds()
	for _, tool := range registeredReadTools() {
		kind, ok := kinds[tool.Name]
		if !ok {
			t.Errorf("Werkzeug %q fehlt in ReadToolKinds — Gangway stuft es dann als KindWrite ein und lehnt jeden Aufruf ab", tool.Name)
			continue
		}
		if kind != access.KindRead {
			t.Errorf("Werkzeug %q ist als %v eingestuft, erwartet KindRead", tool.Name, kind)
		}
	}
}

func TestReadToolKindsEnthaeltKeineUnbekanntenNamen(t *testing.T) {
	registered := make(map[string]bool)
	for _, tool := range registeredReadTools() {
		registered[tool.Name] = true
	}
	for name := range ReadToolKinds() {
		if !registered[name] {
			t.Errorf("ReadToolKinds nennt %q, aber kein Werkzeug dieses Namens wird angemeldet", name)
		}
	}
}
