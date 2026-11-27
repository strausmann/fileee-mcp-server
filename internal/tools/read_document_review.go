package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
	"github.com/strausmann/fileee-mcp-server/internal/issued"
	"github.com/strausmann/go-fileee/fileee"
)

type reviewDocumentQueryService interface {
	Query(context.Context, fileee.QueryOptions) (*fileee.QueryResult[fileee.Document], error)
}

type listReviewDocumentsInput struct {
	State string `json:"state,omitempty" jsonschema:"dashboard (default), archived, or all active documents. Archive state is the document's read flag, not its processing status or reviewed flag."`
	TagID string `json:"tagId,omitempty" jsonschema:"Optional existing tag ID to inspect reference documents carrying that tag."`
	Start int    `json:"start,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type listReviewDocumentsOutput struct {
	untrustedTextField
	Documents []reviewDocumentSummary `json:"documents"`
	TotalRows int                     `json:"totalRows"`
	State     string                  `json:"state"`
	Start     int                     `json:"start"`
	Limit     int                     `json:"limit"`
	HasMore   bool                    `json:"hasMore"`
	NextStart *int                    `json:"nextStart,omitempty"`
}

func normalizeReviewList(in listReviewDocumentsInput) (listReviewDocumentsInput, error) {
	if in.State == "" {
		in.State = "dashboard"
	}
	if in.State != "dashboard" && in.State != "archived" && in.State != "all" {
		return in, fmt.Errorf("state must be dashboard, archived, or all")
	}
	if in.Start < 0 || in.Limit < 0 || in.Limit > maxLimit {
		return in, fmt.Errorf("start must be non-negative and limit must be between 1 and 100")
	}
	if in.TagID != "" {
		if err := validateReviewID(in.TagID); err != nil {
			return in, fmt.Errorf("tagId must be a route-safe Fileee tag ID")
		}
	}
	if in.Limit == 0 {
		in.Limit = defaultLimit
	}
	return in, nil
}

func reviewDocumentsFromService(ctx context.Context, service reviewDocumentQueryService, in listReviewDocumentsInput) (*mcp.CallToolResult, listReviewDocumentsOutput, error) {
	in, err := normalizeReviewList(in)
	if err != nil {
		return nil, listReviewDocumentsOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolListReviewDocuments, err)
	}
	criteria := make([]fileee.Criterion, 0, 5)
	for _, status := range []string{"DELETED", "UPLOADING", "ERROR"} {
		criteria = append(criteria, fileee.Criterion{Field: "DocumentField:DOCUMENT_INFORMATION__STATUS", Operator: fileee.OpNEQ, Value: "PublicDocumentStatus:" + status, FieldType: "Enum", ValueType: "Enum"})
	}
	if in.State != "all" {
		criteria = append(criteria, fileee.Criterion{Field: "DocumentField:DOCUMENT_INFORMATION__ISREAD", Operator: fileee.OpEQ, Value: in.State == "archived", FieldType: "Enum", ValueType: "Boolean"})
	}
	if in.TagID != "" {
		criteria = append(criteria, fileee.Criterion{Field: "DocumentField:TAG_IDS", Operator: fileee.OpHasAll, Value: []string{in.TagID}, FieldType: "Enum", ValueType: "List", CollectionValueType: "String"})
	}
	res, err := service.Query(ctx, fileee.QueryOptions{Start: in.Start, Limit: in.Limit, Criteria: criteria,
		SortOrder: []fileee.SortField{{Field: "DocumentField:DOCUMENT_INFORMATION__CREATED", FieldType: "Enum", Desc: true, NullsFirst: true}, {Field: "EntityObjectField:ID", FieldType: "Enum"}},
	})
	if err != nil {
		return nil, listReviewDocumentsOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolListReviewDocuments, err)
	}
	if res == nil || res.TotalRows < 0 || len(res.Rows) > in.Limit {
		return nil, listReviewDocumentsOutput{}, fmt.Errorf("fileee-mcp: tools: %s: Fileee returned an invalid query result", ToolListReviewDocuments)
	}
	out := listReviewDocumentsOutput{Documents: make([]reviewDocumentSummary, 0, len(res.Rows)), TotalRows: res.TotalRows, State: in.State, Start: in.Start, Limit: in.Limit}
	lines := make([]string, 0, len(res.Rows))
	for i := range res.Rows {
		doc := &res.Rows[i]
		if (in.State == "dashboard" && !reviewDocumentInDashboard(doc)) || (in.State == "archived" && (doc.Attributes.Read == nil || !*doc.Attributes.Read)) {
			return nil, listReviewDocumentsOutput{}, fmt.Errorf("fileee-mcp: tools: %s: Fileee returned a document outside the requested archive state", ToolListReviewDocuments)
		}
		out.Documents = append(out.Documents, reviewSummary(doc))
		lines = append(lines, documentLine(doc.ID, doc.Attributes.Title))
	}
	next := in.Start + len(res.Rows)
	out.HasMore = len(res.Rows) > 0 && next < res.TotalRows
	if out.HasMore {
		out.NextStart = &next
	}
	result, err := wrapUntrustedLines(lines)
	if err != nil {
		return nil, listReviewDocumentsOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolListReviewDocuments, err)
	}
	attachUntrustedStructured(result, &out, &out.UntrustedText)
	return result, out, nil
}

func listReviewDocumentsHandler(p *clientpool.Pool, logger *slog.Logger) mcp.ToolHandlerFor[listReviewDocumentsInput, listReviewDocumentsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in listReviewDocumentsInput) (*mcp.CallToolResult, listReviewDocumentsOutput, error) {
		start := time.Now()
		logToolStart(ctx, logger, ToolListReviewDocuments)
		normalized, err := normalizeReviewList(in)
		if err != nil {
			logToolEnd(ctx, logger, ToolListReviewDocuments, start, "", 0, err)
			return nil, listReviewDocumentsOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolListReviewDocuments, err)
		}
		client, err := clientFor(ctx, p)
		if err != nil {
			logToolEnd(ctx, logger, ToolListReviewDocuments, start, "", 0, err)
			return nil, listReviewDocumentsOutput{}, err
		}
		result, out, err := reviewDocumentsFromService(ctx, client.Documents, normalized)
		logToolEnd(ctx, logger, ToolListReviewDocuments, start, listDocumentsEndpoint, len(out.Documents), err)
		return result, out, err
	}
}

type reviewPage struct {
	ID             string `json:"id"`
	ImageVersion   int64  `json:"imageVersion"`
	ContentVersion int64  `json:"contentVersion"`
}

type getDocumentReviewOutput struct {
	untrustedTextField
	reviewDocumentSummary
	Pages       []reviewPage `json:"pages"`
	CanEdit     bool         `json:"canEdit"`
	CanEditTags bool         `json:"canEditTags"`
}

func reviewAttributeValue(raw json.RawMessage) any {
	wrapper := decodeReviewWrapper(raw)
	if reviewWrapperString(wrapper, "type") == "COMPOSED" {
		values := make(map[string]any)
		for key, child := range decodeReviewWrapper(wrapper["data"]) {
			values[key] = reviewAttributeValue(child)
		}
		return values
	}
	var value any
	if attribute, ok := wrapper["value"]; ok {
		_ = json.Unmarshal(attribute, &value)
	} else {
		_ = json.Unmarshal(raw, &value)
	}
	return value
}

func documentReviewFromService(ctx context.Context, service reviewGetter[fileee.Document], id string, rec *issued.Store) (*mcp.CallToolResult, getDocumentReviewOutput, error) {
	doc, err := service.Get(ctx, id)
	if err != nil {
		return nil, getDocumentReviewOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolGetDocumentReview, err)
	}
	if doc == nil || doc.ID != id {
		return nil, getDocumentReviewOutput{}, fmt.Errorf("fileee-mcp: tools: %s: Fileee returned no matching document", ToolGetDocumentReview)
	}
	out := getDocumentReviewOutput{reviewDocumentSummary: reviewSummary(doc), Pages: make([]reviewPage, 0, len(doc.Pages)), CanEdit: reviewCanEdit(doc) == nil}
	out.CanEditTags = out.CanEdit
	for _, action := range doc.ForbiddenActions {
		if action == fileee.ActionEditTags {
			out.CanEditTags = false
		}
	}
	for _, page := range doc.Pages {
		out.Pages = append(out.Pages, reviewPage{ID: page.ID, ImageVersion: int64(page.ImageVersion), ContentVersion: int64(page.ContentVersion)})
	}
	fields, err := doc.Attributes.AttributeFields()
	if err != nil {
		return nil, getDocumentReviewOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolGetDocumentReview, err)
	}
	values := make(map[string]any)
	for _, key := range strings.Fields("title issueDate invoiceDate invoiceDueDate invoiceId customerId paymentReference contentLanguage payed amount grossIncome netIncome bankAccount1") {
		if raw, ok := fields[key]; ok {
			values[key] = reviewAttributeValue(raw)
		}
	}
	// Nested bank and money wrappers may contain unrelated backend fields.
	// Only the fields used by the document review are part of this output.
	for key, children := range map[string][]string{
		"bankAccount1": {"iban", "bic", "bank", "account_holder"},
		"amount":       {"currency", "value"}, "grossIncome": {"currency", "value"}, "netIncome": {"currency", "value"},
	} {
		if values[key] == nil {
			continue
		}
		projected := make(map[string]any)
		if group, ok := values[key].(map[string]any); ok {
			for _, child := range children {
				if value, ok := group[child]; ok {
					projected[child] = value
				}
			}
		}
		values[key] = projected
	}
	raw, err := json.Marshal(map[string]any{"fieldValues": values})
	if err != nil {
		return nil, getDocumentReviewOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolGetDocumentReview, err)
	}
	result, err := wrapUntrustedLines([]string{string(raw)})
	if err != nil {
		return nil, getDocumentReviewOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolGetDocumentReview, err)
	}
	attachUntrustedStructured(result, &out, &out.UntrustedText)
	rec.Record(ctx, id)
	return result, out, nil
}

func getDocumentReviewHandler(p *clientpool.Pool, logger *slog.Logger, rec *issued.Store) mcp.ToolHandlerFor[getDocumentInput, getDocumentReviewOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in getDocumentInput) (*mcp.CallToolResult, getDocumentReviewOutput, error) {
		start := time.Now()
		logToolStart(ctx, logger, ToolGetDocumentReview)
		if err := validateReviewID(in.ID); err != nil {
			logToolEnd(ctx, logger, ToolGetDocumentReview, start, "", 0, err)
			return nil, getDocumentReviewOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolGetDocumentReview, err)
		}
		client, err := clientFor(ctx, p)
		if err != nil {
			logToolEnd(ctx, logger, ToolGetDocumentReview, start, "", 0, err)
			return nil, getDocumentReviewOutput{}, err
		}
		result, out, err := documentReviewFromService(ctx, client.Documents, in.ID, rec)
		count := 0
		if err == nil {
			count = 1
		}
		logToolEnd(ctx, logger, ToolGetDocumentReview, start, getDocumentEndpoint, count, err)
		return result, out, err
	}
}

func registerReviewReadTools(s *mcp.Server, p *clientpool.Pool, logger *slog.Logger, rec *issued.Store) {
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolListReviewDocuments,
		Description: "List Fileee documents by actual Dashboard/archive state, ordered by uploaded/created date descending and ID ascending. " +
			"state defaults to dashboard; archived uses read=true; all omits the read filter. Excludes DELETED, UPLOADING and ERROR as the Dashboard does. " +
			"Returns document IDs, actual document-type/contact/tag IDs, precise timestamps, state, totalRows and nextStart for bounded pagination. Titles are framed as untrusted data. " +
			"Use it to select review IDs; after archiving a batch, restart dashboard at start=0 because offset positions move. Processing status and reviewed are separate from archive state.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"state": map[string]any{"type": "string", "enum": []string{"dashboard", "archived", "all"}, "default": "dashboard"},
			"tagId": map[string]any{"type": "string", "minLength": 1, "description": "Optional existing tag ID; combines HAS_ALL tag filtering with the selected archive state."},
			"start": map[string]any{"type": "integer", "minimum": 0, "default": 0},
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 20},
		}, "additionalProperties": false},
		Annotations: &mcp.ToolAnnotations{Title: "List documents for review", ReadOnlyHint: true},
	}, listReviewDocumentsHandler(p, logger))
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolGetDocumentReview,
		Description: "Read one document's metadata for review. Returns the document type, sender and recipient contact IDs, tag IDs, archive state, and a precise change timestamp. " +
			"Selected extracted fields (including dates, references and amounts) are returned as untrusted data. Page IDs and image/content versions can be used to examine the source.",
		Annotations: &mcp.ToolAnnotations{Title: "Get full document review metadata", ReadOnlyHint: true},
	}, getDocumentReviewHandler(p, logger, rec))
}
