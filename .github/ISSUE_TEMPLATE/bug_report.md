---
name: Bug-Report
about: Ein Problem mit fileee-mcp-server melden
title: "bug: "
labels: bug
---

## Beschreibung

Kurze Beschreibung des Problems.

## Reproduktion

Schritte, um das Verhalten zu reproduzieren (Tool-Name, Parameter, erwartetes vs. tatsächliches
Ergebnis; bei Auth-Problemen zusätzlich der Discovery-/Token-Schritt, an dem es scheitert):

1. …
2. …

**Keine echten Credentials, Access-Tokens, Dokumentinhalte oder personenbezogene Daten (PII)
posten** — vor dem Anhängen von Requests/Responses entfernen bzw. durch synthetische Werte
ersetzen. Ein JWT enthält die Identität im Klartext; bitte nur die Claim-*Namen* nennen, nicht das
Token.

## Erwartetes Verhalten

Was hätte passieren sollen?

## Tatsächliches Verhalten

Was ist stattdessen passiert? Fehlermeldung, HTTP-Status, Response-Body oder Stacktrace, falls
vorhanden.

## Umgebung

- fileee-mcp-server-Version/Tag: `<z. B. v0.1.0>` (oder Ausgabe von `fileee-mcp-server version`)
- go-fileee-Version (siehe `go.mod`): `<z. B. v0.1.1>`
- Betriebsart: [ ] Binary direkt [ ] Docker-Compose [ ] hinter Reverse Proxy
- Auth-Modus: [ ] `token` [ ] `oidc` [ ] `both`
- Konto-Modus: [ ] `single` [ ] `multi`
- Identity Provider (bei `oidc`): [ ] Authentik [ ] Entra ID [ ] anderer: …
- Secret-Backend: [ ] `.env` [ ] Infisical-Dual-Mode (`SECRET_BACKEND=infisical`)
- Go-Version (bei lokalem Build): `go version`
- MCP-Client: [ ] Claude.ai Web/Desktop/Mobile [ ] Claude Code [ ] anderer: …

## Zusätzlicher Kontext

Alles Weitere, das beim Debuggen hilft — Logs mit maskierten Secrets, Ausgabe von
`curl -i -X POST <MCP_URL>` und der beiden `/.well-known/oauth-protected-resource`-Pfade.
