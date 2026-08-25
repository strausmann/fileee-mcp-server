// issued_coverage_test.go ist der Guardrail für die zehn handgeschriebenen
// Lese-Werkzeuge, seit ADR-0019 mit UMGEKEHRTER Standardrichtung
// gegenüber seiner ursprünglichen Aufgabe-5-Fassung: er mountet den
// ECHTEN Server (tools.RegisterAll, einen echten Gangway-Auth-Stack,
// einen echten *issued.Store) und treibt jedes der zehn Werkzeuge über
// einen echten MCP-Client gegen ein Fake-Fileee-Backend — und prüft
// danach, für JEDE zurückgegebene ID, ob sie aufgenommen wurde
// (internal/issued) UND ob sie das nach der seit dem Sicherheits-Audit
// geltenden Linie überhaupt DARF.
//
// Die Linie (ADR-0019, Betreiber-Entscheidung nach dem Sicherheits-Audit,
// verschärft gegenüber der ursprünglichen Aufgabe 4/5-Fassung): NUR ein
// gezielter Einzelabruf — ein Werkzeug, dem der Aufrufer EINE ID nennt
// und das GENAU DIESE eine, vom Server bestätigte Entität liefert — nimmt
// eine ID auf. Ein Werkzeug, das MEHRERE Entitäten liefert, ohne dass der
// Aufrufer sie einzeln genannt hat, nimmt KEINE mehr auf. Auslöser: zwei
// unabhängige Hunter im Sicherheits-Audit fanden, dass ein EINZIGER
// list_documents-Aufruf bis zu 100 IDs aufnahm (Standardgrenze,
// paginierbar bis zum Deckel von 1000 je Identität), sync_documents beim
// ersten Aufruf gleich den kompletten Bestand, list_boxes/get_box
// vollständig ungedeckelt — bei einem Konto mit ein paar hundert
// Dokumenten war danach praktisch jede ID im Konto gültig.
//
// Von den zehn Werkzeugen nehmen seither NUR NOCH get_document und
// get_box eine ID auf — und jeweils NUR die eine, vom Aufrufer per
// Parameter genannte ID, NICHT die zusätzlichen IDs, die als
// Nebenprodukt mitkommen (get_document's TagIDs, get_box's
// DocumentIDs — siehe documentFromService's/boxFromService's eigene
// Doc-Kommentare, read.go/read_boxes.go). Die übrigen acht (list_documents,
// search_documents, sync_documents, list_document_conversations,
// list_boxes, get_document_pdf, get_page_image, get_page_ocr) nehmen GAR
// NICHTS mehr auf — die letzten drei taten das ohnehin nie (siehe
// read_binary.go's eigener Doc-Kommentar, unverändert durch ADR-0019).
//
// Umfang — genau die zehn Werkzeuge, die diese Datei schon vor ADR-0019
// prüfte: die fünf Dokument-Werkzeuge (read.go: list_documents,
// search_documents, get_document, sync_documents,
// list_document_conversations), list_boxes/get_box (read_boxes.go), und
// die drei Binär-/OCR-Werkzeuge (read_binary.go: get_document_pdf,
// get_page_image, get_page_ocr). Die rund 21 generischen
// Deskriptor-Werkzeuge (read_generic.go/read_sync.go, angemeldet über
// registerReferenceTools/registerPeopleTools/registerSyncTools) haben
// bereits ihren EIGENEN Guardrail — read_generic_test.go — der seit
// ADR-0019 dieselbe Umkehrung trägt: TestAlleGenerischenDeskriptorenHabenEinIDOf
// bleibt unverändert, TestGetFromServiceMerktDenGezieltenEinzelabrufListFromServiceMerktNichts
// (vormals TestGenericHandlerMerktAusgelieferteIDs) prüft jetzt beide
// Richtungen (Get erfasst, List erfasst NICHT). Die fünf Ops-/Meta-Werkzeuge
// (get_runtime_stats, get_tool_manifest, self_check, whoami,
// get_account_status) liefern gar keine fileee-eigene Entitäts-ID aus
// (RegisterAll's eigener Doc-Kommentar; ops.go's Paket-Doc-Kommentar für
// die ersten vier, read_account.go's eigener für das fünfte). Diese 26
// Werkzeuge hier nochmal mit vollen Fixtures abzudecken würde den
// Guardrail nicht verstärken — es würde diese Datei nur größer machen,
// ohne einen Fehler zu fangen, für den diese Datei zuständig ist. Und
// Aufgabe 4's eigener Bericht fand genau in diesem generischen Pfad eine
// Restlücke (contactSummary.CompanyID/reminderSummary.DocumentID,
// read_people.go, beide AUSSERHALB des Dateiumfangs dieser Datei) —
// ein blindes "hier auch alles mitprüfen" hätte diese Datei gezwungen,
// darüber hinwegzusehen oder sie stillschweigend zu akzeptieren.
//
// TestAlleLeseWerkzeugeSindEinsortiert weiter unten ist es, was diese
// Umfangsentscheidung vor stillschweigendem Veralten schützt: er geht
// über die ECHTE, laufende Werkzeugliste (session.Tools) und schlägt
// laut fehl, sobald der Name eines Lese-Werkzeugs weder zu den zehn
// gehört, die diese Datei prüft, noch zu den generischen Deskriptor-
// Werkzeugen mit gesetztem IDOf, noch zu den fünf entitätslosen
// Ops-/Konto-Werkzeugen — ein künftiges handgeschriebenes Werkzeug, das
// sowohl seine Erfassungs-Entscheidung als auch einen Eintrag hier
// vergisst, kann nicht unbemerkt durchrutschen; es schlägt bei seinem
// Erscheinen fehl. Dieser Test prüft AUSSCHLIESSLICH die Einsortierung,
// nicht die Erfassungsrichtung — er blieb durch ADR-0019 unverändert.
//
// WICHTIG (Fix nach Review-Fund, siehe TestAlleLeseWerkzeugeSindEinsortiert's
// eigener Doc-Kommentar für die volle Geschichte): die Zugehörigkeit zu
// den rund 21 generischen Deskriptor-Werkzeugen wird NICHT mehr über
// eine zweite, handgetippte Namensliste geprüft — eine solche Liste
// akzeptierte jeden dort eingetragenen Namen blind, auch einen
// generischen Deskriptor mit kaputtem (nil) IDOf, was den gesamten
// Serverprozess beim ersten echten Aufruf abstürzen lässt (kein
// recover() im SDK-Dispatch-Pfad, Issue #70). Stattdessen wird die
// Zugehörigkeit aus tools.GenericReadToolStatus() (generic_ids_export_test.go,
// package tools) ABGELEITET — aus denselben 14 Deskriptor-Konstruktoren,
// die Aufgabe 4s eigener TestAlleGenerischenDeskriptorenHabenEinIDOf
// bereits einzeln prüft, nicht aus einer zweiten, unabhängig gepflegten
// Liste.
//
// Bewusste "Nein"-Entscheidungen, die diese Datei direkt prüft (nicht nur
// in Prosa behauptet) — siehe TestJedesWerkzeugDasIDsAusliefertMerktSieAuch,
// TestSyncDocumentsMerktWederGeaenderteNochGeloeschteIDs (vormals
// TestSyncDocumentsMerktGeaenderteAberNichtGeloeschteIDs) und
// TestGetPageOcrMerktDieTokenKennungNicht:
//
//   - list_documents/search_documents/sync_documents/
//     list_document_conversations/list_boxes nehmen SEIT ADR-0019 GAR
//     KEINE der zurückgegebenen IDs mehr auf — mehrere Entitäten, keine
//     davon vom Aufrufer einzeln genannt (siehe listFromService's eigenen
//     Doc-Kommentar, read_generic.go, für die volle Begründung).
//   - get_document's TagIDs und get_box's DocumentIDs werden NICHT
//     aufgenommen — nur die eine, vom Aufrufer per ID angeforderte
//     Entität selbst (documentFromService's/boxFromService's eigene
//     Doc-Kommentare, read.go/read_boxes.go).
//   - sync_documents' DeletedIDs werden NIE aufgenommen — ein gelöschtes
//     Dokument ist für kein späteres Werkzeug mehr ein gültiges Ziel; seit
//     ADR-0019 gilt das trivial mit, weil sync_documents inzwischen GAR
//     KEINE ID mehr aufnimmt, geänderte so wenig wie gelöschte.
//   - get_page_ocr's WebappID je Token (json "webappId") wird NIE
//     aufgenommen — sie sieht aus wie eine fileee-eigene ID (und ist
//     eine, siehe ocrTokenPosition's eigener Doc-Kommentar), aber kein
//     Werkzeug dieses Servers nimmt eine OCR-Token-Kennung überhaupt als
//     Parameter entgegen — eine Aufnahme könnte also nie gegen irgendwas
//     geprüft werden (read_binary.go's eigener Doc-Kommentar trägt
//     dieselbe Begründung). Unverändert durch ADR-0019.
package tools_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/gangway/identity/testidp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/accounts"
	"github.com/strausmann/fileee-mcp-server/internal/issued"
	"github.com/strausmann/fileee-mcp-server/internal/tools"
)

