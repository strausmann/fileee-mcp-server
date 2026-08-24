// read_binary.go wires get_document_pdf, get_page_image and
// get_page_ocr — bespoke handlers, DownloadPDF/DownloadPageImage/
// PageOCR have no Query/Diff/Get shape at all.
//
// get_document_pdf and get_page_image return a data STREAM, not a
// string — the server reads it with a hard ceiling (maxBinaryBytes) and
// fails loudly, with the actual byte count it managed to read, the
// moment it can prove the stream is over that ceiling. It never returns
// a silently truncated prefix that would look like a complete, valid
// PDF/image to whatever reads it next — see readLimited's own doc
// comment for why "at least N bytes" is the honest claim this design can
// make, not a guaranteed exact total.
//
// get_page_ocr is, per this repo's own governing rule (ADR-0013) and the
// task that introduced it, the single strongest case of foreign content
// this server handles: OCRToken.Text is recognized text copied verbatim
// from a document a third party sent or scanned — not a title chosen
// once by a sender, but the sender's ENTIRE page content. It is framed
// exactly like every other foreign text in this server (wrapUntrustedLines)
// and never appears in StructuredContent; only token count and
// bounding-box coordinates do (ocrTokenPosition, deliberately excluding
// Text).
//
// Aufgabe 5 (internal/issued, ADR-0013 Punkt 3) decided all three tools
// in this file need NO *issued.Store — none of them hands out a
// Fileee-owned id a later tool could act on:
//
//   - getDocumentPDFOutput/getPageImageOutput carry only SizeBytes — no
//     id field of any kind.
//   - ocrTokenPosition.WebappID (json "webappId") is the one field in
//     this file that LOOKS like an id — it is Fileee's own generated id
//     for that OCR token (see ocrTokenPosition's own doc comment) — but
//     no tool in this server accepts an OCR-token id as a parameter at
//     all, so recording it could never be checked against by anything.
//     Recording it anyway would only consume a slot in the caller's
//     internal/issued per-identity cap for no protective effect — the
//     deliberate "nein" among this task's decisions, see
//     issued_coverage_test.go's own doc comment for the full set.
package tools

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

// maxBinaryBytes bounds get_document_pdf's and get_page_image's own read
// of Fileee's PDF/image stream. 8 MiB is chosen as a ceiling comfortably
// above a typical scanned multi-page PDF or a single page image (a few
// hundred KB to low single-digit MB in practice) while still bounding
// how much of a single tool call's memory and MCP-result size one
// document can consume — a limit too low would make the tool useless for
// ordinary documents; one too high would let a single call return a
// response large enough to overwhelm the caller. 8 MiB is the same order
// of magnitude Claude's own attachment/tool-result handling already
// budgets for a single binary artifact.
const maxBinaryBytes = 8 * 1024 * 1024 // 8 MiB

// readLimited reads r fully into memory, bounded at limit+1 bytes,
// closing r in every path — success, a download error, and the
// over-limit case alike — never leaving the caller to remember it
// (io.LimitReader itself never closes anything).
//
// It returns an error the moment it can prove the stream is over limit,
// rather than returning a silently truncated prefix that would look like
// a complete, valid PDF/image to whatever reads it next: a
// truncated-but-successful response is worse than an explicit failure,
// since nothing downstream would know to distrust it.
//
// The reported size is the number of bytes actually read (at most
// limit+1), not necessarily the stream's true total length — Fileee's
// PDF/image download returns only an io.ReadCloser (go-fileee's
// downloadBinary, documents.go), never a Content-Length this function
// could inspect before deciding whether to proceed, and reading further
// just to report an exact total would defeat the memory bound this
// function exists to enforce. "at least N bytes, over the M-byte limit"
// is the honest, boundedly-obtainable claim; nothing here promises the
// stream's true size.
func readLimited(r io.ReadCloser, limit int) ([]byte, error) {
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("fileee-mcp: tools: read binary content: %w", err)
	}
	if len(data) > limit {
		return nil, fmt.Errorf("fileee-mcp: tools: response too large: read at least %d bytes, exceeds the %d-byte limit", len(data), limit)
	}
	return data, nil
}

