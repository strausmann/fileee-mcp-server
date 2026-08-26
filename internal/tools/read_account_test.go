// White-box tests for read_account.go's get_account_status tool —
// *fileee.Client.AccountStatus has no ReadService[T] shape (no entity
// ID, exactly one value per account), so this is a bespoke handler like
// every other tool in this package's read_boxes.go/read_binary.go.
package tools

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

func TestGetAccountStatusOutputFeldlisteIstAbgeschlossen(t *testing.T) {
	want := []string{
		"AccountTypeID", "SubscriptionName", "SubscriptionFreq", "SubscriptionAmount",
		"PayedUntil", "NextLicenseRefill", "Problem",
	}
	got := fieldNames(getAccountStatusOutput{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("getAccountStatusOutput-Feldliste = %v, want %v", got, want)
	}
}

func TestRegisterAccountToolsMeldetDasWerkzeugAn(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	registerAccountTools(s, (*clientpool.Pool)(nil), discardLogger())

	names := toolNamesOf(t, s)
	if !names[ToolGetAccountStatus] {
		t.Errorf("Werkzeug %q wurde nicht angemeldet", ToolGetAccountStatus)
	}
}

// TestGetAccountStatusInputNimmtKeineParameterEntgegen belegt den
// Auftrag woertlich: das Werkzeug nimmt keine Parameter entgegen — ein
// leerer Struct hat keine Felder, die eine Eingabe uebergeben koennte.
func TestGetAccountStatusInputNimmtKeineParameterEntgegen(t *testing.T) {
	got := fieldNames(getAccountStatusInput{})
	if len(got) != 0 {
		t.Errorf("getAccountStatusInput hat Felder %v, want keine — get_account_status nimmt keine Parameter entgegen", got)
	}
}

// fakeAccountStatusService is accountStatusService's test double.
type fakeAccountStatusService struct {
	result *fileee.AccountStatus
	err    error
}

func (f *fakeAccountStatusService) AccountStatus(context.Context) (*fileee.AccountStatus, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func TestAccountStatusFromServiceWickeltEinenGegenseitenFehlerMitDemWerkzeugnamenEin(t *testing.T) {
	backendErr := errors.New("Gegenseite antwortet nicht")
	service := &fakeAccountStatusService{err: backendErr}

	_, _, err := accountStatusFromService(context.Background(), service)
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, backendErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", backendErr, err)
	}
	if !strings.Contains(err.Error(), ToolGetAccountStatus) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolGetAccountStatus)
	}
}

func TestAccountStatusFromServiceFormatiertZeitwerteUndBehandeltNilZeiger(t *testing.T) {
	payedUntil := time.Date(2027, time.March, 1, 0, 0, 0, 0, time.UTC)
	service := &fakeAccountStatusService{result: &fileee.AccountStatus{
		AccountTypeID:      "premium",
		SubscriptionName:   "Premium Jahresabo",
		SubscriptionFreq:   "YEARLY",
		SubscriptionAmount: 49.99,
		PayedUntil:         &payedUntil,
		NextLicenseRefill:  nil, // belegt den Nil-Zweig -- darf nicht paniken
		Problem:            "",
	}}

	_, out, err := accountStatusFromService(context.Background(), service)
	if err != nil {
		t.Fatalf("accountStatusFromService: %v", err)
	}
	if out.PayedUntil != "2027-03-01T00:00:00Z" {
		t.Errorf("PayedUntil = %q, want RFC3339-Zeitstempel", out.PayedUntil)
	}
	if out.NextLicenseRefill != "" {
		t.Errorf("NextLicenseRefill = %q, want leer bei nil-Zeiger", out.NextLicenseRefill)
	}
	if out.AccountTypeID != "premium" {
		t.Errorf("AccountTypeID = %q, want %q", out.AccountTypeID, "premium")
	}
}