// --- Fixture-Kennungen ------------------------------------------------------
//
// Jede Kennung unten ist ein Literal, das diese Datei von Anfang bis
// Ende selbst kontrolliert: das Fake-Backend liefert sie aus, der
// Werkzeugaufruf soll sie aufnehmen (oder, bei den beiden
// "Nein"-Kennungen, ausdrücklich NICHT), und die Prüfung fragt genau
// dasselbe Literal ab — keine Kennung wird je aus einer vorherigen
// Antwort zurückgelesen, das würde einen Fehler im Antwort-Parsing einen
// Fehler im Aufnahme-Pfad verdecken lassen.
const (
	covDocument           = "cov-document-1"
	covDocumentSynced     = "cov-document-synced-1"  // Nein (seit ADR-0019): sync_documents darf auch die geänderte Zeile NICHT aufnehmen
	covDocumentDeleted    = "cov-document-deleted-1" // Nein: sync_documents darf das NICHT aufnehmen
	covBox                = "cov-box-1"
	covConversation       = "cov-conversation-1"
	covPage               = "cov-page-1"
	covOCRToken           = "cov-ocrtoken-1" // Nein: get_page_ocr darf das NICHT aufnehmen
	covCoverageIdentity   = "coverage-alice"
	covCoverageAccountKey = "coverage-alice@example.invalid"
)