// documentBinaryService is what get_document_pdf/get_page_image/
// get_page_ocr need from *fileee.Client's Documents field — narrow
// enough that a fake test double doesn't need the rest of
// *fileee.DocumentService's much larger method set.
type documentBinaryService interface {
	DownloadPDF(ctx context.Context, id string, mode fileee.PDFMode) (io.ReadCloser, error)
	DownloadPageImage(ctx context.Context, pageID string, size fileee.ImageSize, version int64) (io.ReadCloser, error)
	PageOCR(ctx context.Context, pageID string) ([]fileee.OCRToken, error)
}

// getDocumentPDFEndpoint, getPageImageEndpoint and getPageOCREndpoint are
// the Fileee wire endpoints these three tools reach — logged as fixed,
// per-tool metadata the same way every other endpoint constant in this
// package is (see listDocumentsEndpoint's own doc comment, read.go).
const (
	getDocumentPDFEndpoint = "GET /api/v1/documents/:id/pdf"
	getPageImageEndpoint   = "GET /api/v1/pages/:id/image"
	getPageOCREndpoint     = "GET /api/pages/:id"
)

// --- get_document_pdf ---

// getDocumentPDFInput are get_document_pdf's parameters.
type getDocumentPDFInput struct {
	// ID identifies the document to download. Required; an empty ID is
	// rejected before any network access.
	ID string `json:"id" jsonschema:"identifier of the document to download as PDF"`
	// Mode selects Fileee's own rendering mode: "download" (the default)
	// or "print". An unrecognized value is rejected before any network
	// access — see fileee.PDFModeDownload/PDFModePrint, the only two
	// values Fileee itself accepts (go-fileee/fileee/types.go).
	Mode string `json:"mode,omitempty" jsonschema:"pdf rendering mode: download (default) or print"`
}

// getDocumentPDFOutput is get_document_pdf's structured result. The PDF
// bytes themselves are never here — see this file's own doc comment —
// only their size, so a caller can sanity-check what it received without
// re-reading the embedded resource content.
type getDocumentPDFOutput struct {
	SizeBytes int `json:"sizeBytes"`
}

// parsePDFMode validates a caller-supplied mode string against Fileee's
// own two accepted values, defaulting to PDFModeDownload for an empty
// string. Any other value is rejected before any network access — the
// same "reject cheap mistakes before spending a login round trip" order
// every other required/validated parameter in this package uses.
func parsePDFMode(mode string) (fileee.PDFMode, error) {
	switch mode {
	case "", string(fileee.PDFModeDownload):
		return fileee.PDFModeDownload, nil
	case string(fileee.PDFModePrint):
		return fileee.PDFModePrint, nil
	default:
		return "", fmt.Errorf("unknown mode %q, want %q or %q", mode, fileee.PDFModeDownload, fileee.PDFModePrint)
	}
}

// getDocumentPDFHandler resolves get_document_pdf. The empty-ID check
// and mode validation both run before clientFor.
func getDocumentPDFHandler(p *clientpool.Pool, logger *slog.Logger) mcp.ToolHandlerFor[getDocumentPDFInput, getDocumentPDFOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in getDocumentPDFInput) (*mcp.CallToolResult, getDocumentPDFOutput, error) {
		start := time.Now()
		logToolStart(ctx, logger, ToolGetDocumentPDF, slog.String("id", in.ID), slog.String("mode", in.Mode))

		if strings.TrimSpace(in.ID) == "" {
			err := fmt.Errorf("fileee-mcp: tools: %s: id must not be empty", ToolGetDocumentPDF)
			logToolEnd(ctx, logger, ToolGetDocumentPDF, start, "", 0, err)
			return nil, getDocumentPDFOutput{}, err
		}
		mode, err := parsePDFMode(in.Mode)
		if err != nil {
			wrapped := fmt.Errorf("fileee-mcp: tools: %s: %w", ToolGetDocumentPDF, err)
			logToolEnd(ctx, logger, ToolGetDocumentPDF, start, "", 0, wrapped)
			return nil, getDocumentPDFOutput{}, wrapped
		}

		client, err := clientFor(ctx, p)
		if err != nil {
			logToolEnd(ctx, logger, ToolGetDocumentPDF, start, "", 0, err)
			return nil, getDocumentPDFOutput{}, err
		}
		result, out, err := documentPDFFromService(ctx, client.Documents, in.ID, mode)
		logToolEnd(ctx, logger, ToolGetDocumentPDF, start, getDocumentPDFEndpoint, out.SizeBytes, err)
		return result, out, err
	}
}

