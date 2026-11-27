package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
	"github.com/strausmann/go-fileee/fileee"
	"golang.org/x/sync/singleflight"
)

const (
	contactSearchMaxRows      = 1000
	contactSearchCacheEntries = 4
	contactSearchCacheTTL     = 2 * time.Minute
)

// The client appends contact IDs to a path. Company responses can supply
// those IDs, so accept only a single safe path segment.
var contactSearchIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

type searchContactsInput struct {
	Query   string `json:"query"`
	Start   int    `json:"start,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Refresh bool   `json:"refresh,omitempty"`
}

type contactSearchCoverage struct {
	Scanned   int  `json:"scanned"`
	TotalRows int  `json:"totalRows"`
	Complete  bool `json:"complete"`
}

type contactSearchDetailCoverage struct {
	Requested int  `json:"requested"`
	Loaded    int  `json:"loaded"`
	Complete  bool `json:"complete"`
}

type contactSearchCompanyReference struct {
	CompanyID              string `json:"companyId"`
	Lookup                 string `json:"lookup"`
	EntityKind             string `json:"entityKind"`
	ContactStatus          string `json:"contactStatus"`
	FromUserDB             *bool  `json:"fromUserDb"`
	DocumentCounter        *int   `json:"documentCounter"`
	Created                string `json:"created"`
	Modified               string `json:"modified"`
	MainContactID          string `json:"mainContactId"`
	UserPreferredContactID string `json:"userPreferredContactId"`
	LinkedByContact        bool   `json:"linkedByContact"`
	IsMainContact          bool   `json:"isMainContact"`
	IsPreferredContact     bool   `json:"isPreferredContact"`
}

type contactSearchCandidateSummary struct {
	ContactID            string                                   `json:"contactId"`
	CompanyID            string                                   `json:"companyId"`
	ContactLookup        string                                   `json:"contactLookup"`
	LookupErrorKind      string                                   `json:"lookupErrorKind,omitempty"`
	EntityKind           string                                   `json:"entityKind"`
	ContactStatus        string                                   `json:"contactStatus"`
	FromUserDB           *bool                                    `json:"fromUserDb"`
	ConnectedToOtherUser *bool                                    `json:"connectedToOtherUser"`
	DocumentCounter      *int                                     `json:"documentCounter"`
	Deleted              *bool                                    `json:"deleted"`
	Created              string                                   `json:"created"`
	Modified             string                                   `json:"modified"`
	CompanyReferences    map[string]contactSearchCompanyReference `json:"companyReferences"`
	UnavailableFields    []string                                 `json:"unavailableFields"`
}

type searchContactsOutput struct {
	untrustedTextField
	Candidates                    map[string]contactSearchCandidateSummary `json:"candidates"`
	CandidateIDs                  []string                                 `json:"candidateIds"`
	TotalMatches                  int                                      `json:"totalMatches"`
	Start                         int                                      `json:"start"`
	Limit                         int                                      `json:"limit"`
	HasMore                       bool                                     `json:"hasMore"`
	NextStart                     *int                                     `json:"nextStart,omitempty"`
	Contacts                      contactSearchDetailCoverage              `json:"contacts"`
	Companies                     contactSearchCoverage                    `json:"companies"`
	SearchScope                   string                                   `json:"searchScope"`
	ContactsDirectoryScanned      bool                                     `json:"contactsDirectoryScanned"`
	SelfLookup                    string                                   `json:"selfLookup"`
	SelfLookupErrorKind           string                                   `json:"selfLookupErrorKind,omitempty"`
	CoverageComplete              bool                                     `json:"coverageComplete"`
	IdentityDetailsComplete       bool                                     `json:"identityDetailsComplete"`
	UnresolvedCompanyCount        int                                      `json:"unresolvedCompanyCount"`
	UnresolvedMatchedCompanyCount int                                      `json:"unresolvedMatchedCompanyCount"`
	CacheUsed                     bool                                     `json:"cacheUsed"`
	SnapshotAt                    string                                   `json:"snapshotAt"`
	CacheExpiresAt                string                                   `json:"cacheExpiresAt"`
}

// Identity DTOs are marshalled only inside a fresh untrusted frame. No
// name, address, or other foreign identity value enters the summary DTO.
type contactSearchAddress struct {
	Street        string `json:"street"`
	SecondLine    string `json:"secondLine"`
	ZipCode       string `json:"zipCode"`
	City          string `json:"city"`
	State         string `json:"state"`
	Country       string `json:"country"`
	CountryLocale string `json:"countryLocale"`
}

type contactSearchIdentity struct {
	DisplayName       string               `json:"displayName"`
	DisplayNameSource string               `json:"displayNameSource"`
	FirstName         string               `json:"firstName"`
	LastName          string               `json:"lastName"`
	CompanyName       string               `json:"companyName"`
	Email             string               `json:"email"`
	PhoneNumber       string               `json:"phoneNumber"`
	FaxNumber         string               `json:"faxNumber"`
	URL               string               `json:"url"`
	SupportURL        string               `json:"supportUrl"`
	UserPortalURL     string               `json:"userPortalUrl"`
	Address           contactSearchAddress `json:"address"`
	Domains           []string             `json:"domains"`
}

type contactSearchCompanyIdentity struct {
	contactSearchCompanyReference
	Name         string   `json:"name"`
	Emails       []string `json:"emails"`
	PhoneNumbers []string `json:"phoneNumbers"`
	Websites     []string `json:"websites"`
	Domains      []string `json:"domains"`
}

type contactSearchCandidateIdentity struct {
	contactSearchCandidateSummary
	Identity      contactSearchIdentity                   `json:"identity"`
	CompaniesByID map[string]contactSearchCompanyIdentity `json:"companiesById"`
	MatchedFields []string                                `json:"matchedFields"`
}

type contactSearchQueryService[T any] interface {
	Query(context.Context, fileee.QueryOptions) (*fileee.QueryResult[T], error)
}

// Pages cover only the server-filtered name query, never the shared full
// directory. An early empty page, changing total, failed query, or match
// cap is an error, never evidence that an existing contact does not exist.
func contactSearchAllRows[T any](ctx context.Context, service contactSearchQueryService[T], idOf func(T) string, opts fileee.QueryOptions) ([]T, contactSearchCoverage, error) {
	coverage := contactSearchCoverage{}
	rows := make([]T, 0)
	seen := make(map[string]bool)
	for {
		opts.Start, opts.Limit = len(rows), maxLimit
		page, err := service.Query(ctx, opts)
		if err != nil {
			return nil, coverage, fmt.Errorf("company name lookup incomplete: %w", err)
		}
		if page == nil || page.TotalRows < 0 || len(page.Rows) > maxLimit || page.TotalRows < len(rows)+len(page.Rows) {
			return nil, coverage, errors.New("company name lookup incomplete: invalid page")
		}
		if page.TotalRows > contactSearchMaxRows {
			return nil, coverage, fmt.Errorf("company name lookup incomplete: more than %d matches; use a more distinctive name", contactSearchMaxRows)
		}
		if len(rows) > 0 && page.TotalRows != coverage.TotalRows {
			return nil, coverage, errors.New("company name lookup incomplete: total changed during pagination; retry with refresh")
		}
		coverage.TotalRows = page.TotalRows
		for _, row := range page.Rows {
			id := idOf(row)
			if id == "" || seen[id] {
				return nil, coverage, errors.New("company name lookup incomplete: missing or repeated entity ID")
			}
			seen[id] = true
			rows = append(rows, row)
		}
		coverage.Scanned = len(rows)
		if coverage.Scanned == coverage.TotalRows {
			coverage.Complete = true
			return rows, coverage, nil
		}
		if len(page.Rows) == 0 {
			return nil, coverage, errors.New("company name lookup incomplete: empty page before reported total")
		}
	}
}

type contactSearchSnapshot struct {
	contacts            map[string]fileee.Contact
	companies           map[string]fileee.Company
	matchedCompanies    map[string]bool
	companyReferences   map[string]map[string]bool
	companyContacts     map[string]map[string]bool
	companyCoverage     contactSearchCoverage
	unresolved          int
	selfLookup          string
	selfLookupErrorKind string
	loadedAt            time.Time
	mu                  sync.Mutex
	details             map[string]*fileee.Contact
	detailFlights       singleflight.Group
}

func resolveContactSearchSelf(ctx context.Context, client *fileee.Client) (*fileee.Contact, *fileee.Company, error) {
	page, err := client.Companies.Query(ctx, fileee.QueryOptions{Limit: 2, Criteria: []fileee.Criterion{{Field: "ContactinyObjectField:CONTACT_TYPE", Operator: fileee.OpEQ, Value: "ContactType:ME", FieldType: "Enum", ValueType: "Enum"}}})
	if err != nil {
		return nil, nil, err
	}
	if page == nil || page.TotalRows != 1 || len(page.Rows) != 1 {
		return nil, nil, errors.New("authenticated ME company query did not return exactly one entity")
	}
	company := page.Rows[0]
	if company.Deleted || company.ContactType != string(fileee.ContactTypeMe) || !contactSearchIDPattern.MatchString(company.ID) {
		return nil, nil, errors.New("authenticated ME company could not be verified")
	}
	id := company.UserPreferredContactID
	if id == "" {
		id = company.MainContactID
	}
	if !contactSearchIDPattern.MatchString(id) {
		return nil, &company, errors.New("authenticated ME contact reference is unavailable")
	}
	contact, err := client.Contacts.Get(ctx, id)
	if err != nil {
		return nil, &company, err
	}
	if contact == nil || contact.ID != id || contact.CompanyID != company.ID || contact.ContactType != fileee.ContactTypeMe || contact.Deleted {
		return nil, &company, errors.New("authenticated ME contact could not be verified")
	}
	return contact, &company, nil
}

type contactSearchSelfResolver func(context.Context, *fileee.Client) (*fileee.Contact, *fileee.Company, error)

func loadContactSearchSnapshot(ctx context.Context, client *fileee.Client, query string, selfResolver contactSearchSelfResolver) (*contactSearchSnapshot, error) {
	selfContact, selfCompany, selfErr := selfResolver(ctx, client)
	companies, companyCoverage, err := contactSearchAllRows(ctx, client.Companies, func(c fileee.Company) string { return c.ID }, fileee.QueryOptions{
		Criteria: []fileee.Criterion{
			{Field: "ContactinyObjectField:CONTACT_TYPE", Operator: fileee.OpNEQ, Value: "ContactType:ME", FieldType: "Enum", ValueType: "Enum"},
			{Field: "ContactinyObjectField:COMPANY_NAME", Operator: fileee.OpFuzzy, Value: query, FieldType: "Enum", ValueType: "String"},
		},
		SortOrder: []fileee.SortField{{Field: "HasDocumentCounterField:DOCUMENT_COUNTER", FieldType: "Enum", Desc: true}, {Field: "ContactinyObjectField:COMPANY_NAME", FieldType: "Enum"}, {Field: "EntityObjectField:ID", FieldType: "Enum"}},
	})
	if err != nil {
		return nil, err
	}
	snapshot := &contactSearchSnapshot{contacts: make(map[string]fileee.Contact), companies: make(map[string]fileee.Company), matchedCompanies: make(map[string]bool), companyReferences: make(map[string]map[string]bool), companyContacts: make(map[string]map[string]bool), details: make(map[string]*fileee.Contact), companyCoverage: companyCoverage, selfLookup: "found"}
	if selfErr != nil {
		snapshot.selfLookup = "error"
		snapshot.selfLookupErrorKind, _ = classifyErr(selfErr)
	}
	for _, company := range companies {
		if company.ContactType == string(fileee.ContactTypeMe) {
			return nil, errors.New("company name lookup incomplete: Fileee ignored the ME exclusion")
		}
		if company.Deleted {
			continue
		}
		snapshot.companies[company.ID] = company
		snapshot.matchedCompanies[company.ID] = true
		for _, id := range []string{company.MainContactID, company.UserPreferredContactID} {
			if id != "" {
				snapshot.addCompanyReference(id, company.ID)
			}
		}
	}
	if selfContact != nil {
		snapshot.contacts[selfContact.ID] = *selfContact
		snapshot.addCompanyReference(selfContact.ID, selfContact.CompanyID)
		if selfCompany == nil {
			snapshot.unresolved = 1
		}
	}
	if selfCompany != nil {
		snapshot.companies[selfCompany.ID] = *selfCompany
	}
	return snapshot, nil
}

func (s *contactSearchSnapshot) addCompanyReference(contactID, companyID string) {
	if s.companyReferences[contactID] == nil {
		s.companyReferences[contactID] = make(map[string]bool)
	}
	s.companyReferences[contactID][companyID] = true
	if s.companyContacts[companyID] == nil {
		s.companyContacts[companyID] = make(map[string]bool)
	}
	s.companyContacts[companyID][contactID] = true
}

type contactSearchCache struct {
	mu      sync.Mutex
	entries map[contactSearchCacheKey]*contactSearchSnapshot
	flights singleflight.Group
	now     func() time.Time
	self    contactSearchSelfResolver
}

type contactSearchCacheKey struct {
	client *fileee.Client
	query  string
}

func newContactSearchCache() *contactSearchCache {
	return &contactSearchCache{entries: make(map[contactSearchCacheKey]*contactSearchSnapshot), now: time.Now, self: resolveContactSearchSelf}
}

func (c *contactSearchCache) cached(key contactSearchCacheKey) *contactSearchSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	snapshot := c.entries[key]
	if snapshot != nil && c.now().Sub(snapshot.loadedAt) < contactSearchCacheTTL {
		return snapshot
	}
	return nil
}

func (c *contactSearchCache) store(key contactSearchCacheKey, snapshot *contactSearchSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snapshot.loadedAt = c.now()
	for len(c.entries) >= contactSearchCacheEntries {
		var oldestKey contactSearchCacheKey
		var oldest *contactSearchSnapshot
		for key, candidate := range c.entries {
			if oldest == nil || candidate.loadedAt.Before(oldest.loadedAt) {
				oldestKey, oldest = key, candidate
			}
		}
		delete(c.entries, oldestKey)
	}
	c.entries[key] = snapshot
}

type contactSearchCacheResult struct {
	snapshot *contactSearchSnapshot
	cached   bool
}

func (c *contactSearchCache) load(ctx context.Context, client *fileee.Client, query string, refresh bool) (*contactSearchSnapshot, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	key := contactSearchCacheKey{client: client, query: strings.ToLower(query)}
	if !refresh {
		if snapshot := c.cached(key); snapshot != nil {
			return snapshot, true, nil
		}
	}
	flight := c.flights.DoChan(fmt.Sprintf("%p:%s", client, key.query), func() (any, error) {
		if !refresh {
			if snapshot := c.cached(key); snapshot != nil {
				return contactSearchCacheResult{snapshot: snapshot, cached: true}, nil
			}
		}
		snapshot, err := loadContactSearchSnapshot(ctx, client, query, c.self)
		if err != nil {
			return nil, err
		}
		c.store(key, snapshot)
		return contactSearchCacheResult{snapshot: snapshot}, nil
	})
	select {
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case result := <-flight:
		if result.Err != nil {
			return nil, false, result.Err
		}
		value := result.Val.(contactSearchCacheResult)
		return value.snapshot, value.cached, nil
	}
}

func contactSearchFold(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

func normalizeContactSearch(in searchContactsInput) (searchContactsInput, []string, error) {
	if len(in.Query) > 256 || in.Start < 0 || in.Limit < 0 || in.Limit > maxLimit {
		return in, nil, errors.New("query must contain a name of at most 256 bytes, start must be non-negative, and limit must be between 1 and 100")
	}
	terms := make([]string, 0)
	for _, word := range strings.Fields(in.Query) {
		if folded := contactSearchFold(word); folded != "" {
			terms = append(terms, folded)
		}
	}
	if len(terms) == 0 {
		return in, nil, errors.New("query must contain at least one letter or digit from a person or company name")
	}
	if in.Limit == 0 {
		in.Limit = defaultLimit
	}
	in.Query = strings.Join(strings.Fields(in.Query), " ")
	return in, terms, nil
}

func contactSearchMatches(name string, terms []string) bool {
	folded := contactSearchFold(name)
	for _, term := range terms {
		if !strings.Contains(folded, term) {
			return false
		}
	}
	return true
}

func (s *contactSearchSnapshot) matches(terms []string) (map[string][]string, int) {
	matched := make(map[string][]string)
	for id, contact := range s.contacts {
		if contactSearchMatches(contact.FirstName+" "+contact.LastName, terms) {
			matched[id] = append(matched[id], "contact.personName")
		}
		if contactSearchMatches(contact.CompanyName, terms) {
			matched[id] = append(matched[id], "contact.companyName")
		}
	}
	unresolved := 0
	for companyID := range s.matchedCompanies {
		found := false
		for contactID := range s.companyContacts[companyID] {
			matched[contactID] = append(matched[contactID], "company.companyName")
			found = true
		}
		if !found {
			unresolved++
		}
	}
	for id, contact := range s.contacts {
		if company, ok := s.companies[contact.CompanyID]; ok && contactSearchMatches(company.CompanyName, terms) {
			matched[id] = append(matched[id], "self.companyName")
		}
	}
	return matched, unresolved
}

func (s *contactSearchSnapshot) contact(ctx context.Context, service interface {
	Get(context.Context, string) (*fileee.Contact, error)
}, id string) (*fileee.Contact, string, string) {
	if !contactSearchIDPattern.MatchString(id) {
		return nil, "invalid_id", "invalid_input"
	}
	if contact, ok := s.contacts[id]; ok {
		return &contact, "found", ""
	}
	s.mu.Lock()
	contact := s.details[id]
	s.mu.Unlock()
	if contact != nil {
		return contact, "found", ""
	}
	value, err, _ := s.detailFlights.Do(id, func() (any, error) {
		s.mu.Lock()
		cached := s.details[id]
		s.mu.Unlock()
		if cached != nil {
			return cached, nil
		}
		loaded, err := service.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if loaded == nil || loaded.ID != id {
			return nil, errors.New("contact lookup returned no matching contact ID")
		}
		s.mu.Lock()
		s.details[id] = loaded
		s.mu.Unlock()
		return loaded, nil
	})
	if err != nil {
		if errors.Is(err, fileee.ErrNotFound) {
			return nil, "not_found", "fileee_error"
		}
		kind, _ := classifyErr(err)
		return nil, "error", kind
	}
	return value.(*fileee.Contact), "found", ""
}

func contactSearchKnown[T any](value T) *T { return &value }

func contactSearchDomains(values ...string) []string {
	seen := make(map[string]bool)
	for _, value := range values {
		if at := strings.LastIndex(value, "@"); at >= 0 {
			value = value[at+1:]
		} else {
			if !strings.Contains(value, "://") {
				value = "https://" + value
			}
			parsed, err := url.Parse(value)
			if err != nil {
				continue
			}
			value = parsed.Hostname()
		}
		if value = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(value, "."))); value != "" {
			seen[value] = true
		}
	}
	domains := make([]string, 0, len(seen))
	for value := range seen {
		domains = append(domains, value)
	}
	sort.Strings(domains)
	return domains
}

func (s *contactSearchSnapshot) candidate(id string, contact *fileee.Contact, lookup, kind string, fields []string) contactSearchCandidateIdentity {
	summary := contactSearchCandidateSummary{ContactID: id, ContactLookup: lookup, LookupErrorKind: kind, EntityKind: "unknown", CompanyReferences: make(map[string]contactSearchCompanyReference), UnavailableFields: []string{}}
	identity := contactSearchIdentity{DisplayNameSource: "unavailable", Domains: []string{}}
	if contact != nil {
		summary.CompanyID, summary.EntityKind, summary.ContactStatus = contact.CompanyID, string(contact.ContactType), string(contact.ContactStatus)
		summary.FromUserDB, summary.ConnectedToOtherUser = contactSearchKnown(contact.FromUserDB), contactSearchKnown(contact.ConnectedToOtherUser)
		summary.DocumentCounter, summary.Deleted = contactSearchKnown(contact.DocumentCounter), contactSearchKnown(contact.Deleted)
		summary.Created, summary.Modified = contact.Created, contact.Modified
		identity.FirstName, identity.LastName, identity.CompanyName = contact.FirstName, contact.LastName, contact.CompanyName
		identity.Email, identity.PhoneNumber, identity.FaxNumber = contact.Email, contact.PhoneNumber, contact.FaxNumber
		identity.URL, identity.SupportURL, identity.UserPortalURL = contact.URL, contact.SupportURL, contact.UserPortalURL
		identity.Address = contactSearchAddress{Street: contact.Address.Street, SecondLine: contact.Address.SecondLine, ZipCode: contact.Address.ZipCode, City: contact.Address.City, State: contact.Address.State, Country: contact.Address.Country, CountryLocale: contact.Address.CountryLocale}
		identity.Domains = contactSearchDomains(contact.Email, contact.URL, contact.SupportURL, contact.UserPortalURL)
		identity.DisplayName = contactDisplayName(contact)
		if strings.TrimSpace(contact.CompanyName) != "" {
			identity.DisplayNameSource = "contact.companyName"
		} else if identity.DisplayName != "" {
			identity.DisplayNameSource = "contact.personName"
		}
	}
	refs := make(map[string]bool)
	for companyID := range s.companyReferences[id] {
		refs[companyID] = true
	}
	if summary.CompanyID != "" {
		refs[summary.CompanyID] = true
	}
	companies := make(map[string]contactSearchCompanyIdentity)
	companyIDs := make([]string, 0, len(refs))
	for companyID := range refs {
		companyIDs = append(companyIDs, companyID)
	}
	sort.Strings(companyIDs)
	for _, companyID := range companyIDs {
		ref := contactSearchCompanyReference{CompanyID: companyID, Lookup: "not_found", LinkedByContact: summary.CompanyID == companyID}
		if company, ok := s.companies[companyID]; ok {
			ref.Lookup, ref.EntityKind, ref.ContactStatus = "found", company.ContactType, company.ContactStatus
			ref.FromUserDB, ref.DocumentCounter = contactSearchKnown(company.FromUserDB), contactSearchKnown(company.DocumentCounter)
			ref.Created, ref.Modified = company.Created, company.Modified
			ref.MainContactID, ref.UserPreferredContactID = company.MainContactID, company.UserPreferredContactID
			ref.IsMainContact, ref.IsPreferredContact = company.MainContactID == id, company.UserPreferredContactID == id
			companies[companyID] = contactSearchCompanyIdentity{contactSearchCompanyReference: ref, Name: company.CompanyName, Emails: append([]string{}, company.Attributes.Emails...), PhoneNumbers: append([]string{}, company.Attributes.PhoneNumbers...), Websites: append([]string{}, company.Attributes.Websites...), Domains: contactSearchDomains(append(append([]string{}, company.Attributes.Emails...), company.Attributes.Websites...)...)}
			if identity.DisplayName == "" {
				identity.DisplayName, identity.DisplayNameSource = company.CompanyName, "company.companyName"
			}
		} else {
			summary.UnavailableFields = append(summary.UnavailableFields, "linkedCompany")
		}
		summary.CompanyReferences[companyID] = ref
	}
	for _, missing := range []struct{ field, value string }{{"displayName", identity.DisplayName}, {"contact.email", identity.Email}, {"contact.phoneNumber", identity.PhoneNumber}, {"contact.url", identity.URL}} {
		if strings.TrimSpace(missing.value) == "" {
			summary.UnavailableFields = append(summary.UnavailableFields, missing.field)
		}
	}
	if identity.Address == (contactSearchAddress{}) {
		summary.UnavailableFields = append(summary.UnavailableFields, "contact.address")
	}
	if contact == nil {
		summary.UnavailableFields = append(summary.UnavailableFields, "contact.metadata")
	}
	sort.Strings(fields)
	return contactSearchCandidateIdentity{contactSearchCandidateSummary: summary, Identity: identity, CompaniesByID: companies, MatchedFields: fields}
}

func contactSearchFromClient(ctx context.Context, client *fileee.Client, cache *contactSearchCache, in searchContactsInput) (*mcp.CallToolResult, searchContactsOutput, error) {
	in, terms, err := normalizeContactSearch(in)
	if err != nil {
		return nil, searchContactsOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolSearchContacts, err)
	}
	snapshot, cached, err := cache.load(ctx, client, in.Query, in.Refresh)
	if err != nil {
		return nil, searchContactsOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolSearchContacts, err)
	}
	matches, unresolvedMatches := snapshot.matches(terms)
	ids := make([]string, 0, len(matches))
	for id := range matches {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	start := min(in.Start, len(ids))
	end := min(start+in.Limit, len(ids))
	out := searchContactsOutput{Candidates: make(map[string]contactSearchCandidateSummary), CandidateIDs: append([]string{}, ids[start:end]...), TotalMatches: len(ids), Start: in.Start, Limit: in.Limit, Companies: snapshot.companyCoverage, SearchScope: "fileee_company_name_fuzzy_directory_plus_authenticated_me", SelfLookup: snapshot.selfLookup, SelfLookupErrorKind: snapshot.selfLookupErrorKind, CoverageComplete: snapshot.unresolved == 0 && unresolvedMatches == 0 && snapshot.selfLookup == "found", IdentityDetailsComplete: true, UnresolvedCompanyCount: snapshot.unresolved, UnresolvedMatchedCompanyCount: unresolvedMatches, CacheUsed: cached, SnapshotAt: snapshot.loadedAt.UTC().Format(time.RFC3339Nano), CacheExpiresAt: snapshot.loadedAt.Add(contactSearchCacheTTL).UTC().Format(time.RFC3339Nano)}
	out.Contacts.Requested = len(out.CandidateIDs)
	unresolvedCompanies := make(map[string]bool)
	identities := make(map[string]contactSearchCandidateIdentity)
	for _, id := range out.CandidateIDs {
		contact, lookup, kind := snapshot.contact(ctx, client.Contacts, id)
		candidate := snapshot.candidate(id, contact, lookup, kind, matches[id])
		out.Candidates[id], identities[id] = candidate.contactSearchCandidateSummary, candidate
		if lookup != "found" {
			out.IdentityDetailsComplete, out.CoverageComplete = false, false
		}
		if lookup == "found" {
			out.Contacts.Loaded++
		}
		for _, reference := range candidate.CompanyReferences {
			if reference.Lookup != "found" {
				out.IdentityDetailsComplete, out.CoverageComplete = false, false
				unresolvedCompanies[reference.CompanyID] = true
			}
		}
	}
	out.Contacts.Complete = out.Contacts.Requested == out.Contacts.Loaded
	out.UnresolvedCompanyCount += len(unresolvedCompanies)
	out.HasMore = end < len(ids)
	if out.HasMore {
		out.NextStart = &end
	}
	result := &mcp.CallToolResult{}
	if len(identities) > 0 {
		payload, err := json.Marshal(struct {
			ContactsByID map[string]contactSearchCandidateIdentity `json:"contactsById"`
		}{identities})
		if err != nil {
			return nil, searchContactsOutput{}, err
		}
		result, err = wrapUntrustedLines([]string{string(payload)})
		if err != nil {
			return nil, searchContactsOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolSearchContacts, err)
		}
		attachUntrustedStructured(result, &out, &out.UntrustedText)
	}
	return result, out, nil
}

func searchContactsHandler(p *clientpool.Pool, logger *slog.Logger) mcp.ToolHandlerFor[searchContactsInput, searchContactsOutput] {
	cache := newContactSearchCache()
	return func(ctx context.Context, _ *mcp.CallToolRequest, in searchContactsInput) (*mcp.CallToolResult, searchContactsOutput, error) {
		start := time.Now()
		logToolStart(ctx, logger, ToolSearchContacts, slog.Int("start", in.Start), slog.Int("limit", in.Limit), slog.Bool("refresh", in.Refresh))
		if _, _, err := normalizeContactSearch(in); err != nil {
			logToolEnd(ctx, logger, ToolSearchContacts, start, "", 0, err)
			return nil, searchContactsOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolSearchContacts, err)
		}
		client, err := clientFor(ctx, p)
		if err != nil {
			logToolEnd(ctx, logger, ToolSearchContacts, start, "", 0, err)
			return nil, searchContactsOutput{}, err
		}
		result, out, err := contactSearchFromClient(ctx, client, cache, in)
		logToolEnd(ctx, logger, ToolSearchContacts, start, "/api/companies/rest/query", len(out.Candidates), err)
		return result, out, err
	}
}

func registerContactSearchTool(s *mcp.Server, p *clientpool.Pool, logger *slog.Logger) {
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolSearchContacts,
		Description: "Search existing sender and recipient entities by a short person or company name using Fileee's targeted FUZZY company-name query, including managed and user-entered directory matches. " +
			"Includes each matching company's explicit main and user-preferred contact IDs, then loads only the returned candidate page; never scans the shared contacts directory. " +
			"The authenticated ME company/contact is checked separately; a failed supplemental self lookup is reported without hiding valid company matches. " +
			"Returns candidates keyed by contact ID, distinct company IDs, types/status/provenance, contact and company document counters, timestamps and preferred/main roles. " +
			"Names, address, email, phone, websites and domains are untrusted JSON keyed by the same IDs in both response channels. " +
			"start/limit page candidates in contact-ID order (default 20, max 100). Every page of the filtered company-name matches is checked, capped at 1000 matches; a broader query fails explicitly and needs a more distinctive name. " +
			"A per-client, per-query snapshot lasts two minutes; refresh=true reloads catalogues and details. Coverage, missing fields and snapshot age are explicit; an incomplete lookup never proves no existing match. " +
			"Use the returned identity facts to verify the existing sender or recipient. This tool never selects a canonical contact and never creates, renames, merges, assigns or deletes anything.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":   map[string]any{"type": "string", "minLength": 1, "maxLength": 256, "description": "Short person or company name (at most 256 UTF-8 bytes); a distinctive abbreviation is often sufficient."},
				"start":   map[string]any{"type": "integer", "minimum": 0, "default": 0, "description": "Offset into contact-ID-sorted matching candidates, not into the shared contact directory."},
				"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 20},
				"refresh": map[string]any{"type": "boolean", "default": false, "description": "Reload the filtered company-name query and candidate details instead of reusing the two-minute snapshot."},
			},
			"required":             []string{"query"},
			"additionalProperties": false,
		},
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, Title: "Search existing contacts and company references"},
	}, searchContactsHandler(p, logger))
}