// newCoverageServer startet einen httptest.Server, der genau die
// Fileee-Endpunkte beantwortet, die die zehn Werkzeuge dieser Datei im
// Umfang erreichen — den Login-Handshake (spiegelt newIsolationServer's
// eigene vier Routen, read_test.go) plus eine feste Antwort je Endpunkt,
// benannt nach go-fileee's eigener resourcePath-Konvention
// (restService[T]{resourcePath: "..."},
// go-fileee/fileee/{documents,boxes,conversations}.go) — gegen den
// tatsächlichen go-fileee-v0.2.0-Quellcode geprüft, nicht angenommen.
// Ein einziges festes Konto reicht hier (anders als bei
// newIsolationServer's Mehrkonten-Isolationstests): jede Route
// antwortet bedingungslos, Session-Cookies werden nie geprüft.
func newCoverageServer(t *testing.T) string {
	t.Helper()

	mux := http.NewServeMux()

	// --- Login-Handshake (identisch zu newIsolationServer's eigenen vier Routen) ---
	mux.HandleFunc("GET /api/f/start", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/f/existent", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"existent":true,"twoFactorAuthEnabled":false}`))
	})
	mux.HandleFunc("POST /api/f/login", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "sess-coverage"})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"loggedIn":true}`))
	})
	mux.HandleFunc("GET /api/f/user-session", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"authorized":true,"secondsBlocked":0}`))
	})

	// --- Dokumente: list_documents/search_documents (Query), get_document (Get),
	//     sync_documents (Diff) ---
	mux.HandleFunc("POST /api/documents/rest/query", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"rows":[{"id":"`+covDocument+`","status":"DONE","attributes":{"data":{}}}],"totalRows":1}`)
	})
	mux.HandleFunc("GET /api/documents/rest/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		writeJSON(w, `{"id":"`+id+`","status":"DONE","attributes":{"data":{`+
			`"title":{"value":"Testdokument"},"tagIds":{"value":["`+covTag+`"]}}}}`)
	})
	mux.HandleFunc("POST /api/documents/rest/diff", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"rows":[{"id":"`+covDocumentSynced+`","status":"DONE","attributes":{"data":{}}}],`+
			`"idsToDelete":["`+covDocumentDeleted+`"],"totalRows":1}`)
	})

	// --- Konversationen: list_document_conversations (Documents.Conversations,
	//     ruft selbst Conversations.Diff auf, go-fileee/fileee/conversations.go) ---
	mux.HandleFunc("POST /api/conversations/rest/diff", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"rows":[{"id":"`+covConversation+`","conversationType":"SHARE","kind":"DIRECT",`+
			`"participants":[],"state":{"sharedDocumentIds":["`+covDocument+`"]},"version":1}],`+
			`"idsToDelete":[],"totalRows":1}`)
	})

	// --- Boxen: list_boxes (List, ruft selbst Diff auf), get_box (Get) ---
	mux.HandleFunc("POST /api/fileeeboxes/rest/diff", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"rows":[{"id":"`+covBox+`","boxNr":1,"boxName":"Steuerunterlagen",`+
			`"documents":[{"documentId":"`+covDocument+`","pageCount":1,"modified":""}],"version":1}],`+
			`"idsToDelete":[],"totalRows":1}`)
	})
	mux.HandleFunc("GET /api/fileeeboxes/rest/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		writeJSON(w, `{"id":"`+id+`","boxNr":1,"boxName":"Steuerunterlagen",`+
			`"documents":[{"documentId":"`+covDocument+`","pageCount":1,"modified":""}],"version":1}`)
	})

	// --- Binär/OCR: get_document_pdf, get_page_image, get_page_ocr ---
	mux.HandleFunc("GET /api/v1/documents/{id}/pdf", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("%PDF-1.4 Fixture für die Guardrail-Abdeckung"))
	})
	mux.HandleFunc("GET /api/v1/pages/{id}/image", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Bild-Bytes als Fixture für die Guardrail-Abdeckung"))
	})
	mux.HandleFunc("GET /api/pages/{id}", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `[{"webappId":"`+covOCRToken+`","text":"erkannter Text","left":0,"top":0,`+
			`"right":1,"bottom":1,"width":1,"height":1}]`)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// covTag ist eine Fixture-Kennung für get_document's TagIDs — getrennt
// vom obigen const-Block deklariert, weil sie ein eigenes, drittes Muster
// ist: sie steht in get_document's StructuredContent (out.TagIDs), wird
// aber NICHT vom Aufrufer per Parameter genannt — anders als covDocument
// (die per get_document(id) angeforderte ID selbst, MUSS aufgenommen
// werden) und wie covDocumentDeleted/covOCRToken (dürfen NICHT
// aufgenommen werden). Seit ADR-0019 (Betreiber-Entscheidung nach dem
// Sicherheits-Audit: "erfasst wird nur die ID, die der Aufrufer im
// Parameter genannt hat") gehört covTag deshalb faktisch zu den
// "Nein"-Kennungen — documentFromService's eigener Doc-Kommentar
// (read.go) begründet das ausführlich.
const covTag = "cov-tag-1"

// writeJSON schreibt body als application/json-Antwort mit Status 200 —
// der eigene, kleine Helfer dieser Datei, da weder newIsolationServer
// noch newErrorServer (read_test.go) einen festen Antworttext ohne die
// dort mitgelieferte Konto-/Status-Verzweigung schreiben, die diese
// Datei nicht braucht.
func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// handWrittenReadTools sind genau die zehn Werkzeuge, für die diese
// Aufgabe (Aufgabe 5) die Aufnahme verantwortet — benannt über die
// exportierten Konstanten, die names.go bereits definiert, nie über ein
// rohes String-Literal, damit eine Umbenennung einer dieser Konstanten
// hier ein Kompilierfehler wird statt eines still veralteten Eintrags.
var handWrittenReadTools = map[string]bool{
	tools.ToolListDocuments:             true,
	tools.ToolSearchDocuments:           true,
	tools.ToolGetDocument:               true,
	tools.ToolSyncDocuments:             true,
	tools.ToolListDocumentConversations: true,
	tools.ToolListBoxes:                 true,
	tools.ToolGetBox:                    true,
	tools.ToolGetDocumentPDF:            true,
	tools.ToolGetPageImage:              true,
	tools.ToolGetPageOCR:                true,
}

// knownNonEntityReadTools sind die fünf Werkzeuge, die überhaupt KEINE
// fileee-eigene Entitäts-ID ausliefern und NICHT über den generischen
// Deskriptor-Pfad laufen — die vier Ops-/Meta-Werkzeuge (ops.go, fassen
// fileee-Daten gar nicht an) plus get_account_status (read_account.go,
// AccountTypeID ist ein Abo-Plan-Code, kein Verweis auf etwas, das ein
// anderes Werkzeug anfassen könnte). Für DIESE fünf ist eine kleine,
// handgepflegte Liste unproblematisch: sie laufen nicht über
// registerReadService/registerSync und tragen deshalb nicht das Risiko,
// das knownOutOfScopeReadTools früher hatte (siehe TestAlleLeseWerkzeugeSindEinsortiert's
// eigener Doc-Kommentar) — es gibt kein IDOf, das hier je nil sein
// könnte, also auch keinen Riss, den eine Ableitung schließen müsste.
//
// Die rund 21 generischen Deskriptor-Werkzeuge stehen bewusst NICHT
// mehr in einer Liste dieser Art — ihre Mitgliedschaft wird jetzt aus
// tools.GenericReadToolStatus() abgeleitet (siehe
// TestAlleLeseWerkzeugeSindEinsortiert).
var knownNonEntityReadTools = map[string]bool{
	tools.ToolGetRuntimeStats: true, tools.ToolGetToolManifest: true,
	tools.ToolSelfCheck: true, tools.ToolWhoami: true,
	tools.ToolGetAccountStatus: true,
}