// documentPDFFromService is getDocumentPDFHandler's logic below client
// resolution — split out so a test can drive it against a
// documentBinaryService fake (fakeDocumentBinaryService,
// read_binary_test.go) instead of a live *fileee.Client.
func documentPDFFromService(ctx context.Context, service documentBinaryService, id string, mode fileee.PDFMode) (*mcp.CallToolResult, getDocumentPDFOutput, error) {
	rc, err := service.DownloadPDF(ctx, id, mode)
	if err != nil {
		return nil, getDocumentPDFOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolGetDocumentPDF, err)
	}
	data, err := readLimited(rc, maxBinaryBytes)
	if err != nil {
		return nil, getDocumentPDFOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolGetDocumentPDF, err)
	}
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.EmbeddedResource{
		Resource: &mcp.ResourceContents{
			URI:      "fileee://documents/" + id + "/pdf",
			MIMEType: "application/pdf",
			Blob:     data,
		},
	}}}
	return result, getDocumentPDFOutput{SizeBytes: len(data)}, nil
}

// --- get_page_image ---

// getPageImageInput are get_page_image's parameters. Version is
// required and MUST come from the page's own current imageVersion —
// go-fileee's own doc comment on DownloadPageImage (documents.go)
// requires it to be freshly read each time, never cached. No tool in
// this server currently exposes a page's imageVersion (get_document
// returns only a page COUNT, not per-page detail) — a caller has to get
// it from wherever it already tracks page metadata outside this server.
type getPageImageInput struct {
	PageID string `json:"pageId" jsonschema:"identifier of the page to load as an image"`
	// Size selects Fileee's own image size: "smedium" or "medium" (the
	// default). An unrecognized value is rejected before any network
	// access.
	Size string `json:"size,omitempty" jsonschema:"image size: smedium or medium (default medium)"`
	// Version is the page's current imageVersion. Required — Fileee
	// serves a stale or missing image for an outdated version.
	Version int64 `json:"version" jsonschema:"the page's current imageVersion; must be freshly read, never cached"`
}

// getPageImageOutput is get_page_image's structured result — only the
// image's size; the bytes themselves travel as mcp.ImageContent (see
// documentPageImageFromService), never here.
type getPageImageOutput struct {
	SizeBytes int `json:"sizeBytes"`
}

// parseImageSize validates a caller-supplied size string against
// Fileee's own two accepted values, defaulting to ImageSizeMedium for an
// empty string — the same pattern parsePDFMode uses.
func parseImageSize(size string) (fileee.ImageSize, error) {
	switch size {
	case "", string(fileee.ImageSizeMedium):
		return fileee.ImageSizeMedium, nil
	case string(fileee.ImageSizeSmedium):
		return fileee.ImageSizeSmedium, nil
	default:
		return "", fmt.Errorf("unknown size %q, want %q or %q", size, fileee.ImageSizeSmedium, fileee.ImageSizeMedium)
	}
}

