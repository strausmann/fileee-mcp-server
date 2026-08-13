// read_people.go wires three of go-fileee's seven ReadService[T]
// implementations — Contacts, Reminders, Conversations — into list/get tool
// pairs via registerReadService (read_generic.go), the same generic helper
// Aufgabe 3's read_reference.go uses for the account's reference data.
//
// Unlike three of Aufgabe 3's four reference-data types, all three types
// here carry foreign text: a contact's own name (supplied by the contact
// itself or extracted from a document, fileee.Contact's FromUserDB field),
// a reminder's description (which may be copied from the document it is
// attached to, reminders.go's own doc comment on fileee.Reminder), and a
// conversation's subject (chosen by whoever is on the other end). Every
// descriptor in this file therefore sets BOTH UntrustedLine and
// PoisonProbe — see readServiceDescriptor's own doc comment (read_generic.go)
// for what a nil pair versus a set pair means, and why getting this wrong
// for even one of the three would leave that tool's foreign text unframed
// with no test failure to show for it (a missing UntrustedLine call site
// produces an empty text block, not an error).
//
// Each summary struct mirrors its already-reviewed sync counterpart
// (syncContactSummary/syncReminderSummary/syncConversationSummary,
// read_sync.go) field-for-field, deliberately — the two views (incremental
// diff vs. list/get) should agree on what counts as Fileee-owned metadata
// versus foreign text for the same type, and reusing that already-settled
// classification is cheaper and safer than re-deriving it. contactSummary
// additionally exposes ConnectedToOtherUser and FromUserDB, the same
// provenance fields Aufgabe 3's companySummary (read_reference.go) already
// added beyond its own sync twin — list/get tools return richer structured
// metadata than the sync tools' minimal-diff view, on purpose.
package tools