// knownWriteTools sind die acht heute gemounteten Schreib-Werkzeuge
// (write.go: update_contact/create_contact; write_people.go:
// create_reminder/update_reminder; write_boxes.go:
// box_add_document/box_remove_document; write_documents.go:
// upload_document/update_document). Ihre eigene rec.Record-Verdrahtung
// ist bewusst NICHT Gegenstand dieses Guardrails — das ist die
// ausdrücklich offene, entschiedene Lücke aus Issue #74, kein
// vergessener Fall. Diese Liste bleibt trotzdem hier eingetragen, statt
// wie tools.GenericReadToolStatus() abgeleitet zu werden, und zwar aus
// genau dem Grund, der TestAlleLeseWerkzeugeSindEinsortiert unten
// unverändert lässt: ein KÜNFTIGES Schreib-Werkzeug, das seine eigene
// rec.Record-Verdrahtung UND seinen Annotations-Eintrag vergisst, muss
// weiterhin explizit hier eingetragen werden, um zu bestehen — fehlt der
// Eintrag, schlägt der Test unten fehl und nennt den Namen, statt still
// "außerhalb des Umfangs" zu übergehen. Eine aus ReadOnlyHint abgeleitete
// Mitgliedschaft (etwa "ReadOnlyHint == false → Schreib-Werkzeug, also
// unproblematisch") würde genau die Annahme wiederholen, die den Fund
// dieser Aufgabe ausgemacht hat: eine vergessene Annotation wird dann
// zur stillschweigenden Einordnung statt zum Fehlschlag. Diese Liste
// verschwindet, sobald Issue #74 umgesetzt ist und die Schreib-Werkzeuge
// eine eigene rec.Record-Verdrahtung plus eigene Fixtures/Zusicherungen
// bekommen — dann werden sie aus dieser Handliste heraus in
// handWrittenReadTools-artige Prüfungen überführt, nicht länger nur
// zugelassen.
var knownWriteTools = map[string]bool{
	tools.ToolUpdateContact:     true,
	tools.ToolCreateContact:     true,
	tools.ToolCreateReminder:    true,
	tools.ToolUpdateReminder:    true,
	tools.ToolBoxAddDocument:    true,
	tools.ToolBoxRemoveDocument: true,
	tools.ToolUploadDocument:    true,
	tools.ToolUpdateDocument:    true,
}

// newCoverageSession mountet den echten, mit RegisterAll angemeldeten
// Server (mit einem echten *issued.Store), einen echten Gangway-Auth-
// Stack und ein Fake-Fileee-Backend (newCoverageServer) und verbindet
// dann eine authentifizierte MCP-Client-Sitzung als covCoverageIdentity.
// Zusätzlich mountet sie ein Wegwerf-Werkzeug "capture" auf demselben
// *mcp.Server und ruft es einmal über dieselbe Sitzung auf, um ein
// echtes, Gangway-verifiziertes ctx zu dieser Identität zu bekommen —
// der einzige Weg, rec.Check aus diesem externen Testpaket heraus mit
// dem richtigen Subject aufzurufen, da serve.IdentityFrom(ctx) nur für
// ein ctx wahr liefert, das Gangways eigene Middleware erzeugt hat (kein
// exportierter Weg, eines zu fälschen — dieselbe Begründung, die
// read_generic_test.go's ctxMitIdentitaet und
// internal/server/issued_wiring_test.go's authenticatedCtx jeweils für
// sich selbst festhalten).
func newCoverageSession(t *testing.T) (*mcp.ClientSession, *issued.Store, context.Context) {
	t.Helper()

	srv := newCoverageServer(t)
	pool := testPool(t, srv, accounts.NewMulti(map[string]fileee.Credentials{
		covCoverageIdentity: {Username: covCoverageAccountKey, Password: "pw-coverage"},
	}))
	rec := issued.New(time.Hour, 1000)

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "issued-coverage-test", Version: "0.0.0"}, nil)
	captured := make(chan context.Context, 1)
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "capture"},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			captured <- ctx
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
		})
	tools.RegisterAll(mcpServer, pool, tools.ServerInfo{Mode: "single"}, testLogger(t), rec)

	idp := testidp.New(t)
	httpSrv := newGangwayServer(t, idp, mcpServer)
	session := connectAs(t, httpSrv, idp, covCoverageIdentity)

	if res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "capture"}); err != nil || res.IsError {
		t.Fatalf("newCoverageSession: Aufruf von capture fehlgeschlagen: err=%v, res=%+v", err, res)
	}
	identityCtx := <-captured

	return session, rec, identityCtx
}

// structuredContentOf ruft tool auf session mit args auf, lässt den Test
// fehlschlagen, wenn der Aufruf selbst einen Fehler liefert, und gibt
// StructuredContent als map[string]any zurück — genau die Form, die ein
// echter MCP-Client tatsächlich sieht (ein rohes JSON-Objekt, generisch
// dekodiert), nicht das konkrete Go-Struct des Servers; diese Datei
// treibt bewusst dieselbe, für den Client sichtbare Form, die jeder
// echte Aufrufer dieses Servers bekommt, keine interne Abkürzung.
func structuredContentOf(t *testing.T, session *mcp.ClientSession, tool string, args map[string]any) map[string]any {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", tool, err)
	}
	if res.IsError {
		t.Fatalf("CallTool(%s): IsError = true, content = %+v", tool, res.Content)
	}
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("CallTool(%s): StructuredContent = %T, want map[string]any (bekam %+v)", tool, res.StructuredContent, res.StructuredContent)
	}
	return sc
}

// stringsAt liest sc[key] und liefert es als []string zurück —
// unabhängig davon, ob der JSON-Wert eine einzelne Zeichenkette oder ein
// Array von Zeichenketten war. Beide Formen kommen über die zehn
// Werkzeuge dieser Datei hinweg vor (z. B. getDocumentOutput's "id" ist
// eine einzelne Zeichenkette, ihr "tagIds" ist ein Array).
func stringsAt(t *testing.T, sc map[string]any, key string) []string {
	t.Helper()
	v, ok := sc[key]
	if !ok || v == nil {
		return nil
	}
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				t.Fatalf("stringsAt(%q): Element %v ist %T, erwartet string", key, e, e)
			}
			out = append(out, s)
		}
		return out
	default:
		t.Fatalf("stringsAt(%q): Wert %v ist %T, erwartet string oder []any", key, v, v)
		return nil
	}
}

