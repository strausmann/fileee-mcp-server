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

// TestStammdatenWerkzeugeSindAngemeldet ist Aufgabe 3's eigener Test: alle
// acht Stammdaten-Werkzeuge (vier Dienste, je Liste+Detail) muessen unter
// RegisterRead auftauchen. Der Beschreibungs- und Einstufungstest oben
// deckt sie automatisch mit ab, sobald sie hier registriert sind — dieser
// Test prueft zusaetzlich explizit, dass keiner der acht Namen fehlt.
func TestStammdatenWerkzeugeSindAngemeldet(t *testing.T) {
	want := []string{
		"list_tags", "get_tag",
		"list_companies", "get_company",
		"list_document_types", "get_document_type",
		"list_document_type_schemes", "get_document_type_scheme",
	}
	got := make(map[string]bool)
	for _, tool := range registeredReadTools() {
		got[tool.Name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("Werkzeug %q fehlt", name)
		}
	}
}
