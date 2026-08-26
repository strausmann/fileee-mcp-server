// White-box tests for read_reference.go's four descriptors and their
// registration — the same pattern read_generic_test.go and
// read_sync_test.go already establish for their own descriptors.
package tools

import (
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

func TestRegisterReferenceToolsMeldetAlleAchtAn(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	registerReferenceTools(s, (*clientpool.Pool)(nil), discardLogger(), nil)

	names := toolNamesOf(t, s)
	want := []string{
		ToolListTags, ToolGetTag,
		ToolListCompanies, ToolGetCompany,
		ToolListDocumentTypes, ToolGetDocumentType,
		ToolListDocumentTypeSchemes, ToolGetDocumentTypeScheme,
	}
	for _, name := range want {
		if !names[name] {
			t.Errorf("Werkzeug %q wurde nicht angemeldet", name)
		}
	}
}

// TestCompanySummaryEnthaeltKeinenFirmennamen ist read_sync_test.go's
// TestSyncCompanySummaryEnthaeltKeinenFirmennamen-Gegenstueck fuer die
// list/get-Seite: referenceCompanyDescriptor setzt UntrustedLine und
// PoisonProbe, weil eine automatisch aus einem Dokument extrahierte
// Company (FromUserDB == false) ihren Namen vom Absender dieses
// Dokuments erbt, nicht vom Kontoinhaber (read_reference.go's eigener
// Kommentar). Dieser Test beweist, dass companySummary den Marker aus
// PoisonProbe an keiner Stelle reproduziert.
func TestCompanySummaryEnthaeltKeinenFirmennamen(t *testing.T) {
	d := referenceCompanyDescriptor()
	marker := "poison-marker-fuer-diesen-test"
	entry := d.PoisonProbe(marker)

	if !strings.Contains(d.UntrustedLine(entry), marker) {
		t.Fatalf("UntrustedLine liest nicht das von PoisonProbe gesetzte Feld")
	}
	summary := d.Summarize(entry)
	v := reflect.ValueOf(summary)
	for i := 0; i < v.NumField(); i++ {
		if s, ok := v.Field(i).Interface().(string); ok && strings.Contains(s, marker) {
			t.Fatalf("Feld %q enthaelt fremdbestimmten Text", v.Type().Field(i).Name)
		}
	}
}

// TestReferenceTagDocumentTypeSchemeBleibenOhneFremdtextfelder belegt die
// Kehrseite: Tag, DocumentType und DocumentTypeScheme tragen laut Recherche
// (docs/research/2026-08-12-fileee-go-library-feldnamen.md) keinen
// fremdbestimmten Text und lassen UntrustedLine/PoisonProbe deshalb nil —
// registerReadService darf das NICHT als Fehler werten (ein gesetztes
// PoisonProbe ohne UntrustedLine haette gepanickt, siehe
// mustNotLeakUntrustedText in read_generic.go).
func TestReferenceTagDocumentTypeSchemeBleibenOhneFremdtextfelder(t *testing.T) {
	for _, name := range []string{"tag", "documentType", "documentTypeScheme"} {
		t.Run(name, func(t *testing.T) {
			s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
			switch name {
			case "tag":
				registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), referenceTagDescriptor(), nil)
			case "documentType":
				registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), referenceDocumentTypeDescriptor(), nil)
			case "documentTypeScheme":
				registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), referenceDocumentTypeSchemeDescriptor(), nil)
			}
			// Kein Panic bis hierher ist der eigentliche Test — siehe
			// mustNotLeakUntrustedText's eigener Kommentar dazu, dass ein
			// gesetztes PoisonProbe ohne UntrustedLine ebenfalls panickt.
		})
	}
}