// sliceOfMapStringsAt liest sc[key] als ein []any von map[string]any
// (z. B. listDocumentsOutput's "documents" oder listBoxesOutput's
// "boxes") und liefert childKey aus jedem Element über stringsAt
// gelesen, abgeflacht, zurück.
func sliceOfMapStringsAt(t *testing.T, sc map[string]any, key, childKey string) []string {
	t.Helper()
	v, ok := sc[key]
	if !ok || v == nil {
		return nil
	}
	items, ok := v.([]any)
	if !ok {
		t.Fatalf("sliceOfMapStringsAt(%q): Wert %v ist %T, erwartet []any", key, v, v)
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			t.Fatalf("sliceOfMapStringsAt(%q): Element %v ist %T, erwartet map[string]any", key, it, it)
		}
		out = append(out, stringsAt(t, m, childKey)...)
	}
	return out
}

// assertAllIssued lässt den Test fehlschlagen, sofern rec.Check(identityCtx, id)
// nicht für jede Kennung in ids nil liefert.
func assertAllIssued(t *testing.T, rec *issued.Store, identityCtx context.Context, tool string, ids []string) {
	t.Helper()
	if len(ids) == 0 {
		t.Fatalf("%s: keine Kennungen zu prüfen — die eigene Fixture dieses Tests lieferte keine, Fixture korrigieren", tool)
	}
	for _, id := range ids {
		if err := rec.Check(identityCtx, id); err != nil {
			t.Errorf("%s: rec.Check(%q) = %v, erwartet nil — diese Kennung stand in StructuredContent, wurde aber nie aufgenommen", tool, id, err)
		}
	}
}

// assertNoneIssued lässt den Test fehlschlagen, sofern rec.Check(identityCtx, id)
// nicht für jede Kennung in ids einen Fehler (ErrNotIssued) liefert —
// die "Nein"-Hälfte der Zusicherungen dieser Datei.
func assertNoneIssued(t *testing.T, rec *issued.Store, identityCtx context.Context, tool string, ids []string) {
	t.Helper()
	for _, id := range ids {
		if err := rec.Check(identityCtx, id); err == nil {
			t.Errorf("%s: rec.Check(%q) = nil, erwartet einen Fehler — diese Kennung darf NICHT aufgenommen werden (siehe eigener Paket-Doc-Kommentar dieser Datei)", tool, id)
		}
	}
}

