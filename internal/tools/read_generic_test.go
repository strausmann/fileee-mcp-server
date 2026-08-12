// White-box tests for read_generic.go's registerReadService helper and the
// two failure paths its handlers must surface correctly — a backend error
// from Query, and an empty ID rejected before Get ever touches the
// network. Both failure-path tests exercise the client-resolution-free
// halves of the handlers directly (listFromService, genericGetHandler)
// rather than driving a full gangway/HTTP round trip: clientFor's
// serve.IdentityFrom(ctx) can only ever return ok when the context came
// through gangway's own (unexported) identity wiring, which this package
// has no way to fabricate — see clientFor's own doc comment in read.go.
package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

// tagSummary is this file's stand-in for a real service's summary struct
// (Aufgabe 3/4 define one per service) — just enough fields to prove
// Summarize's typed return value actually reaches genericListOutput /
// genericGetOutput and survives a tools/list round trip's schema
// derivation.
type tagSummary struct {
	ID string `json:"id"`
}

// descriptionFixture stands in for a real tool description in tests that
// never call descriptions_test.go's length check (that check runs against
// registeredReadTools(), i.e. only tools RegisterRead itself mounts —
// nothing in this file does) — kept realistically long anyway so a test
// failure here is never mistaken for that unrelated check.
const descriptionFixture = "Beschreibungstext lang genug fuer die Pruefung, mindestens hundertzwanzig Zeichen, damit der Beschreibungstest nicht anschlaegt."

func tagDescriptor() readServiceDescriptor[fileee.Tag, tagSummary] {
	return readServiceDescriptor[fileee.Tag, tagSummary]{
		ListName:        "list_tags",
		GetName:         "get_tag",
		ListDescription: descriptionFixture,
		GetDescription:  descriptionFixture,
		Service:         func(c *fileee.Client) fileee.ReadService[fileee.Tag] { return c.Tags },
		Summarize:       func(tag *fileee.Tag) tagSummary { return tagSummary{ID: tag.ID} },
		UntrustedLine:   func(tag *fileee.Tag) string { return tag.Name },
	}
}

func TestRegisterReadServiceMeldetListeUndDetailAn(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	registerReadService(s, (*clientpool.Pool)(nil), tagDescriptor())

	names := toolNamesOf(t, s)
	if !names["list_tags"] {
		t.Error("list_tags wurde nicht angemeldet")
	}
	if !names["get_tag"] {
		t.Error("get_tag wurde nicht angemeldet")
	}
}

// toolNamesOf verbindet einen echten MCP-Client ueber eine
// In-Memory-Transportstrecke mit s und liest tools/list — derselbe Weg wie
// names.go's registeredReadTools() und internal/server/server_test.go's
// toolNamesOf, hier lokal dupliziert: go-sdk v1.7.0's *mcp.Server haelt
// seine angemeldeten Werkzeuge in einer unexportierten featureSet, es gibt
// kein "s.Tools()".
func toolNamesOf(t *testing.T, s *mcp.Server) map[string]bool {
	t.Helper()
	ctx := context.Background()

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := s.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("Server.Connect: %v", err)
	}
	defer func() { _ = serverSession.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "read-generic-probe", Version: "0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("Client.Connect: %v", err)
	}
	defer func() { _ = clientSession.Close() }()

	res, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	return names
}

// fakeReadService steht fuer fileee.ReadService[T] in Tests, die die
// Fehlerpfade von listFromService/genericGetHandler pruefen — ohne
// Mock-HTTP-Server, ohne Login-Handshake: readServiceDescriptor.Service
// entscheidet, welche Implementierung der echte Handler bekommt, und ein
// Test kann dort schlicht diese Attrappe eintragen.
type fakeReadService[T any] struct {
	queryErr error
	getErr   error
}

func (f *fakeReadService[T]) Query(context.Context, fileee.QueryOptions) (*fileee.QueryResult[T], error) {
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	return &fileee.QueryResult[T]{}, nil
}

func (f *fakeReadService[T]) Diff(context.Context, fileee.Cursor) (*fileee.DiffResult[T], error) {
	return &fileee.DiffResult[T]{}, nil
}

func (f *fakeReadService[T]) Get(context.Context, string) (*T, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	var zero T
	return &zero, nil
}

func TestListFromServiceWickeltEinenGegenseitenFehlerMitDemWerkzeugnamenEin(t *testing.T) {
	backendErr := errors.New("Gegenseite antwortet nicht")
	d := tagDescriptor()
	service := &fakeReadService[fileee.Tag]{queryErr: backendErr}

	_, _, err := listFromService(context.Background(), d, service, genericListInput{})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, backendErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", backendErr, err)
	}
	if !strings.Contains(err.Error(), d.ListName) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), d.ListName)
	}
}

func TestGenericGetHandlerLehntEineLeereKennungOhneNetzwerkzugriffAb(t *testing.T) {
	d := tagDescriptor()
	// p bleibt nil: erreicht der Handler clientFor doch noch, bricht der
	// Test mit einer Nil-Pointer-Dereferenzierung ab statt still zu
	// bestehen — das ist der Beleg, dass die leere Kennung VOR jedem
	// Netzwerkzugriff abgefangen wird.
	handler := genericGetHandler[fileee.Tag, tagSummary](nil, d)

	_, _, err := handler(context.Background(), nil, genericGetInput{ID: "   "})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !strings.Contains(err.Error(), d.GetName) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), d.GetName)
	}
}
