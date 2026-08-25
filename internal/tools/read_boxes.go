// read_boxes.go wires list_boxes/get_box — bespoke handlers, not routed
// through registerReadService (read_generic.go): fileee.BoxService (the
// interface behind *fileee.Client's Boxes field) is not a
// fileee.ReadService[T]. Its List has no QueryOptions parameter at all —
// go-fileee's own List implementation runs Diff(ctx, NewCursor("Box"))
// internally and returns every row, unpaginated (boxes.go) — so
// list_boxes takes no limit/offset either; offering one would promise a
// parameter the underlying call cannot honour.
//
// A box's name (BoxName) is the account holder's own label for a
// physical fileeeBox or a self-made fileeeDIY box — Feldnamen-Recherche,
// Abschnitt Box: no FromUserDB-style provenance field, no code path that
// derives it from a document's content the way Company's or Contact's
// name can be. Neither boxSummary nor boxDetail therefore needs
// UntrustedLine/PoisonProbe.
package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
	"github.com/strausmann/fileee-mcp-server/internal/issued"
)

// boxReadService is what list_boxes/get_box need from *fileee.Client's
// Boxes field — exactly fileee.BoxService's own List/Get, narrowed so a
// fake test double doesn't also have to implement AddDocument/
// RemoveDocument (the two write operations this server never calls).
type boxReadService interface {
	List(ctx context.Context) ([]fileee.Box, error)
	Get(ctx context.Context, id string) (*fileee.Box, error)
}

// getBoxEndpoint is list_boxes'/get_box's own wire endpoint — both reach
// the same generic diff/rest-get routes fileee.boxService wraps
// (resourcePath "fileeeboxes", boxes.go); logged as fixed, per-tool
// metadata the same way every other endpoint constant in this package is
// (see listDocumentsEndpoint's own doc comment, read.go).
const (
	listBoxesEndpoint = "POST /api/fileeeboxes/rest/diff"
	getBoxEndpoint    = "GET /api/fileeeboxes/rest/:id"
)

// boxSummary is list_boxes' structured summary — every field is either
// Fileee's own metadata or the account holder's own label, per this
// file's own doc comment.
type boxSummary struct {
	ID            string `json:"id"`
	BoxNr         int    `json:"boxNr"`
	BoxName       string `json:"boxName"`
	QRCode        string `json:"qrCode,omitempty"`
	ProductCode   string `json:"productCode,omitempty"`
	DocumentCount int    `json:"documentCount"`
	Created       string `json:"created,omitempty"`
	Modified      string `json:"modified,omitempty"`
}

// boxDetail is get_box's structured result — the same fields boxSummary
// exposes, minus the count and plus the actual document IDs filed in the
// box (fileee.BoxDocument.DocumentID; PageCount/Modified per contained
// document are not exposed — a caller wanting a document's own detail
// calls get_document with the returned ID).
type boxDetail struct {
	ID          string   `json:"id"`
	BoxNr       int      `json:"boxNr"`
	BoxName     string   `json:"boxName"`
	QRCode      string   `json:"qrCode,omitempty"`
	ProductCode string   `json:"productCode,omitempty"`
	DocumentIDs []string `json:"documentIds,omitempty"`
	Created     string   `json:"created,omitempty"`
	Modified    string   `json:"modified,omitempty"`
}

// boxSummaryOf renders one fileee.Box as boxSummary — a pure function,
// independent of client resolution, shared by list_boxes' handler and
// its own tests.
func boxSummaryOf(b *fileee.Box) boxSummary {
	return boxSummary{
		ID:            b.ID,
		BoxNr:         b.BoxNr,
		BoxName:       b.BoxName,
		QRCode:        b.QRCode,
		ProductCode:   b.ProductCode,
		DocumentCount: len(b.Documents),
		Created:       b.Created,
		Modified:      b.Modified,
	}
}

// boxDetailOf renders one fileee.Box as boxDetail.
func boxDetailOf(b *fileee.Box) boxDetail {
	ids := make([]string, 0, len(b.Documents))
	for _, d := range b.Documents {
		ids = append(ids, d.DocumentID)
	}
	return boxDetail{
		ID:          b.ID,
		BoxNr:       b.BoxNr,
		BoxName:     b.BoxName,
		QRCode:      b.QRCode,
		ProductCode: b.ProductCode,
		DocumentIDs: ids,
		Created:     b.Created,
		Modified:    b.Modified,
	}
}

// listBoxesInput are list_boxes' parameters — deliberately empty. See
// this file's own doc comment for why there is no limit/offset.
type listBoxesInput struct{}

// listBoxesOutput is list_boxes' structured result.
type listBoxesOutput struct {
	Boxes []boxSummary `json:"boxes"`
}

