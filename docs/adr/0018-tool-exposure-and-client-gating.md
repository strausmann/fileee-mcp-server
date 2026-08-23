# ADR-0018: Tool Exposure and Client-Side Gating

**Status:** accepted
**Datum:** 2026-08-23
**Ersetzt:** [ADR-0011](0011-capability-gating.md)
**Ersetzt durch:** —
**Überarbeitet:** [ADR-0010](0010-idp-agnostische-konfiguration.md) — Punkt 3 (`MCP_OIDC_CAPABILITY_CLAIM` removed)
**Überarbeitet durch:** —
**Verwandt:** [ADR-0012](0012-multi-account-mapping.md), [ADR-0013](0013-prompt-injection-schutz.md), [ADR-0015](0015-gangway-als-unterbau.md)

## Context

MCP connectors are meant to expose all tools with correct `ToolAnnotations` and let the **client
and its user** gate access per tool — Always allow / Needs approval / Blocked — per the [MCP
specification's tool annotations](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)
(`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`) and the client-side approval
model that Claude and other MCP hosts build on top of them. Anthropic's [connector review
criteria](https://claude.com/docs/connectors/building/review-criteria) expect a server to describe
what each tool does and how risky it is, so the host and its user can decide — not to withhold
tools from the catalog based on a server-side policy the user never sees.

[ADR-0011](0011-capability-gating.md)'s per-capability server-side gating — four capability groups
(`read`/`write`/`share`/`destructive`), configured via `FILEEE_CAPABILITIES` and
`FILEEE_ALLOW_DESTRUCTIVE`, resolved against an optional IdP claim (`MCP_OIDC_CAPABILITY_CLAIM`),
and enforced by simply never registering a tool the caller's resolved capability set does not
include — contradicts that model. It reintroduces, on the server, exactly the per-tool allow/deny
decision the connector model places with the client and its user. It also multiplies server state:
one `*mcp.Server` instance per capability set, chosen per caller at connect time, purely so an
unregistered tool never appears in `tools/list`.

The write/share/destructive tools this gating was meant to protect do not exist yet on this branch
— only the `read` group is implemented (32 tools). Removing the gating now, before those tools
exist, avoids building and then un-building a server-side policy layer around them.

Account isolation ([ADR-0012](0012-multi-account-mapping.md)) and untrusted-content framing
([ADR-0013](0013-prompt-injection-schutz.md)) are orthogonal concerns — which Fileee account a
caller is bound to, and how document content is presented so the model does not treat it as
instructions — and are unaffected by this decision.

## Decision

The server exposes **every** tool, always, to every caller who passes Gangway's authentication and
required-scope check. There is no server-side notion of "this caller may not see tool X."

1. **Every tool carries a `Title` and the hints that apply to it** — `ReadOnlyHint: true` for the
   read-only tools that exist today; `DestructiveHint`/`IdempotentHint` for future write/destructive
   tools, set truthfully per operation rather than defaulted. `Title` is a short, human-readable
   label a client's approval UI can show next to the raw tool name — already true for all 36 tools
   `tools.RegisterAll` mounts.
2. **Gangway authorizes every call with `access.AllowAll()`.** There is exactly one `*mcp.Server`
   instance, mounted once, not chosen per caller. `AttachMCPSelector` is still used — Gangway only
   routes `/mcp` through an attached selector or server (see [ADR-0015](0015-gangway-als-unterbau.md))
   — but the selector always returns the same instance once authentication and the required-scope
   check succeed.
3. **`FILEEE_CAPABILITIES`, `FILEEE_ALLOW_DESTRUCTIVE`, and `MCP_OIDC_CAPABILITY_CLAIM` are removed**
   from configuration entirely — not defaulted, not deprecated-but-accepted. A deployment that still
   sets them gets the same unrecognized-variable rejection as any other stale setting, not silent
   ignoring.
4. **Account isolation and content framing are unchanged.** [ADR-0012](0012-multi-account-mapping.md)
   still decides which Fileee account a call runs against; [ADR-0013](0013-prompt-injection-schutz.md)
   still frames document content as untrusted data. Neither is a form of tool gating, and neither is
   touched by this decision.

## Consequences

**Positive**

- The client and its user make the actual allow/deny decision per tool, with full information
  (`Title`, `ReadOnlyHint`, `DestructiveHint`, `IdempotentHint`) instead of a coarse, server-chosen
  four-way split whose reasoning the user never sees.
- One server instance, one code path. No per-capability-set instance catalog to keep in sync with
  the tool set as it grows — future write/share/destructive tools are simply registered with
  truthful hints, no new gating logic to maintain alongside them.
- No possibility of a tool being reachable through a handler bug despite being "not granted" —
  there is no grant to bypass in the first place.
- Matches the shape MCP hosts under active client-side review already expect from a connector:
  Claude and other hosts build approval UI on `ToolAnnotations`; a server that hides tools instead
  of annotating them works against that UI, not with it.

**Negative / costs**

- A deployment operator loses the ability to hand out a connector link that only exposes a subset
  of tools by server-side policy. A deployment that genuinely wants "read-only, no matter what the
  client does" needs a different mechanism — e.g. a separate, intentionally read-only build, or an
  operator-side policy enforced outside this server. That remains legitimate, but is out of scope
  here and explicitly not the default going forward.
- Every future write/share/destructive tool must set its hints correctly and honestly the first
  time — there is no second gate behind it to catch an under-annotated destructive tool. This shifts
  the correctness burden from "was this capability group ever unlocked" (a config question, easy to
  verify at startup) to "does this tool's `DestructiveHint`/`IdempotentHint` truthfully describe what
  it does" (a per-tool code-review question).
- Operators relying on `FILEEE_CAPABILITIES`, `FILEEE_ALLOW_DESTRUCTIVE`, or
  `MCP_OIDC_CAPABILITY_CLAIM` must remove them from their deployment config; the server rejects them
  as unrecognized rather than silently accepting and ignoring them.

## References

- MCP specification, [Tool annotations](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)
- Anthropic, [Connector review criteria](https://claude.com/docs/connectors/building/review-criteria)
- [ADR-0011](0011-capability-gating.md) — superseded per-capability server-side gating
- [ADR-0010](0010-idp-agnostische-konfiguration.md) — Punkt 3, partially revised (capability claim removed)
- [ADR-0012](0012-multi-account-mapping.md) — account isolation, unaffected
- [ADR-0013](0013-prompt-injection-schutz.md) — untrusted-content framing, unaffected
- [ADR-0015](0015-gangway-als-unterbau.md) — `access.Decider` / `AttachMCPSelector` mechanics this decision builds on
- `internal/tools/read.go`, `internal/server/server.go` — `RegisterAll`, `access.AllowAll()`