// TestDocumentTypeSchemeSummarizeFlachtFeldSchluesselAb belegt, dass
// documentTypeSchemeSummary die Feldschluessel aus
// SchemaDefinition.ComposingTypes zieht (ueber DocumentTypeScheme.Fields(),
// go-fileee/fileee/documenttypeschemes.go) statt nur die Baumwurzel zu
// spiegeln — der Fallstrick, den die Feldnamen-Recherche fuer diesen Typ
// ausdruecklich nennt.
func TestDocumentTypeSchemeSummarizeFlachtFeldSchluesselAb(t *testing.T) {
	d := referenceDocumentTypeSchemeDescriptor()
	scheme := &fileee.DocumentTypeScheme{
		ID: "scheme-1",
		SchemaDefinition: fileee.SchemaField{
			Key: "root",
			ComposingTypes: []fileee.SchemaField{
				{Key: "amount"},
				{Key: "invoiceDate"},
			},
		},
	}

	summary := d.Summarize(scheme)

	if summary.ID != "scheme-1" {
		t.Errorf("ID = %q, want %q", summary.ID, "scheme-1")
	}
	want := []string{"amount", "invoiceDate"}
	if !reflect.DeepEqual(summary.FieldKeys, want) {
		t.Errorf("FieldKeys = %v, want %v (die Baumwurzel selbst, %q, darf nicht auftauchen)",
			summary.FieldKeys, want, scheme.SchemaDefinition.Key)
	}
}

// TestDocumentTypeSummarizeVerwendetI18NName belegt den in der
// Feldnamen-Recherche genannten Fallstrick: das Feld heisst I18NName
// (grosses N nach der 18), nicht I18nName — ein Tippfehler beim Feldzugriff
// waere ein Kompilierfehler, dieser Test macht die Zuordnung trotzdem
// explizit sichtbar.
func TestDocumentTypeSummarizeVerwendetI18NName(t *testing.T) {
	d := referenceDocumentTypeDescriptor()
	dt := &fileee.DocumentType{
		ID:                 "dt-1",
		I18NName:           "Rechnung",
		DocumentTypeScheme: "scheme-1",
		DocumentCounter:    3,
	}

	summary := d.Summarize(dt)

	if summary.Name != "Rechnung" {
		t.Errorf("Name = %q, want %q", summary.Name, "Rechnung")
	}
	if summary.DocumentTypeScheme != "scheme-1" {
		t.Errorf("DocumentTypeScheme = %q, want %q", summary.DocumentTypeScheme, "scheme-1")
	}
	if summary.DocumentCounter != 3 {
		t.Errorf("DocumentCounter = %d, want %d", summary.DocumentCounter, 3)
	}
}

// TestReferenceDeskriptorenLiefernDieFileeeEigeneIDUeberIDOf belegt
// Aufgabe 4's Pflichtfeld direkt: jedes der vier Deskriptoren dieser Datei
// liefert über IDOf exakt die ID, die auch Summarize im ID-Feld
// zurückgibt — kein Deskriptor lässt hier ein anderes Feld einfließen.
func TestReferenceDeskriptorenLiefernDieFileeeEigeneIDUeberIDOf(t *testing.T) {
	if got := referenceTagDescriptor().IDOf(&fileee.Tag{ID: "tag-1"}); got != "tag-1" {
		t.Errorf("referenceTagDescriptor().IDOf = %q, want %q", got, "tag-1")
	}
	if got := referenceCompanyDescriptor().IDOf(&fileee.Company{ID: "company-1"}); got != "company-1" {
		t.Errorf("referenceCompanyDescriptor().IDOf = %q, want %q", got, "company-1")
	}
	if got := referenceDocumentTypeDescriptor().IDOf(&fileee.DocumentType{ID: "doctype-1"}); got != "doctype-1" {
		t.Errorf("referenceDocumentTypeDescriptor().IDOf = %q, want %q", got, "doctype-1")
	}
	if got := referenceDocumentTypeSchemeDescriptor().IDOf(&fileee.DocumentTypeScheme{ID: "scheme-1"}); got != "scheme-1" {
		t.Errorf("referenceDocumentTypeSchemeDescriptor().IDOf = %q, want %q", got, "scheme-1")
	}
}
