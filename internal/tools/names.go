// names.go buendelt die Namen aller lesenden Werkzeuge und ihre Einstufung
// fuer Gangways Autorisierungs-Zwischenschicht an einer Stelle. Ein Name,
// der hier fehlt, gilt dort als access.KindWrite und wird fuer jeden
// Aufrufer abgelehnt.
//
// Die Einstufung (readToolNames) ist bewusst NICHT aus der tatsaechlichen
// Anmeldung abgeleitet, obwohl RegisterRead (registeredReadTools) genau das
// haette hergeben koennen — eine Liste, die sich selbst aus dem prueft, was
// sie eigentlich absichern soll, kann nichts entdecken: ein versehentlich
// registriertes schreibendes Werkzeug waere ebenso automatisch als lesend
// durchgerutscht. readToolNames ist deshalb eine zweite, physisch getrennte
// Handlung; registeredReadTools() dient nur noch als Gegenprobe in beide
// Richtungen. Ein fehlender Eintrag fuehrt zur Ablehnung (KindWrite), nie
// zur Freigabe — die sichere Ausfallrichtung. Details siehe readToolNames.
package tools

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/gangway/access"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

// ToolListDocuments and ToolSearchDocuments are list_documents' and
// search_documents' registered names, exported so a caller wiring
// Gangway's tool-authorization middleware doesn't have to repeat the
// string literals — see ReadToolKinds.
const (
	ToolListDocuments   = "list_documents"
	ToolSearchDocuments = "search_documents"
)

// readToolNames is the hand-maintained list of tool names ReadToolKinds
// classifies as access.KindRead — the only source it consults.
//
// This is deliberately NOT derived from registeredReadTools(). An earlier
// version of this file did exactly that (every tool RegisterRead mounts is
// read, unconditionally) and a review caught the failure mode: a writing
// tool mistakenly added to RegisterRead's wiring would have been
// auto-classified as reading and thereby granted unconditional access for
// every caller (access.NewGrid.Allow lets KindRead through with no role
// check at all — see gangway/access/grid.go). Reproduced with a
// delete_document tool inserted into RegisterRead: every test in this file
// still passed, because the classification and the check it was supposed
// to guard against read the exact same source.
//
// ToolAnnotations.ReadOnlyHint (set inline at the mcp.AddTool call) was
// considered and rejected as the independent source: gangway's
// toolMiddleware never reads it — only this map — so it would still only
// affect map-building, not authorization directly; and the SDK's own doc
// comment on ToolAnnotations warns "Clients should never make tool use
// decisions based on ToolAnnotations received from untrusted servers"
// (go-sdk/mcp/protocol.go). More concretely: whoever mistakenly adds a
// destructive tool to RegisterRead is copying boilerplate from a
// neighbouring read tool at the very same call site — the same
// carelessness that added the tool there would just as easily carry
// ReadOnlyHint: true along with it.
//
// A hand-maintained list is a second, physically separate edit — adding a
// name here is not something a copy-pasted mcp.AddTool block does for you.
// registeredReadTools() (the actual, live tools/list round-trip) becomes
// this list's Gegenprobe instead of its source: descriptions_test.go's
// TestJedesWerkzeugIstAlsLesendEingestuft fails loudly if a registered tool
// is missing here (forgotten entry, or a write tool that was never meant
// to be here at all), and TestReadToolKindsEnthaeltKeineUnbekanntenNamen
// fails loudly if an entry here names nothing RegisterRead actually
// mounts (stale entry after a rename or removal). Both directions now
// scream instead of one of them silently granting access — see
// docs/superpowers/plans/2026-08-12-fileee-werkzeuge-teil-a-lesend.md,
// "Globale Randbedingungen", for the full writeup.
var readToolNames = []string{
	ToolListDocuments,
	ToolSearchDocuments,
}

// ReadToolKinds returns the access.ToolKind classification for every tool
// named in readToolNames — the mapping Gangway's tool-authorization
// middleware needs via serve.WithToolKinds.
//
// This is not optional wiring: a tool name absent from that mapping
// defaults to access.KindWrite (see gangway/serve, toolMiddleware), and
// this server's default access.Decider (access.NewGrid) refuses writing
// outright unless a writer role is separately configured. Without
// serve.WithToolKinds(tools.ReadToolKinds()) at the call to serve.New,
// every call to any of these strictly reading tools would be refused for
// every caller, including ones with no interest in writing anything.
func ReadToolKinds() map[string]access.ToolKind {
	kinds := make(map[string]access.ToolKind, len(readToolNames))
	for _, name := range readToolNames {
		kinds[name] = access.KindRead
	}
	return kinds
}

// registeredReadTools mounts RegisterRead onto a throwaway server and reads
// its tools back over an in-memory client-server connection — the ground
// truth of what RegisterRead actually mounts, used two ways:
//
//  1. descriptions_test.go's description-length check runs against this
//     list, because that question ("what does a caller see") is exactly
//     what a real tools/list call answers.
//  2. It is readToolNames' Gegenprobe, not its source (see that var's doc
//     comment for why the two must stay independent): the same tests
//     compare this list against readToolNames in both directions, so
//     neither a forgotten entry nor a stale one goes unnoticed.
//
// go-sdk v1.7.0's *mcp.Server keeps its registered tools in an unexported
// featureSet (see mcp.Server.AddTool/listTools) with no public accessor —
// there is no "probe.Tools()". Introspecting a server's own tool set
// therefore means acting like a real MCP client and asking it, the same
// pattern this repo's own tests already use for exactly this purpose
// (internal/server/server_test.go, toolNamesOf).
//
// p is (*clientpool.Pool)(nil): none of RegisterRead's tool handlers run
// during registration or during a tools/list round-trip — only AddTool's
// schema derivation and the ListTools call below do, neither of which
// touches p. logger is a discarding *slog.Logger for the same reason:
// nothing here calls a handler, so nothing here logs.
func registeredReadTools() []*mcp.Tool {
	ctx := context.Background()

	probe := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	RegisterRead(probe, (*clientpool.Pool)(nil), slog.New(slog.DiscardHandler))

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := probe.Connect(ctx, serverTransport, nil)
	if err != nil {
		panic(fmt.Errorf("fileee-mcp: tools: registeredReadTools: connect probe server: %w", err))
	}
	// A close failure on a throwaway in-memory session that already served its
	// one tools/list round-trip changes nothing about the result already
	// captured below — same reasoning as cmd/fileee-mcp-server/main.go's
	// resp.Body.Close() on a healthcheck response already fully read.
	defer func() { _ = serverSession.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "registered-read-tools-probe", Version: "0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		panic(fmt.Errorf("fileee-mcp: tools: registeredReadTools: connect probe client: %w", err))
	}
	defer func() { _ = clientSession.Close() }()

	res, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		panic(fmt.Errorf("fileee-mcp: tools: registeredReadTools: list tools: %w", err))
	}
	return res.Tools
}
