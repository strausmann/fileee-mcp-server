// read_reference.go wires four of go-fileee's seven ReadService[T]
// implementations — Tags, Companies, DocumentTypes, DocumentTypeSchemes —
// into list/get tool pairs via registerReadService (read_generic.go).
// These four are the account's reference/master data: categorisation and
// structural metadata a caller looks up before or alongside document
// operations, not documents themselves.
//
// Three of the four are entirely the account holder's own naming — a
// tag's name, a document type's display name, a scheme's field structure
// — and leave UntrustedLine/PoisonProbe nil (see readServiceDescriptor's
// own doc comment on what that means). Company is the exception: a
// company record Fileee extracted from a document (FromUserDB == false)
// carries the sender's own chosen name, not the account holder's — the
// same FromUserDB-gated distinction Contact already makes
// (read_sync.go's contactSyncDescriptor). Its descriptor therefore sets
// both fields; the other three do not.
package tools

import (
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
	"github.com/strausmann/fileee-mcp-server/internal/issued"
)

// referenceTagSummary is list_tags/get_tag's structured summary. Named
// with the "reference" prefix (not plain tagSummary) because
// read_generic_test.go already declares a package-level tagSummary
// fixture for its own tests — a second, production tagSummary would
// collide with it the moment this package compiles for `go test` (see
// this repo's Aufgabe 3 plan, "Globale Randbedingungen", for the same
// collision already hit once by read_sync.go's syncTagSummary).
//
// A tag's name is the account holder's own categorisation, never text a
// third party wrote (this file's own doc comment) — it appears here
// directly rather than through UntrustedLine.
type referenceTagSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// companySummary is list_companies/get_company's structured summary. It
// deliberately excludes CompanyName — see this file's own doc comment and
// referenceCompanyDescriptor's UntrustedLine for why a company Fileee
// extracted from a document (FromUserDB == false) carries the sender's
// own chosen name, not the account holder's.
type companySummary struct {
	ID              string `json:"id"`
	ContactType     string `json:"contactType"`
	ContactStatus   string `json:"contactStatus"`
	DocumentCounter int    `json:"documentCounter"`
	// FromUserDB is true when the account holder entered this company
	// themselves, false when Fileee extracted it from a document — the
	// same field UntrustedLine's own doc comment reasons from.
	FromUserDB bool `json:"fromUserDb"`
	Connected  bool `json:"connected"`
}

// documentTypeSummary is list_document_types/get_document_type's
// structured summary. Name is DocumentType.I18NName (capital N —
// go-fileee/fileee/types.go) — either one of Fileee's own built-in types
// or one the account holder created, never third-party text (this file's
// own doc comment).
type documentTypeSummary struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	DocumentTypeScheme string `json:"documentTypeScheme"`
	DocumentCounter    int    `json:"documentCounter"`
}

// documentTypeSchemeSummary is list_document_type_schemes/
// get_document_type_scheme's structured summary. FieldKeys is
// DocumentTypeScheme.Fields()'s own keys — the metadata field names a
// document type using this scheme carries, flattened out of
// SchemaDefinition.ComposingTypes (go-fileee/fileee/documenttypeschemes.go)
// rather than reproducing the whole (recursive, constraint-bearing) field
// tree wholesale.
type documentTypeSchemeSummary struct {
	ID        string   `json:"id"`
	FieldKeys []string `json:"fieldKeys"`
}

// referenceTagDescriptor describes list_tags/get_tag.
func referenceTagDescriptor() readServiceDescriptor[fileee.Tag, referenceTagSummary] {
	return readServiceDescriptor[fileee.Tag, referenceTagSummary]{
		ListName:  ToolListTags,
		ListTitle: "List tags",
		GetName:   ToolGetTag,
		GetTitle:  "Get tag",
		ListDescription: "List the tags defined in the calling user's Fileee account. Returns each " +
			"tag's ID and name — a category the account holder created themselves, never text a " +
			"third party wrote. Use it to discover which tags exist before filtering or describing " +
			"documents. It does not return the documents carrying a tag — use list_documents for that.",
		GetDescription: "Load a single tag by its ID. Returns the tag's ID and name, the same fields " +
			"list_tags exposes for each entry. Use it when another tool handed you a tag ID and you " +
			"need its name. It does not search by name and does not return the documents carrying " +
			"the tag.",
		Service:   func(c *fileee.Client) fileee.ReadService[fileee.Tag] { return c.Tags },
		Summarize: func(t *fileee.Tag) referenceTagSummary { return referenceTagSummary{ID: t.ID, Name: t.Name} },
		IDOf:      func(t *fileee.Tag) string { return t.ID },
	}
}