// getPageImageHandler resolves get_page_image. The empty-page-ID check
// and size validation both run before clientFor.
func getPageImageHandler(p *clientpool.Pool, logger *slog.Logger) mcp.ToolHandlerFor[getPageImageInput, getPageImageOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in getPageImageInput) (*mcp.CallToolResult, getPageImageOutput, error) {
		start := time.Now()
		logToolStart(ctx, logger, ToolGetPageImage, slog.String("pageId", in.PageID), slog.String("size", in.Size), slog.Int64("version", in.Version))

		if strings.TrimSpace(in.PageID) == "" {
			err := fmt.Errorf("fileee-mcp: tools: %s: page id must not be empty", ToolGetPageImage)
			logToolEnd(ctx, logger, ToolGetPageImage, start, "", 0, err)
			return nil, getPageImageOutput{}, err
		}
		size, err := parseImageSize(in.Size)
		if err != nil {
			wrapped := fmt.Errorf("fileee-mcp: tools: %s: %w", ToolGetPageImage, err)
			logToolEnd(ctx, logger, ToolGetPageImage, start, "", 0, wrapped)
			return nil, getPageImageOutput{}, wrapped
		}

		client, err := clientFor(ctx, p)
		if err != nil {
			logToolEnd(ctx, logger, ToolGetPageImage, start, "", 0, err)
			return nil, getPageImageOutput{}, err
		}
		result, out, err := documentPageImageFromService(ctx, client.Documents, in.PageID, size, in.Version)
		logToolEnd(ctx, logger, ToolGetPageImage, start, getPageImageEndpoint, out.SizeBytes, err)
		return result, out, err
	}
}

// documentPageImageFromService is getPageImageHandler's logic below
// client resolution — split out for the same testability reason
// documentPDFFromService is.
func documentPageImageFromService(ctx context.Context, service documentBinaryService, pageID string, size fileee.ImageSize, version int64) (*mcp.CallToolResult, getPageImageOutput, error) {
	rc, err := service.DownloadPageImage(ctx, pageID, size, version)
	if err != nil {
		return nil, getPageImageOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolGetPageImage, err)
	}
	data, err := readLimited(rc, maxBinaryBytes)
	if err != nil {
		return nil, getPageImageOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolGetPageImage, err)
	}
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{
		Data:     data,
		MIMEType: "image/jpeg",
	}}}
	return result, getPageImageOutput{SizeBytes: len(data)}, nil
}

// --- get_page_ocr ---

// getPageOCRInput are get_page_ocr's parameters.
type getPageOCRInput struct {
	// PageID identifies the page to run OCR lookup for. Required; an
	// empty ID is rejected before any network access.
	PageID string `json:"pageId" jsonschema:"identifier of the page to run OCR lookup for"`
}

// ocrTokenPosition is get_page_ocr's structured summary of ONE
// recognized token — coordinates and Fileee's own token ID only, NEVER
// Text (see this file's own doc comment on why OCR text is this
// server's single strongest foreign-content case).
type ocrTokenPosition struct {
	// WebappID is Fileee's own generated ID for this token — fileee.OCRToken
	// has no ID field at all, only WebappID (Feldnamen-Recherche, Abschnitt
	// OCRToken — the most surprising case among the thirteen Fileee types).
	WebappID string  `json:"webappId"`
	Left     float64 `json:"left"`
	Top      float64 `json:"top"`
	Right    float64 `json:"right"`
	Bottom   float64 `json:"bottom"`
	Width    float64 `json:"width"`
	Height   float64 `json:"height"`
}

// getPageOCROutput is get_page_ocr's structured result — a count and the
// per-token coordinates, never the recognized text itself.
type getPageOCROutput struct {
	TokenCount int                `json:"tokenCount"`
	Tokens     []ocrTokenPosition `json:"tokens"`
}

// getPageOCRHandler resolves get_page_ocr. The empty-page-ID check runs
// before clientFor.
func getPageOCRHandler(p *clientpool.Pool, logger *slog.Logger) mcp.ToolHandlerFor[getPageOCRInput, getPageOCROutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in getPageOCRInput) (*mcp.CallToolResult, getPageOCROutput, error) {
		start := time.Now()
		logToolStart(ctx, logger, ToolGetPageOCR, slog.String("pageId", in.PageID))

		if strings.TrimSpace(in.PageID) == "" {
			err := fmt.Errorf("fileee-mcp: tools: %s: page id must not be empty", ToolGetPageOCR)
			logToolEnd(ctx, logger, ToolGetPageOCR, start, "", 0, err)
			return nil, getPageOCROutput{}, err
		}

		client, err := clientFor(ctx, p)
		if err != nil {
			logToolEnd(ctx, logger, ToolGetPageOCR, start, "", 0, err)
			return nil, getPageOCROutput{}, err
		}
		result, out, err := documentPageOCRFromService(ctx, client.Documents, in.PageID)
		logToolEnd(ctx, logger, ToolGetPageOCR, start, getPageOCREndpoint, out.TokenCount, err)
		return result, out, err
	}
}