import (
	"log/slog"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

// contactSummary is list_contacts/get_contact's structured summary. It
// deliberately excludes FirstName, LastName, CompanyName, Email,
// PhoneNumber, FaxNumber, URL, SupportURL, UserPortalURL, and Address —
// all either the contact's own supplied data or extracted from a document
// (Feldnamen-Recherche, Abschnitt Contact, Fallstrick 1/2) — never Fileee's
// own. contactDescriptor's UntrustedLine composes the display name instead,
// framed, never structured; the other free-text fields are dropped
// entirely rather than framed, matching contactSyncDescriptor's own choice
// (read_sync.go) — a caller needing them would go through a future,
// separate tool with its own framing, not this one.
type contactSummary struct {
	ID                   string `json:"id"`
	CompanyID            string `json:"companyId"`
	ContactType          string `json:"contactType"`
	ContactStatus        string `json:"contactStatus"`
	ConnectedToOtherUser bool   `json:"connectedToOtherUser"`
	FromUserDB           bool   `json:"fromUserDb"`
	DocumentCounter      int    `json:"documentCounter"`
}

// reminderSummary is list_reminders/get_reminder's structured summary. It
// deliberately excludes Description and DetailedDescription — a reminder's
// own text can be copied from the document it is attached to
// (reminders.go's own doc comment on fileee.Reminder). reminderDescriptor's
// UntrustedLine frames Description; DetailedDescription is dropped
// entirely rather than framed, matching reminderSyncDescriptor's own
// choice (read_sync.go).
type reminderSummary struct {
	ID         string `json:"id"`
	DocumentID string `json:"documentId"`
	StartDate  string `json:"startDate"`
	Done       bool   `json:"done"`
}

// conversationSummary is list_conversations/get_conversation's structured
// summary. It deliberately excludes Title (chosen by whoever is on the
// other end) AND every participant's own Name and every message's Text/
// SenderName — Feldnamen-Recherche's own finding, beyond what the already-
// reviewed syncConversationSummary needed to consider: Participant.Name
// and Message.Text/SenderName are just as foreign as Title, authored by
// whoever is on the other end of the conversation, not the account
// holder. ParticipantCount is a plain integer count, never a name list —
// see TestKonversationSummaryZaehltNurTeilnehmer (read_people_test.go) for
// the regression this guards. conversationDescriptor's UntrustedLine
// frames only Title; participant names and message content are dropped
// entirely rather than framed, matching conversationSyncDescriptor's own
// choice (read_sync.go) — Raw (fileee.Conversation.Raw/fileee.Message.Raw)
// is never touched here at all, since it carries the complete, unframed
// JSON of the conversation including every message (Feldnamen-Recherche,
// Abschnitt Conversation, Fallstrick 2).
type conversationSummary struct {
	ID               string `json:"id"`
	ConversationType string `json:"conversationType"`
	Kind             string `json:"kind"`
	ParticipantCount int    `json:"participantCount"`
}

// contactDescriptor describes list_contacts/get_contact.
func contactDescriptor() readServiceDescriptor[fileee.Contact, contactSummary] {
	return readServiceDescriptor[fileee.Contact, contactSummary]{
		ListName: ToolListContacts,
		GetName:  ToolGetContact,
		ListDescription: "List the contacts in the calling user's Fileee account. Returns each " +
			"contact's ID and Fileee-owned metadata (linked company ID, contact type, contact " +
			"status, whether it is connected to another Fileee account or was entered by the " +
			"account holder, document counter); the contact's own name is included separately as " +
			"clearly marked, untrusted text, since it was supplied by that contact or extracted " +
			"from a document, not written by the account holder. It does not search by name and " +
			"does not return the contact's email, phone number, or address.",
		GetDescription: "Load a single contact by its ID. Returns the same Fileee-owned metadata " +
			"list_contacts exposes, plus the contact's own name as clearly marked, untrusted text " +
			"for the same reason list_contacts frames it. Use it when another tool handed you a " +
			"contact ID and you need its details. It does not search by name and does not return " +
			"the contact's email, phone number, or address.",
		Service: func(c *fileee.Client) fileee.ReadService[fileee.Contact] { return c.Contacts },
		Summarize: func(c *fileee.Contact) contactSummary {
			return contactSummary{
				ID:                   c.ID,
				CompanyID:            c.CompanyID,
				ContactType:          string(c.ContactType),
				ContactStatus:        string(c.ContactStatus),
				ConnectedToOtherUser: c.ConnectedToOtherUser,
				FromUserDB:           c.FromUserDB,
				DocumentCounter:      c.DocumentCounter,
			}
		},
		UntrustedLine: func(c *fileee.Contact) string {
			return strings.TrimSpace(c.FirstName + " " + c.LastName)
		},
		PoisonProbe: func(marker string) *fileee.Contact { return &fileee.Contact{LastName: marker} },
	}
}

// reminderDescriptor describes list_reminders/get_reminder.
func reminderDescriptor() readServiceDescriptor[fileee.Reminder, reminderSummary] {
	return readServiceDescriptor[fileee.Reminder, reminderSummary]{
		ListName: ToolListReminders,
		GetName:  ToolGetReminder,
		ListDescription: "List the reminders in the calling user's Fileee account. Returns each " +
			"reminder's ID, the linked document ID, its start date, and whether it is done; the " +
			"reminder's own description is included separately as clearly marked, untrusted text, " +
			"since it may have been copied from the document it is attached to rather than written " +
			"by the account holder. It does not search by description and does not return the " +
			"reminder's detailed description.",
		GetDescription: "Load a single reminder by its ID. Returns the same fields list_reminders " +
			"exposes — the linked document ID, its start date, and whether it is done — plus the " +
			"reminder's own description as clearly marked, untrusted text for the same reason " +
			"list_reminders frames it. Use it when another tool handed you a reminder ID and you " +
			"need its details. It does not return the reminder's detailed description.",
		Service: func(c *fileee.Client) fileee.ReadService[fileee.Reminder] { return c.Reminders },
		Summarize: func(r *fileee.Reminder) reminderSummary {
			return reminderSummary{ID: r.ID, DocumentID: r.DocumentID, StartDate: r.StartDate, Done: r.Done}
		},
		UntrustedLine: func(r *fileee.Reminder) string { return r.Description },
		PoisonProbe:   func(marker string) *fileee.Reminder { return &fileee.Reminder{Description: marker} },
	}
}

// conversationDescriptor describes list_conversations/get_conversation.
func conversationDescriptor() readServiceDescriptor[fileee.Conversation, conversationSummary] {
	return readServiceDescriptor[fileee.Conversation, conversationSummary]{
		ListName: ToolListConversations,
		GetName:  ToolGetConversation,
		ListDescription: "List the conversations in the calling user's Fileee account. Returns " +
			"each conversation's ID, its type, its kind, and how many participants it has; the " +
			"conversation's own subject is included separately as clearly marked, untrusted text, " +
			"since it was chosen by whoever is on the other end, not by the account holder. It " +
			"does not search by subject and does not return participant names or message content " +
			"— both are also chosen by whoever is on the other end, and are out of scope for this " +
			"tool.",
		GetDescription: "Load a single conversation by its ID. Returns the same fields " +
			"list_conversations exposes — its type, its kind, and how many participants it has — " +
			"plus its own subject as clearly marked, untrusted text for the same reason " +
			"list_conversations frames it. Use it when another tool handed you a conversation ID " +
			"and you need its details. It does not return participant names or message content.",
		Service: func(c *fileee.Client) fileee.ReadService[fileee.Conversation] { return c.Conversations },
		Summarize: func(c *fileee.Conversation) conversationSummary {
			return conversationSummary{
				ID:               c.ID,
				ConversationType: c.ConversationType,
				Kind:             c.Kind,
				ParticipantCount: len(c.Participants),
			}
		},
		UntrustedLine: func(c *fileee.Conversation) string { return c.Title },
		PoisonProbe:   func(marker string) *fileee.Conversation { return &fileee.Conversation{Title: marker} },
	}
}

// registerPeopleTools mounts this file's three descriptors onto s — called
// once from RegisterRead (read.go). logger is threaded straight through to
// registerReadService, the same pattern registerReferenceTools already
// follows (read_reference.go) for its own four descriptors.
func registerPeopleTools(s *mcp.Server, p *clientpool.Pool, logger *slog.Logger) {
	registerReadService(s, p, logger, contactDescriptor())
	registerReadService(s, p, logger, reminderDescriptor())
	registerReadService(s, p, logger, conversationDescriptor())
}