// referenceCompanyDescriptor describes list_companies/get_company.
func referenceCompanyDescriptor() readServiceDescriptor[fileee.Company, companySummary] {
	return readServiceDescriptor[fileee.Company, companySummary]{
		ListName:  ToolListCompanies,
		ListTitle: "List companies",
		GetName:   ToolGetCompany,
		GetTitle:  "Get company",
		ListDescription: "List the companies recorded in the calling user's Fileee account — both " +
			"ones the account holder entered themselves and ones Fileee extracted automatically from " +
			"a document. Returns each company's ID and Fileee-owned metadata (contact type, contact " +
			"status, document counter, whether it came from the account holder's own data or was " +
			"extracted); the company name is included separately as clearly marked, untrusted text, " +
			"since an automatically extracted company's name was written by whoever sent the " +
			"document, not by the account holder. It does not return the contacts or documents " +
			"linked to a company.",
		GetDescription: "Load a single company by its ID. Returns the same Fileee-owned metadata " +
			"list_companies exposes, plus the company name as clearly marked, untrusted text for the " +
			"same reason list_companies frames it — an automatically extracted company's name comes " +
			"from whoever sent the document, not from the account holder. Use it when another tool " +
			"handed you a company ID and you need its details. It does not search by company name and " +
			"does not return the contacts or documents linked to the company.",
		Service: func(c *fileee.Client) fileee.ReadService[fileee.Company] { return c.Companies },
		Summarize: func(c *fileee.Company) companySummary {
			return companySummary{
				ID:              c.ID,
				ContactType:     c.ContactType,
				ContactStatus:   c.ContactStatus,
				DocumentCounter: c.DocumentCounter,
				FromUserDB:      c.FromUserDB,
				Connected:       c.Connected,
			}
		},
		UntrustedLine: func(c *fileee.Company) string { return c.CompanyName },
		PoisonProbe:   func(marker string) *fileee.Company { return &fileee.Company{CompanyName: marker} },
		IDOf:          func(c *fileee.Company) string { return c.ID },
	}
}

// referenceDocumentTypeDescriptor describes list_document_types/get_document_type.
func referenceDocumentTypeDescriptor() readServiceDescriptor[fileee.DocumentType, documentTypeSummary] {
	return readServiceDescriptor[fileee.DocumentType, documentTypeSummary]{
		ListName:  ToolListDocumentTypes,
		ListTitle: "List document types",
		GetName:   ToolGetDocumentType,
		GetTitle:  "Get document type",
		ListDescription: "List the document types defined in the calling user's Fileee account — " +
			"Fileee's own built-in types plus any the account holder created. Returns each type's " +
			"ID, display name, the ID of the document-type scheme it uses, and how many documents " +
			"carry it — all Fileee-owned or account-holder-chosen values, never third-party text. " +
			"Use it to discover which types exist before filtering or classifying documents. It does " +
			"not return the scheme's own field definitions — pass the returned scheme ID to " +
			"get_document_type_scheme for that.",
		GetDescription: "Load a single document type by its ID. Returns the same fields " +
			"list_document_types exposes: display name, the ID of the document-type scheme it uses, " +
			"and its document counter. Use it when another tool handed you a document type ID and " +
			"you need its name or scheme reference. It does not search by name and does not return " +
			"the scheme's own field definitions.",
		Service: func(c *fileee.Client) fileee.ReadService[fileee.DocumentType] { return c.DocumentTypes },
		Summarize: func(dt *fileee.DocumentType) documentTypeSummary {
			return documentTypeSummary{
				ID:                 dt.ID,
				Name:               dt.I18NName,
				DocumentTypeScheme: dt.DocumentTypeScheme,
				DocumentCounter:    dt.DocumentCounter,
			}
		},
		IDOf: func(dt *fileee.DocumentType) string { return dt.ID },
	}
}

// referenceDocumentTypeSchemeDescriptor describes
// list_document_type_schemes/get_document_type_scheme.
func referenceDocumentTypeSchemeDescriptor() readServiceDescriptor[fileee.DocumentTypeScheme, documentTypeSchemeSummary] {
	return readServiceDescriptor[fileee.DocumentTypeScheme, documentTypeSchemeSummary]{
		ListName:  ToolListDocumentTypeSchemes,
		ListTitle: "List document type schemes",
		GetName:   ToolGetDocumentTypeScheme,
		GetTitle:  "Get document type scheme",
		ListDescription: "List the document-type schemes defined in the calling user's Fileee " +
			"account — the field definitions a document type can use. Returns each scheme's ID and " +
			"the keys of the metadata fields it composes, flattened out of its field tree — " +
			"structural configuration, never third-party text. Use it to discover which schemes " +
			"exist before inspecting one in detail with get_document_type_scheme. It does not return " +
			"which document types reference a given scheme.",
		GetDescription: "Load a single document-type scheme by its ID. Returns the same fields " +
			"list_document_type_schemes exposes: the scheme's ID and the keys of the metadata " +
			"fields it composes. Use it when a document type's own scheme reference handed you a " +
			"scheme ID and you need its field list. It does not search by field key and does not " +
			"return which document types reference this scheme.",
		Service: func(c *fileee.Client) fileee.ReadService[fileee.DocumentTypeScheme] {
			return c.DocumentTypeSchemes
		},
		Summarize: func(sch *fileee.DocumentTypeScheme) documentTypeSchemeSummary {
			fields := sch.Fields()
			keys := make([]string, 0, len(fields))
			for _, f := range fields {
				keys = append(keys, f.Key)
			}
			return documentTypeSchemeSummary{ID: sch.ID, FieldKeys: keys}
		},
		IDOf: func(sch *fileee.DocumentTypeScheme) string { return sch.ID },
	}
}

// registerReferenceTools mounts this file's four descriptors onto s —
// called once from RegisterAll (read.go). logger and rec (Aufgabe 4) are
// threaded straight through to registerReadService — get_* nimmt die vom
// Aufrufer genannte ID auf. registerSyncTools (read_sync.go) bekommt seit
// der Verschärfung auf gezielte Einzelabrufe (ADR-0019) kein rec mehr:
// sync_* nimmt nichts mehr auf.
func registerReferenceTools(s *mcp.Server, p *clientpool.Pool, logger *slog.Logger, rec *issued.Store) {
	registerReadService(s, p, logger, referenceTagDescriptor(), rec)
	registerReadService(s, p, logger, referenceCompanyDescriptor(), rec)
	registerReadService(s, p, logger, referenceDocumentTypeDescriptor(), rec)
	registerReadService(s, p, logger, referenceDocumentTypeSchemeDescriptor(), rec)
}
