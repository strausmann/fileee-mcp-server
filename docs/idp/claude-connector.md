# Connector in Claude eintragen

Gilt unabhängig vom gewählten Identity Provider. Vorher muss der IdP eingerichtet sein — siehe [`authentik.md`](authentik.md) oder [`entra-id.md`](entra-id.md).

## Zwei Bauweisen — nicht verwechseln

Anleitungen für andere MCP-Server beschreiben teils eine andere Architektur. Der Unterschied ist am Redirect-URI erkennbar:

| | Redirect-URI im IdP | Wer stellt das Token aus | Client-ID im Connector |
|---|---|---|---|
| **Resource Server** (dieses Projekt) | `https://claude.ai/api/mcp/auth_callback` | der IdP | muss eingetragen werden |
| **Eigener Authorization Server** | `https://<mcp-host>/callback` | der MCP-Server selbst, IdP nur als Login-Broker | entfällt, sofern der Server Dynamic Client Registration anbietet |

`fileee-mcp-server` ist ein Resource Server. Wird versehentlich der Redirect-URI der zweiten Variante im IdP eingetragen, schlägt der Login fehl, ohne dass der MCP-Server je einen Request sieht.

## Vorab prüfen

```bash
curl -i -X POST <MCP_URL>
# erwartet: 401 + WWW-Authenticate: Bearer resource_metadata="…"

curl -s https://<MCP_HOST>/.well-known/oauth-protected-resource | jq
curl -s https://<MCP_HOST>/.well-known/oauth-protected-resource/mcp | jq
# beide Pfade müssen dasselbe Dokument liefern

curl -sI <MCP_URL>
# darf kein 3xx liefern
```

Drei Dinge müssen stimmen, sonst scheitert die Verbindung ohne aussagekräftige Fehlermeldung:

- Das `resource`-Feld im Metadata-Dokument entspricht **exakt** der URL, die im Connector eingetragen wird — inklusive Pfad.
- `authorization_servers` führt den Issuer als **ersten** Eintrag. Spätere Einträge werden ignoriert, es gibt keinen Fallback.
- Kein Redirect auf einen anderen Host. Ein Cross-Host-Redirect verwirft den `Authorization`-Header; das äußert sich als „Authorization failed".

Der zweite Well-known-Pfad mit angehängter MCP-Pfadkomponente ist [RFC 9728 §3.1](https://www.rfc-editor.org/rfc/rfc9728#section-3.1) und wird von Claude angefragt, wenn die MCP-URL einen Pfad hat.

## Eintragen

*Einstellungen → Connectors → Benutzerdefinierten Connector hinzufügen*

| Feld | Wert |
|---|---|
| Name | frei wählbar |
| Remote MCP Server URL | `<MCP_URL>` |
| OAuth Client ID | `<CLIENT_ID>` |
| OAuth Client Secret | `<CLIENT_SECRET>` — bei einem Public Client leer lassen |

Anschließend den Login-Flow durchlaufen und einen Tool-Aufruf testen.

## Negativtest

Ein Account, der nicht in der Gruppenbindung des IdP liegt bzw. nicht in `MCP_ALLOWED_SUBJECTS` steht, darf **nicht** durchkommen. Der Server antwortet in diesem Fall mit `403` und fällt nicht auf ein Standardkonto zurück.

## Zeitgrenzen

Claude wartet maximal 10 s auf Discovery-, Registration- und Token-Endpunkte und maximal 30 s auf Refresh-Requests. Ein Reverse Proxy, der Antworten puffert oder auf langsame Upstream-Aufrufe wartet, erzeugt sporadische Verbindungsfehler.

## Andere Claude-Oberflächen

| Oberfläche | Callback |
|---|---|
| Claude.ai Web, Desktop, Mobile | `https://claude.ai/api/mcp/auth_callback` |
| Claude Code | RFC-8252-Loopback auf wechselndem Port, deklariert `http://localhost/callback` und `http://127.0.0.1/callback` — der IdP muss beide **port-unabhängig** akzeptieren |

Ohne port-unabhängige Redirect-URI im IdP funktioniert der lokale Testpfad über Claude Code nicht. Authentik kann das per Regex, Entra ID nicht.