// listBoxesHandler resolves list_boxes.
//
// WICHTIG (ADR-0019, Betreiber-Entscheidung nach dem Sicherheits-Audit,
// verschärft gegenüber der ursprünglichen Aufgabe 5-Fassung): this
// handler no longer takes an *issued.Store — see boxesFromService's own
// doc comment for why.
func listBoxesHandler(p *clientpool.Pool, logger *slog.Logger) mcp.ToolHandlerFor[listBoxesInput, listBoxesOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ listBoxesInput) (*mcp.CallToolResult, listBoxesOutput, error) {
		start := time.Now()
		logToolStart(ctx, logger, ToolListBoxes)

		client, err := clientFor(ctx, p)
		if err != nil {
			logToolEnd(ctx, logger, ToolListBoxes, start, "", 0, err)
			return nil, listBoxesOutput{}, err
		}
		result, out, err := boxesFromService(ctx, client.Boxes)
		logToolEnd(ctx, logger, ToolListBoxes, start, listBoxesEndpoint, len(out.Boxes), err)
		return result, out, err
	}
}

// boxesFromService is listBoxesHandler's logic below client resolution —
// split out so a test can drive it against a boxReadService fake
// (fakeBoxService, read_boxes_test.go) instead of a live *fileee.Client.
//
// WICHTIG (ADR-0019, Betreiber-Entscheidung nach dem Sicherheits-Audit,
// verschärft gegenüber der ursprünglichen Aufgabe 5-Fassung): this
// function no longer records ANY of the returned boxes' ids — it used to
// record every one of them, once the full result was built. list_boxes
// returns every box in the account at once (this file's own package doc
// comment: Fileee's own List has no pagination), none of it individually
// requested by the caller — the same "list of several entities" shape as
// list_documents (see listDocumentsHandler's own doc comment, read.go).
// A caller who needs a box id to count as issued now calls get_box with
// that id.
func boxesFromService(ctx context.Context, service boxReadService) (*mcp.CallToolResult, listBoxesOutput, error) {
	boxes, err := service.List(ctx)
	if err != nil {
		return nil, listBoxesOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolListBoxes, err)
	}
	out := listBoxesOutput{Boxes: make([]boxSummary, 0, len(boxes))}
	for i := range boxes {
		out.Boxes = append(out.Boxes, boxSummaryOf(&boxes[i]))
	}
	return &mcp.CallToolResult{}, out, nil
}

// getBoxInput are get_box's parameters.
type getBoxInput struct {
	// ID identifies the box to load. Required; an empty ID is rejected
	// before any network access.
	ID string `json:"id" jsonschema:"identifier of the box to load"`
}

// getBoxHandler resolves get_box. The empty-ID check runs before
// clientFor, the same order every other detail handler in this package
// uses for its own required parameter.
func getBoxHandler(p *clientpool.Pool, logger *slog.Logger, rec *issued.Store) mcp.ToolHandlerFor[getBoxInput, boxDetail] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in getBoxInput) (*mcp.CallToolResult, boxDetail, error) {
		start := time.Now()
		logToolStart(ctx, logger, ToolGetBox, slog.String("id", in.ID))

		if strings.TrimSpace(in.ID) == "" {
			err := fmt.Errorf("fileee-mcp: tools: %s: id must not be empty", ToolGetBox)
			logToolEnd(ctx, logger, ToolGetBox, start, "", 0, err)
			return nil, boxDetail{}, err
		}

		client, err := clientFor(ctx, p)
		if err != nil {
			logToolEnd(ctx, logger, ToolGetBox, start, "", 0, err)
			return nil, boxDetail{}, err
		}
		result, out, err := boxFromService(ctx, client.Boxes, in.ID, rec)
		logToolEnd(ctx, logger, ToolGetBox, start, getBoxEndpoint, 1, err)
		return result, out, err
	}
}