// TestAlleLeseWerkzeugeSindEinsortiert leitet die lebende Menge ALLER
// gemounteten Werkzeuge aus dem ECHTEN Server ab (session.Tools) und
// prüft JEDEN Namen — unabhängig von seinen Annotations — gegen VIER
// Quellen, in dieser Reihenfolge:
//
//  1. tools.GenericReadToolStatus() — ABGELEITET aus den 14 echten
//     Deskriptor-Konstruktoren (generic_ids_export_test.go), nicht
//     getippt. Steht der Name dort mit true: erledigt, von Aufgabe 4s
//     eigenem Guardrail (read_generic_test.go) abgedeckt. Steht er dort
//     mit false: harter Fehlschlag MIT SPEZIFISCHER MELDUNG — dieser
//     generische Deskriptor hat ein nil IDOf, das ist im Deskriptor zu
//     reparieren, nicht hier einzutragen.
//  2. handWrittenReadTools — die zehn Werkzeuge dieser Aufgabe, mit
//     eigenen Fixtures unten geprüft.
//  3. knownNonEntityReadTools — die fünf Ops-/Konto-Werkzeuge ohne jede
//     Entitäts-ID (siehe deren eigenen Doc-Kommentar, warum eine kleine
//     Handliste dafür unproblematisch ist).
//  4. knownWriteTools — die acht heute existierenden Schreib-Werkzeuge
//     (siehe deren eigenen Doc-Kommentar) — bewusst NICHT über
//     rec.Record geprüft (Issue #74, offen), aber trotzdem NAMENTLICH
//     eingetragen, damit ein künftiges Schreib-Werkzeug hier ebenso
//     hart auffällt wie ein künftiges Lese-Werkzeug.
//
// Ein Name, der in KEINER der vier Quellen auftaucht, ist ein
// unbekanntes, neu aufgetauchtes Werkzeug — harter Fehlschlag, kein
// stilles Überspringen.
//
// WICHTIG (Sicherheitsaudit-Fund, zweiter Riss nach demselben Muster
// wie der knownOutOfScopeReadTools-Fund unten): die frühere Fassung
// filterte VOR der Einsortierung auf
// "tool.Annotations == nil || !tool.Annotations.ReadOnlyHint { continue }"
// — sie prüfte also nur Werkzeuge, die bereits korrekt als lesend
// annotiert waren, und übersprang jedes andere stillschweigend als
// "außerhalb des Umfangs". Ein KÜNFTIGES Lese-Werkzeug, das eine
// fileee-eigene ID ausliefert, aber sein ReadOnlyHint:true vergisst,
// wäre damit nie geprüft worden — genau die Annahme, die dieser Test
// eigentlich verhindern soll (ein vergessenes Annotation-Feld darf
// niemals zu "gilt als erledigt" führen, sondern muss zu einem
// Fehlschlag führen). Reproduziert: ein Werkzeug, das eine ID
// ausliefert, nichts aufnimmt und ReadOnlyHint auslässt, blieb bei
// BEIDEN Guardrail-Tests grün. Der Fix entfernt den Annotations-Filter
// komplett — JEDES gemountete Werkzeug muss jetzt in einer der vier
// Quellen stehen, unabhängig davon, was seine Annotations behaupten;
// ein Werkzeug ohne Annotations oder mit falschem ReadOnlyHint fällt
// dadurch automatisch in "keine der vier Quellen" und schlägt fehl,
// statt übersprungen zu werden. TestEveryMountedToolHasAnnotations
// (descriptions_test.go) bleibt die separate, unabhängige Prüfung, dass
// jedes Werkzeug überhaupt Annotations trägt — beide Tests widersprechen
// sich nicht: dieser hier braucht Annotations gar nicht mehr, um jedes
// Werkzeug zu erfassen.
//
// Vorher prüfte dieser Test Punkt 1 über eine zweite, unabhängig
// getippte Namensliste (knownOutOfScopeReadTools) — die akzeptierte
// JEDEN dort eingetragenen Namen, unabhängig davon, ob er zu einem
// echten, gesund verdrahteten Deskriptor gehörte. Ein Review fand und
// reproduzierte den Riss real: ein generischer Deskriptor mit IDOf: nil,
// gemountet und in diese Liste eingetragen — genau der Weg, den die
// alte Fehlermeldung selbst vorschlug — blieb hier UND in Aufgabe 4s
// eigenem TestAlleGenerischenDeskriptorenHabenEinIDOf unbemerkt grün,
// bis der erste echte Aufruf mit einer Nil-Pointer-Panik in
// read_generic.go:444 (d.IDOf(&entry)) den GESAMTEN Serverprozess
// mitriss (kein recover() im gesamten SDK-Dispatch-Pfad, Issue #70) —
// nicht nur den aufrufenden Request. Siehe generic_ids_export_test.go's
// eigenen Doc-Kommentar für die vollständige Begründung des Fixes.
func TestAlleLeseWerkzeugeSindEinsortiert(t *testing.T) {
	session, _, _ := newCoverageSession(t)
	genericStatus := tools.GenericReadToolStatus()

	for tool, err := range session.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatalf("session.Tools: %v", err)
		}
		if tool.Name == "capture" {
			// newCoverageSession's eigenes Wegwerf-Werkzeug (siehe dessen
			// Doc-Kommentar) — kein Produkt von tools.RegisterAll, nur ein
			// Hilfsmittel dieser Testdatei selbst, um an ein echtes,
			// Gangway-verifiziertes ctx zu kommen. Es liefert nie eine
			// fileee-eigene ID und gehört deshalb in keine der vier
			// Produktions-Quellen — anders als jedes ECHTE Werkzeug bleibt
			// es hier per Namensvergleich übersprungen, nicht per
			// Annotations-Filter (der wäre wieder genau die Lücke, die
			// dieser Test jetzt schließt).
			continue
		}
		if idOf, isGeneric := genericStatus[tool.Name]; isGeneric {
			if !idOf {
				t.Errorf("Werkzeug %q läuft über einen generischen Deskriptor "+
					"(registerReadService/registerSync), dessen IDOf-Feld nil ist — das gehört im "+
					"jeweiligen Deskriptor-Konstruktor gesetzt (siehe readServiceDescriptor.IDOf/"+
					"syncDescriptor.IDOf, read_generic.go/read_sync.go), NICHT hier irgendwo "+
					"eingetragen. Ein gemountetes Werkzeug mit nil IDOf stürzt beim ersten echten "+
					"Aufruf den gesamten Serverprozess ab (read_generic.go:444, kein recover() im "+
					"SDK-Dispatch-Pfad, Issue #70)", tool.Name)
			}
			continue
		}
		if handWrittenReadTools[tool.Name] {
			continue
		}
		if knownNonEntityReadTools[tool.Name] {
			continue
		}
		if knownWriteTools[tool.Name] {
			continue
		}
		t.Errorf("Werkzeug %q ist in keiner der vier bekannten Quellen (tools.GenericReadToolStatus, "+
			"handWrittenReadTools, knownNonEntityReadTools, knownWriteTools) — ein neues, ungetriagtes "+
			"Werkzeug ist aufgetaucht (Annotations spielen für diese Prüfung KEINE Rolle mehr — auch "+
			"ein Werkzeug ohne oder mit falschem ReadOnlyHint muss hier einsortiert sein). Entscheiden: "+
			"läuft es über registerReadService/registerSync mit gesetztem IDOf — dann gehört es "+
			"automatisch zu tools.GenericReadToolStatus(), nichts hier einzutragen, nur prüfen, dass "+
			"IDOf im Deskriptor gesetzt ist — oder ist es ein handgeschriebener LESE-Handler, der eine "+
			"eigene rec.Record-Verdrahtung UND eine eigene Fixture/Zusicherung in dieser Datei braucht "+
			"— dann in handWrittenReadTools eintragen und unten mitprüfen — oder liefert es "+
			"nachweislich keine fileee-eigene Entitäts-ID — dann in knownNonEntityReadTools eintragen "+
			"— oder ist es ein SCHREIB-Werkzeug — dann in knownWriteTools eintragen (siehe dessen "+
			"eigenen Doc-Kommentar zu Issue #74)",
			tool.Name)
	}
}

