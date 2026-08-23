package tools

import (
	"strings"
	"testing"
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

// TestEveryMountedToolHasAnnotations replaces the two classification tests
// the tool-exposure foundation refactor's Task 3 removed
// (TestJedesWerkzeugIstAlsLesendEingestuft/
// TestReadToolKindsEnthaeltKeineUnbekanntenNamen, which cross-checked the
// now-deleted readToolNames/ReadToolKinds() KindRead map against this same
// live tool set). It asserts the weaker property that classification map
// leaves behind: every mounted tool still carries SOME Annotations value
// at all — mcp.AddTool never defaults one in. Task 4 strengthens this to
// checking Annotations.Title specifically, once every tool sets one.
func TestEveryMountedToolHasAnnotations(t *testing.T) {
	for _, tool := range registeredReadTools() {
		if tool.Annotations == nil {
			t.Errorf("tool %q has no annotations", tool.Name)
		}
	}
}

// TestStammdatenWerkzeugeSindAngemeldet ist Aufgabe 3's eigener Test: alle
// acht Stammdaten-Werkzeuge (vier Dienste, je Liste+Detail) muessen unter
// RegisterRead auftauchen. Der Beschreibungstest oben deckt sie
// automatisch mit ab, sobald sie hier registriert sind — dieser Test
// prueft zusaetzlich explizit, dass keiner der acht Namen fehlt.
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
