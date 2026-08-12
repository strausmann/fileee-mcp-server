// names.go buendelt die Namen aller lesenden Werkzeuge und ihre Einstufung
// fuer Gangways Autorisierungs-Zwischenschicht an einer Stelle. Ein Name,
// der hier fehlt, gilt dort als access.KindWrite und wird fuer jeden
// Aufrufer abgelehnt — deshalb liegen Anmeldung und Einstufung nebeneinander,
// abgeleitet aus denselben tatsaechlich angemeldeten Werkzeugen statt
// doppelt gepflegt (siehe registeredReadTools).
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

// ReadToolKinds returns the access.ToolKind classification for every tool
// RegisterRead adds — the mapping Gangway's tool-authorization middleware
// needs via serve.WithToolKinds.
//
// This is not optional wiring: a tool name absent from that mapping
// defaults to access.KindWrite (see gangway/serve, toolMiddleware), and
// this server's default access.Decider (access.NewGrid) refuses writing
// outright unless a writer role is separately configured. Without
// serve.WithToolKinds(tools.ReadToolKinds()) at the call to serve.New,
// every call to any of these strictly reading tools would be refused for
// every caller, including ones with no interest in writing anything.
//
// The mapping is derived from registeredReadTools() rather than listed by
// hand, so it can never omit a tool RegisterRead actually mounts — the
// failure mode this function exists to prevent.
func ReadToolKinds() map[string]access.ToolKind {
	registered := registeredReadTools()
	kinds := make(map[string]access.ToolKind, len(registered))
	for _, tool := range registered {
		kinds[tool.Name] = access.KindRead
	}
	return kinds
}

// registeredReadTools mounts RegisterRead onto a throwaway server and reads
// its tools back over an in-memory client-server connection.
//
// go-sdk v1.7.0's *mcp.Server keeps its registered tools in an unexported
// featureSet (see mcp.Server.AddTool/listTools) with no public accessor —
// there is no "probe.Tools()". Introspecting a server's own tool set
// therefore means acting like a real MCP client and asking it, the same
// pattern this repo's own tests already use for exactly this purpose
// (internal/server/server_test.go, toolNamesOf). Going through the wire
// protocol like a real caller, rather than keeping a second,
// hand-maintained list of registered tools, means this function can never
// drift from what RegisterRead actually mounts: descriptions_test.go's
// checks see exactly what a real MCP client sees after a tools/list call.
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
