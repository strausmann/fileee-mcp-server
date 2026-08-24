// White-box tests for read_boxes.go's two box tools (list_boxes,
// get_box) — BoxService is not a fileee.ReadService[T] (List takes no
// QueryOptions, no pagination — see boxReadService's own doc comment),
// so these are bespoke handlers like Aufgabe 5-7's document tools, tested
// the same way (fakeBoxService, no real HTTP mock).
package tools

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

func TestBoxSummaryFeldlisteIstAbgeschlossen(t *testing.T) {
	want := []string{"ID", "BoxNr", "BoxName", "QRCode", "ProductCode", "DocumentCount", "Created", "Modified"}
	got := fieldNames(boxSummary{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("boxSummary-Feldliste = %v, want %v", got, want)
	}
}

func TestBoxDetailFeldlisteIstAbgeschlossen(t *testing.T) {
	want := []string{"ID", "BoxNr", "BoxName", "QRCode", "ProductCode", "DocumentIDs", "Created", "Modified"}
	got := fieldNames(boxDetail{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("boxDetail-Feldliste = %v, want %v", got, want)
	}
}

func TestRegisterBoxToolsMeldetBeideAn(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	registerBoxTools(s, (*clientpool.Pool)(nil), discardLogger(), nil)

	names := toolNamesOf(t, s)
	for _, name := range []string{ToolListBoxes, ToolGetBox} {
		if !names[name] {
			t.Errorf("Werkzeug %q wurde nicht angemeldet", name)
		}
	}
}

// fakeBoxService is boxReadService's test double.
type fakeBoxService struct {
	listResult []fileee.Box
	listErr    error

	getResult *fileee.Box
	getErr    error
}

func (f *fakeBoxService) List(context.Context) ([]fileee.Box, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listResult, nil
}

func (f *fakeBoxService) Get(context.Context, string) (*fileee.Box, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getResult != nil {
		return f.getResult, nil
	}
	var zero fileee.Box
	return &zero, nil
}

func TestBoxesFromServiceWickeltEinenGegenseitenFehlerMitDemWerkzeugnamenEin(t *testing.T) {
	backendErr := errors.New("Gegenseite antwortet nicht")
	service := &fakeBoxService{listErr: backendErr}

	_, _, err := boxesFromService(context.Background(), service, nil)
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, backendErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", backendErr, err)
	}
	if !strings.Contains(err.Error(), ToolListBoxes) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolListBoxes)
	}
}

func TestBoxesFromServiceLiefertAlleBoxenOhneSeitenaufteilung(t *testing.T) {
	service := &fakeBoxService{listResult: []fileee.Box{
		{ID: "b1", BoxNr: 1, BoxName: "Steuerunterlagen", Documents: []fileee.BoxDocument{{DocumentID: "d1"}, {DocumentID: "d2"}}},
		{ID: "b2", BoxNr: 2, BoxName: "Versicherungen"},
	}}

	_, out, err := boxesFromService(context.Background(), service, nil)
	if err != nil {
		t.Fatalf("boxesFromService: %v", err)
	}
	if len(out.Boxes) != 2 {
		t.Fatalf("Boxes hat %d Eintraege, want 2", len(out.Boxes))
	}
	if out.Boxes[0].DocumentCount != 2 {
		t.Errorf("Boxes[0].DocumentCount = %d, want 2", out.Boxes[0].DocumentCount)
	}
	if out.Boxes[0].BoxName != "Steuerunterlagen" {
		t.Errorf("Boxes[0].BoxName = %q, want %q — der Name ist die eigene Beschriftung des Kontoinhabers, kein Fremdtext", out.Boxes[0].BoxName, "Steuerunterlagen")
	}
}

func TestGetBoxHandlerLehntEineLeereKennungOhneNetzwerkzugriffAb(t *testing.T) {
	handler := getBoxHandler(nil, discardLogger(), nil)

	_, _, err := handler(context.Background(), nil, getBoxInput{ID: "  "})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !strings.Contains(err.Error(), ToolGetBox) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolGetBox)
	}
}

func TestBoxFromServiceWickeltEinenGegenseitenFehlerMitDemWerkzeugnamenEin(t *testing.T) {
	backendErr := errors.New("Gegenseite antwortet nicht")
	service := &fakeBoxService{getErr: backendErr}

	_, _, err := boxFromService(context.Background(), service, "b1", nil)
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, backendErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", backendErr, err)
	}
	if !strings.Contains(err.Error(), ToolGetBox) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolGetBox)
	}
}

func TestBoxFromServiceListetDieEnthaltenenDokumentkennungenAuf(t *testing.T) {
	service := &fakeBoxService{getResult: &fileee.Box{
		ID: "b1", BoxNr: 1, BoxName: "Steuerunterlagen",
		Documents: []fileee.BoxDocument{{DocumentID: "d1"}, {DocumentID: "d2"}},
	}}

	_, out, err := boxFromService(context.Background(), service, "b1", nil)
	if err != nil {
		t.Fatalf("boxFromService: %v", err)
	}
	if len(out.DocumentIDs) != 2 || out.DocumentIDs[0] != "d1" || out.DocumentIDs[1] != "d2" {
		t.Errorf("DocumentIDs = %v, want [d1 d2]", out.DocumentIDs)
	}
}