// boxFromService is getBoxHandler's logic below client resolution — split
// out for the same testability reason boxesFromService is.
//
// rec records ONLY id — the box id the caller actually named in
// getBoxInput.ID — once the full result is built. service.Get cannot
// fail after this point, so recording right before the return is already
// the success-only point.
//
// WICHTIG (ADR-0019, Betreiber-Entscheidung nach dem Sicherheits-Audit,
// verschärft gegenüber der ursprünglichen Aufgabe 5-Fassung): dieser
// Aufruf nahm zuvor BEIDE hier gelieferten IDs auf — die des Boxes SELBST
// und, angehängt, detail.DocumentIDs (die IDs der aktuell darin
// abgelegten Dokumente — boxDetail's eigener Doc-Kommentar: "a caller
// wanting a document's own detail calls get_document with the returned
// ID"). Die neue Linie aus dem Audit lautet aber "erfasst wird NUR die
// ID, die der Aufrufer im Parameter genannt hat" — detail.DocumentIDs
// erfüllt das nicht: der Aufrufer hat keines dieser Dokumente einzeln
// genannt, sie kommen als NEBENPRODUKT der Box-Antwort mit, exakt
// dasselbe Muster wie get_document's TagIDs (read.go,
// documentFromService's eigener Doc-Kommentar) und
// list_document_conversations' Konversations-IDs
// (documentConversationsFromService's eigener Doc-Kommentar) — beide
// ebenfalls seit ADR-0019 NICHT mehr aufgenommen. Ein Aufrufer, der ein
// per get_box zurückgegebenes Dokument als gültiges Ziel für ein
// späteres destruktives Werkzeug braucht (z. B. box_add_document/
// box_remove_document, write_boxes.go), muss es gezielt per get_document
// nachladen.
//
// WICHTIG (Nachprüfungs-Befund, Codex-Review nach ADR-0019): rec.Record
// lief hier zuvor mit detail.ID (boxDetailOf's b.ID-Feld — die ID aus
// der ANTWORT), NICHT mit id (der vom Aufrufer im Parameter genannten).
// Der frühere Kommentar oben behauptete, "detail.ID ist genau das (die
// von get_box(id) angeforderte Box)" — das gilt nur, solange der fremde
// Fileee-Server für jede erfolgreich aufgelöste id ein identisches
// "id"-Feld im JSON-Body zurückliefert. go-fileee selbst kanonisiert
// nichts (boxReadService.Get dekodiert den Response-Body direkt in
// *fileee.Box, ohne id vorher umzuschreiben oder das dekodierte ID-Feld
// gegen id zu vergleichen) — eine Divergenz ist von hier aus also nicht
// ausschließbar. Divergierten beide, hätte diese Funktion die
// tatsächlich angeforderte id NIE gewhitelistet und stattdessen eine ID
// aufgenommen, die der Aufrufer nie genannt hat — die exakte Umkehrung
// der oben zitierten Linie. id selbst erfüllt beide Hälften unabhängig
// von detail.ID: der Aufrufer hat sie im Parameter genannt, und der
// erfolgreiche, fehlerfrei dekodierte service.Get-Aufruf IST die
// Bestätigung, dass sie bei diesem Server auf eine existierende, lesbare
// Entität auflöst. Dieselbe Korrektur, aus demselben Fund, trifft
// getFromService (read_generic.go) und documentFromService (read.go)
// gleichermaßen — siehe deren eigene Doc-Kommentare für die volle
// Begründung, warum weder "nur detail.ID" noch "beide IDs aufnehmen"
// gewählt wurde.
func boxFromService(ctx context.Context, service boxReadService, id string, rec *issued.Store) (*mcp.CallToolResult, boxDetail, error) {
	box, err := service.Get(ctx, id)
	if err != nil {
		return nil, boxDetail{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolGetBox, err)
	}
	detail := boxDetailOf(box)
	rec.Record(ctx, id)
	return &mcp.CallToolResult{}, detail, nil
}

// registerBoxTools mounts list_boxes/get_box onto s — called once from
// RegisterAll (read.go). rec is threaded through to getBoxHandler ONLY —
// see boxesFromService's/boxFromService's own doc comments for why
// listBoxesHandler does not take it (ADR-0019, Betreiber-Entscheidung
// nach dem Sicherheits-Audit).
func registerBoxTools(s *mcp.Server, p *clientpool.Pool, logger *slog.Logger, rec *issued.Store) {
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolListBoxes,
		Description: "List the boxes in the calling user's Fileee account — physical fileeeBox " +
			"or self-made fileeeDIY boxes tracked digitally. Returns every box at once; Fileee's " +
			"own API has no pagination for this endpoint, so there is no limit or offset " +
			"parameter to pass. Each box's name is the account holder's own label, not " +
			"third-party content. Use it to discover which boxes exist before loading one in " +
			"detail with get_box. It does not return the contained documents' own titles or " +
			"metadata — use list_documents/get_document for that.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, Title: "List boxes"},
	}, listBoxesHandler(p, logger))

	mcp.AddTool(s, &mcp.Tool{
		Name: ToolGetBox,
		Description: "Load a single box by its ID. Returns the same fields list_boxes exposes, " +
			"plus the IDs of the documents currently filed in it. Use it when another tool " +
			"handed you a box ID and you need its contents. It does not return the documents' " +
			"own titles or metadata — use get_document with each returned ID for that — and it " +
			"does not search by box name.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, Title: "Get box"},
	}, getBoxHandler(p, logger, rec))
}