// documentPageOCRFromService is getPageOCRHandler's logic below client
// resolution — split out for the same testability reason
// documentPDFFromService is. Text is collected into lines for
// wrapUntrustedLines (read_generic.go) and never touches out.
func documentPageOCRFromService(ctx context.Context, service documentBinaryService, pageID string) (*mcp.CallToolResult, getPageOCROutput, error) {
	tokens, err := service.PageOCR(ctx, pageID)
	if err != nil {
		return nil, getPageOCROutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolGetPageOCR, err)
	}

	out := getPageOCROutput{TokenCount: len(tokens), Tokens: make([]ocrTokenPosition, 0, len(tokens))}
	lines := make([]string, 0, len(tokens))
	for i := range tokens {
		tok := tokens[i]
		out.Tokens = append(out.Tokens, ocrTokenPosition{
			WebappID: tok.WebappID,
			Left:     tok.Left,
			Top:      tok.Top,
			Right:    tok.Right,
			Bottom:   tok.Bottom,
			Width:    tok.Width,
			Height:   tok.Height,
		})
		if tok.Text != "" {
			lines = append(lines, tok.Text)
		}
	}

	result, err := wrapUntrustedLines(lines)
	if err != nil {
		return nil, getPageOCROutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolGetPageOCR, err)
	}
	return result, out, nil
}

// registerBinaryTools mounts get_document_pdf, get_page_image and
// get_page_ocr onto s — called once from RegisterAll (read.go).
func registerBinaryTools(s *mcp.Server, p *clientpool.Pool, logger *slog.Logger) {
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolGetDocumentPDF,
		Description: "Download a document's original PDF file. Returns it as embedded binary " +
			"resource content, capped at 8 MiB — a response larger than that fails with an " +
			"explicit error naming the byte count read, never a silently truncated file. Use it " +
			"after list_documents/get_document handed you a document ID and you need the file " +
			"itself, not just its metadata. It does not return a page's plain image (use " +
			"get_page_image for a page-by-page fallback) and it does not return the document's " +
			"OCR text (use get_page_ocr).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, Title: "Get document PDF"},
	}, getDocumentPDFHandler(p, logger))

	mcp.AddTool(s, &mcp.Tool{
		Name: ToolGetPageImage,
		Description: "Download a single page's rendered image — the fallback when no PDF is " +
			"available for a document. Returns it as image content, capped at 8 MiB with the " +
			"same explicit over-limit failure get_document_pdf uses, never a silently truncated " +
			"image. Requires the page's current imageVersion, freshly read, never cached — a " +
			"stale version returns a stale or missing image. Use it only when get_document_pdf " +
			"is not applicable. It does not return the document's OCR text and does not return " +
			"more than one page per call.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, Title: "Get page image"},
	}, getPageImageHandler(p, logger))

	mcp.AddTool(s, &mcp.Tool{
		Name: ToolGetPageOCR,
		Description: "Run Fileee's OCR lookup for a single page and return its recognized text. " +
			"Returns the token count and each token's bounding-box coordinates and Fileee's own " +
			"token ID structurally; the recognized text itself is included separately as clearly " +
			"marked, untrusted text, since it is copied verbatim from a document a third party " +
			"sent or scanned — the single strongest case of foreign content this server handles. " +
			"Use it when you need a page's actual wording, not just its existence or image. It " +
			"does not return the text under any structured field, and it does not search by " +
			"content.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, Title: "Get page OCR text"},
	}, getPageOCRHandler(p, logger))
}