// TestJedesWerkzeugDasIDsAusliefertMerktSieAuch treibt alle zehn
// Werkzeuge im Umfang in getrennten Sitzungen und prüft für JEDE
// zurückgegebene Kennung, ob sie aufgenommen wurde oder nicht — je
// nachdem, ob sie die eine, vom Aufrufer per Parameter genannte ID ist
// (MUSS aufgenommen sein, ADR-0019) oder eine von mehreren Entitäten
// bzw. ein Nebenprodukt-Feld, das der Aufrufer nie einzeln genannt hat
// (DARF NICHT aufgenommen sein). Nur get_document und get_box haben
// beide Hälften gleichzeitig (ihre eigene ID MUSS, ihre Nebenprodukt-IDs
// DÜRFEN NICHT); die übrigen acht Werkzeuge im Umfang haben ausschließlich
// die "darf nicht"-Hälfte (list_documents/search_documents/
// sync_documents/list_document_conversations/list_boxes: mehrere
// Entitäten, gar nichts wird aufgenommen) oder gar keine Kennung
// (get_document_pdf/get_page_image, stillschweigend in Ordnung — nichts
// zu prüfen). sync_documents' DeletedIDs und get_page_ocr's WebappID
// haben ihre EIGENEN, dedizierten Tests weiter unten.
//
// Vor ADR-0019 hieß dieser Test dasselbe und prüfte GENAU DAS GEGENTEIL
// für sechs der acht "darf nicht"-Fälle (list_documents/search_documents/
// sync_documents/list_document_conversations/list_boxes mussten
// aufnehmen; get_document/get_box mussten auch ihre Nebenprodukt-IDs
// aufnehmen) — siehe Git-Historie für die alte Fassung.
func TestJedesWerkzeugDasIDsAusliefertMerktSieAuch(t *testing.T) {
	// WICHTIG (Fix nach Review-Fund, Befund 1, weiterhin gültig nach
	// ADR-0019): JEDER Subtest ruft newCoverageSession(t) SELBST auf und
	// bekommt damit eine frische Sitzung + einen frischen *issued.Store.
	// Der frühere Aufbau rief newCoverageSession(t) einmal oben auf und
	// teilte Sitzung/Store über alle Subtests — dieselben
	// Fixture-Kennungen (covDocument für list_documents/search_documents/
	// get_document, covBox für list_boxes/get_box) tauchen in mehreren
	// Werkzeugen auf, und ein früherer Subtest hatte die Kennung dadurch
	// schon aufgenommen, bevor der eigentlich zu prüfende Subtest
	// überhaupt lief — assertAllIssued wurde dann aus dem falschen Grund
	// grün. Per Gegenversuch belegt: rec.Record(...) komplett aus
	// searchDocumentsHandler (read.go) bzw. boxFromService (read_boxes.go)
	// entfernt, das ganze Paket blieb trotzdem grün. Mit einer frischen
	// Sitzung je Subtest kann eine fehlende (oder, seit ADR-0019, eine
	// FÄLSCHLICH VORHANDENE) Record-Verdrahtung nicht mehr hinter einem
	// fremden Subtest verschwinden — dasselbe Argument gilt jetzt in
	// BEIDE Richtungen, nicht mehr nur für die positive.
	t.Run("list_documents", func(t *testing.T) {
		session, rec, identityCtx := newCoverageSession(t)
		sc := structuredContentOf(t, session, tools.ToolListDocuments, nil)
		ids := sliceOfMapStringsAt(t, sc, "documents", "id")
		assertNoneIssued(t, rec, identityCtx, tools.ToolListDocuments, ids)
	})

	t.Run("search_documents", func(t *testing.T) {
		session, rec, identityCtx := newCoverageSession(t)
		sc := structuredContentOf(t, session, tools.ToolSearchDocuments, map[string]any{"term": "invoice"})
		ids := stringsAt(t, sc, "ids")
		assertNoneIssued(t, rec, identityCtx, tools.ToolSearchDocuments, ids)
	})

	t.Run("get_document", func(t *testing.T) {
		session, rec, identityCtx := newCoverageSession(t)
		sc := structuredContentOf(t, session, tools.ToolGetDocument, map[string]any{"id": covDocument})
		ids := stringsAt(t, sc, "id")
		assertAllIssued(t, rec, identityCtx, tools.ToolGetDocument, ids)
		tagIDs := stringsAt(t, sc, "tagIds")
		assertNoneIssued(t, rec, identityCtx, tools.ToolGetDocument+" (tagIds, Nebenprodukt — nie vom Aufrufer genannt)", tagIDs)
	})

	t.Run("sync_documents", func(t *testing.T) {
		session, rec, identityCtx := newCoverageSession(t)
		sc := structuredContentOf(t, session, tools.ToolSyncDocuments, nil)
		ids := sliceOfMapStringsAt(t, sc, "entries", "id")
		assertNoneIssued(t, rec, identityCtx, tools.ToolSyncDocuments, ids)
	})

	t.Run("list_document_conversations", func(t *testing.T) {
		session, rec, identityCtx := newCoverageSession(t)
		sc := structuredContentOf(t, session, tools.ToolListDocumentConversations, map[string]any{"documentId": covDocument})
		ids := sliceOfMapStringsAt(t, sc, "conversations", "id")
		assertNoneIssued(t, rec, identityCtx, tools.ToolListDocumentConversations, ids)
	})

	t.Run("list_boxes", func(t *testing.T) {
		session, rec, identityCtx := newCoverageSession(t)
		sc := structuredContentOf(t, session, tools.ToolListBoxes, nil)
		ids := sliceOfMapStringsAt(t, sc, "boxes", "id")
		assertNoneIssued(t, rec, identityCtx, tools.ToolListBoxes, ids)
	})

	t.Run("get_box", func(t *testing.T) {
		session, rec, identityCtx := newCoverageSession(t)
		sc := structuredContentOf(t, session, tools.ToolGetBox, map[string]any{"id": covBox})
		ids := stringsAt(t, sc, "id")
		assertAllIssued(t, rec, identityCtx, tools.ToolGetBox, ids)
		docIDs := stringsAt(t, sc, "documentIds")
		assertNoneIssued(t, rec, identityCtx, tools.ToolGetBox+" (documentIds, Nebenprodukt — nie vom Aufrufer genannt)", docIDs)
	})

	t.Run("get_document_pdf_und_get_page_image_liefern_keine_kennung", func(t *testing.T) {
		session, _, _ := newCoverageSession(t)
		pdf := structuredContentOf(t, session, tools.ToolGetDocumentPDF, map[string]any{"id": covDocument})
		if _, ok := pdf["id"]; ok {
			t.Errorf("get_document_pdf's StructuredContent trägt unerwartet einen %q-Schlüssel: %+v", "id", pdf)
		}
		img := structuredContentOf(t, session, tools.ToolGetPageImage, map[string]any{"pageId": covPage, "version": 1})
		if _, ok := img["pageId"]; ok {
			t.Errorf("get_page_image's StructuredContent trägt unerwartet einen %q-Schlüssel: %+v", "pageId", img)
		}
	})
}

