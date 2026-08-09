# ADR-0009: Reiner Resource Server statt eigenem Authorization Server

**Status:** accepted
**Datum:** 2026-08-06
**Ersetzt:** —
**Ersetzt durch:** —
**Überarbeitet:** —
**Überarbeitet durch:** —
**Verwandt:** [ADR-0010](0010-idp-agnostische-konfiguration.md), [ADR-0012](0012-multi-account-mapping.md), [ADR-0015](0015-gangway-als-unterbau.md), [ADR-0016](0016-anbieter-namensraeume-statt-roher-oidc-parameter.md), [fileee-server ADR-0008](https://github.com/strausmann/fileee-server/blob/main/docs/adr/0008-fileee-server.md)

## Kontext

Fileee-Inhalte sollen in AI-Clients nutzbar sein, primär als **Custom Connector in der Claude.ai-Web-UI**. Ein solcher Connector spricht einen Remote-MCP-Server über HTTPS an und erwartet dort eine OAuth-2.1-Absicherung nach der MCP-Authorization-Spezifikation: 401 mit `WWW-Authenticate`, Protected Resource Metadata nach [RFC 9728](https://www.rfc-editor.org/rfc/rfc9728), PKCE mit `S256`.

Der bestehende [`fileee-server`](https://github.com/strausmann/fileee-server) kann das nicht: er kennt genau ein Fileee-Konto und ein statisches Bearer-Token, also keine benutzergebundene Identität.

Für die OAuth-Rolle des neuen Servers gibt es zwei etablierte Bauweisen:

**A — Reiner Resource Server.** Der Server stellt keine Tokens aus. Sein Metadata-Dokument verweist auf einen externen Identity Provider; der Client holt das Token dort und der Server prüft es gegen dessen JWKS.

**B — Eigener Authorization Server.** Der Server stellt eigene Tokens aus und nutzt den IdP nur als vorgelagerten Login-Broker. Zwei Referenzimplementierungen sind bekannt: [`ttpears/bookstack-mcp`](https://github.com/ttpears/bookstack-mcp) sowie der im eigenen HomeLab betriebene `mcp-gitlab`.

Variante B hat einen echten Vorteil: der Server kann Dynamic Client Registration selbst anbieten, wodurch im Connector-Dialog weder Client-ID noch Secret eingetragen werden müssen. Sie kostet aber Authorize-, Token- und Registration-Endpunkte, Consent, Refresh-Rotation und Schlüsselverwaltung — alles sicherheitskritischer Eigencode vor privaten Finanzdokumenten.

Der ausschlaggebende offene Punkt war das Verhalten des vorgesehenen IdP beim `aud`-Claim und beim Resource-Indicator ([RFC 8707](https://www.rfc-editor.org/rfc/rfc8707)). Er wurde vor der Entscheidung gegen eine laufende Authentik-2026.2-Instanz geklärt.

## Entscheidung

1. **Variante A.** Der Server ist ein reiner OAuth-2.1-Resource-Server. Er liefert das Protected-Resource-Metadata-Dokument unter **beiden** von der Spezifikation vorgesehenen Pfaden aus — `/.well-known/oauth-protected-resource` und, wegen der Pfadkomponente in der MCP-URL, zusätzlich `/.well-known/oauth-protected-resource/mcp` ([RFC 9728 §3.1](https://www.rfc-editor.org/rfc/rfc9728#section-3.1)).

2. **Client-Registrierung bleibt statisch.** Client-ID und optionales Secret werden im Connector-Dialog eingetragen. Dynamic Client Registration wird nicht implementiert und ist auch nicht nötig — Claude unterstützt statische Zugangsdaten für Custom Connectors ausdrücklich.

3. **Mehrere Audience-Werte gelten als gültig:** `MCP_RESOURCE_URL`, `MCP_OIDC_AUDIENCE` und, bei Entra ID, `api://<client-id>`. Eine zu enge Prüfung erzeugt eine 401-Schleife, die im Client nur als „Authorization failed" ankommt.

4. **Absolute URLs stammen ausschließlich aus der Konfiguration.** Das `resource`-Feld im Metadata-Dokument und die `resource_metadata`-URL im `WWW-Authenticate`-Header werden aus `MCP_RESOURCE_URL` gebildet, niemals aus `r.Host` oder `X-Forwarded-*`. Ein client-setzbarer Header dürfte die Ressourcen-Identität nicht bestimmen.

5. **Kein IP-basiertes Blocking oder Rate-Limiting** im Server. Alle Claude-Nutzer teilen sich denselben Egress-Bereich; eine IP-Grenze trifft entweder niemanden oder alle gleichzeitig. Missbrauchsschutz greift pro Subject.

## Konsequenzen

**Positiv**

- Deutlich weniger sicherheitskritischer Eigencode. Das SDK liefert `auth.RequireBearerToken` und `auth.ProtectedResourceMetadataHandler` fertig mit; die Eigenleistung beschränkt sich auf Discovery, JWKS-Caching und die Claim-Prüfung.
- Der IdP bleibt Single Source of Truth für Benutzer, Gruppen und Consent. Kein zweiter Ort, an dem Zugriffe verwaltet werden.
- Keine eigene Schlüsselverwaltung, keine Refresh-Token-Rotation, kein Consent-Screen.

**Negativ**

- Im Connector-Dialog müssen Client-ID und Secret eingetragen werden. Für jeden weiteren Benutzer ist das eine Handreichung.
- Die Resource-Bindung hängt vom IdP ab. **Authentik 2026.2 ignoriert `resource` vollständig** — kein Fehler, keine Wirkung; `aud` wird hart auf die Client-ID gesetzt (`id_token.py`). Damit validiert jedes Token desselben Clients gegen diesen Server, auch eines für eine andere Anwendung. Gegenmaßnahme ist ein Scope-Mapping, das die Resource-URI zusätzlich in `aud` schreibt; sie ist Betriebsaufgabe, nicht Code.
- Bei Entra ID muss die MCP-URL als Application ID URI registriert sein, sonst scheitert der Token-Request mit `AADSTS9010010`.

**Wann diese Entscheidung neu zu bewerten ist**

- Wenn der Connector für Personen außerhalb des eigenen Umfelds nutzbar sein soll und das Eintragen von Client-ID und Secret zur Hürde wird.
- Wenn ein IdP eingesetzt werden soll, dessen `aud`-Verhalten sich nicht auf einen akzeptierten Wert bringen lässt.

In beiden Fällen ist der Wechsel auf Variante B kein Neuland — zwei erprobte Referenzimplementierungen existieren. Betroffen wären `auth_oidc.go` und die Discovery-Handler; der Client-Pool, die Konto-Auflösung und die Tools blieben unverändert.

## Referenzen

- [MCP Authorization Specification](https://modelcontextprotocol.io/specification/draft/basic/authorization)
- [Claude: Authentication for connectors](https://claude.com/docs/connectors/building/authentication)
- [RFC 9728 — OAuth 2.0 Protected Resource Metadata](https://www.rfc-editor.org/rfc/rfc9728)
- [RFC 8707 — Resource Indicators for OAuth 2.0](https://www.rfc-editor.org/rfc/rfc8707)
- `docs/idp/authentik.md`, `docs/idp/entra-id.md` — die betriebliche Umsetzung je IdP