// TestSyncDocumentsMerktWederGeaenderteNochGeloeschteIDs ist der
// dedizierte, ausdrückliche Test dafür, dass sync_documents seit
// ADR-0019 (Betreiber-Entscheidung nach dem Sicherheits-Audit) BEIDE
// Kennungs-Arten NICHT aufnimmt — nicht nur die schon vorher niemals
// aufgenommenen gelöschten, sondern jetzt AUCH die geänderten/neuen. Die
// Diff-Fixture des Fake-Backends (newCoverageServer) liefert EINE
// geänderte Zeile (covDocumentSynced) und EINE gelöschte Kennung
// (covDocumentDeleted) in derselben Antwort — dieser Test prüft beide
// Hälften an genau diesem einen Aufruf, nicht nur die Hälfte, die
// TestJedesWerkzeugDasIDsAusliefertMerktSieAuch bereits abdeckt.
//
// Vor ADR-0019 hieß dieser Test TestSyncDocumentsMerktGeaenderteAberNichtGeloeschteIDs
// und prüfte das GENAUE GEGENTEIL für die geänderte Zeile (assertAllIssued
// statt assertNoneIssued) — siehe Git-Historie für die alte Fassung. Die
// gelöschte Kennung war und bleibt NIE ein gültiges Ziel — dieser Teil der
// Prüfung ist unverändert, nur ihre Begründung hat sich geändert: früher
// "eine gelöschte Entität ist kein gültiges Ziel mehr", jetzt zusätzlich
// "sync_documents nimmt ohnehin nichts mehr auf, egal was".
func TestSyncDocumentsMerktWederGeaenderteNochGeloeschteIDs(t *testing.T) {
	session, rec, identityCtx := newCoverageSession(t)

	sc := structuredContentOf(t, session, tools.ToolSyncDocuments, nil)

	changed := sliceOfMapStringsAt(t, sc, "entries", "id")
	if len(changed) != 1 || changed[0] != covDocumentSynced {
		t.Fatalf("entries-Kennungen = %v, erwartet [%s] — Fixture ist abgedriftet, erst die Fixture korrigieren, bevor diesem Test getraut wird", changed, covDocumentSynced)
	}
	deleted := stringsAt(t, sc, "deletedIds")
	if len(deleted) != 1 || deleted[0] != covDocumentDeleted {
		t.Fatalf("deletedIds = %v, erwartet [%s] — Fixture ist abgedriftet, erst die Fixture korrigieren, bevor diesem Test getraut wird", deleted, covDocumentDeleted)
	}

	assertNoneIssued(t, rec, identityCtx, tools.ToolSyncDocuments+" (geändert)", changed)
	assertNoneIssued(t, rec, identityCtx, tools.ToolSyncDocuments+" (gelöscht)", deleted)
}

// TestGetPageOcrMerktDieTokenKennungNicht ist der dedizierte,
// ausdrückliche Test für die zweite bewusste "Nein"-Entscheidung dieser
// Aufgabe — siehe den eigenen Paket-Doc-Kommentar dieser Datei und
// read_binary.go's eigenen Paket-Doc-Kommentar für die vollständige
// Begründung. ocrTokenPosition.WebappID sieht aus wie eine
// fileee-eigene Kennung (sie ist eine — Fileee's eigene generierte
// Kennung für das Token), aber kein Werkzeug dieses Servers nimmt je
// eine OCR-Token-Kennung als Parameter entgegen — eine Aufnahme würde
// nur einen Platz im Deckel je Identität des Aufrufers verbrauchen, ohne
// jede schützende Wirkung.
func TestGetPageOcrMerktDieTokenKennungNicht(t *testing.T) {
	session, rec, identityCtx := newCoverageSession(t)

	sc := structuredContentOf(t, session, tools.ToolGetPageOCR, map[string]any{"pageId": covPage})

	tokenCount, _ := sc["tokenCount"].(float64) // JSON-Zahlen dekodieren als float64 in map[string]any
	if int(tokenCount) != 1 {
		t.Fatalf("tokenCount = %v, erwartet 1 — Fixture ist abgedriftet, erst die Fixture korrigieren, bevor dieser Zusicherung getraut wird", sc["tokenCount"])
	}
	webappIDs := sliceOfMapStringsAt(t, sc, "tokens", "webappId")
	if len(webappIDs) != 1 || webappIDs[0] != covOCRToken {
		t.Fatalf("tokens[].webappId = %v, erwartet [%s] — Fixture ist abgedriftet, erst die Fixture korrigieren, bevor dieser Zusicherung getraut wird", webappIDs, covOCRToken)
	}

	assertNoneIssued(t, rec, identityCtx, tools.ToolGetPageOCR, webappIDs)
}

// TestGetDocumentPdfUndGetPageImageLiefernKeineKennungInDerStruktur ist
// eine zweite, JSON-Ebenen-Bestätigung der "gar keine Kennungen"-Hälfte
// des eigenen Subtests von TestJedesWerkzeugDasIDsAusliefertMerktSieAuch
// — sie liest die rohen, dekodierten JSON-Schlüssel direkt (über einen
// json.Marshal-Rundlauf) statt sich auf eine feste Feldnamen-Vermutung
// zu verlassen, damit ein künftig zu einem der beiden Ausgabetypen
// hinzugefügtes Feld, das wie eine Kennung aussieht, hier auffällt —
// selbst wenn der obige Subtest nicht daran gedacht hätte, genau nach
// diesem Namen zu suchen.
func TestGetDocumentPdfUndGetPageImageLiefernKeineKennungInDerStruktur(t *testing.T) {
	session, _, _ := newCoverageSession(t)

	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{tools.ToolGetDocumentPDF, map[string]any{"id": covDocument}},
		{tools.ToolGetPageImage, map[string]any{"pageId": covPage, "version": 1}},
	} {
		sc := structuredContentOf(t, session, tc.tool, tc.args)
		raw, err := json.Marshal(sc)
		if err != nil {
			t.Fatalf("%s: json.Marshal(StructuredContent): %v", tc.tool, err)
		}
		lower := strings.ToLower(string(raw))
		if strings.Contains(lower, `"id"`) || strings.Contains(lower, `"ids"`) || strings.Contains(lower, `id":`) {
			t.Errorf("%s: StructuredContent sieht so aus, als trüge es ein kennungsförmiges Feld: %s", tc.tool, raw)
		}
	}
}
