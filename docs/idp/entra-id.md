# Microsoft Entra ID als Authorization Server

`fileee-mcp-server` ist ein reiner OAuth-2.1-Resource-Server. Er stellt selbst keine Tokens aus, sondern verweist Clients über [RFC 9728](https://www.rfc-editor.org/rfc/rfc9728) auf einen externen Identity Provider und prüft die dort ausgestellten Tokens gegen dessen JWKS. Für Authentik siehe [`authentik.md`](authentik.md).

## Platzhalter

| Platzhalter | Bedeutung | Beispiel |
|---|---|---|
| `<MCP_URL>` | vollständige MCP-Endpunkt-URL, exakt wie sie im Client eingetragen wird | `https://mcp.example.com/mcp` |
| `<MCP_HOST>` | nur der Host davon | `mcp.example.com` |
| `<TENANT_ID>` | Verzeichnis-ID (Directory / tenant ID) | `00000000-0000-0000-0000-000000000000` |
| `<CLIENT_ID>` / `<CLIENT_SECRET>` | Anwendungs-ID und Secret der App-Registrierung | – |
| `<APP_SLUG>` | Anzeigename der App-Registrierung | `fileee-mcp` |

## Voraussetzungen

1. Entra muss aus dem Egress-Bereich des MCP-Clients erreichbar sein — bei Claude `160.79.104.0/21`. Für die öffentlichen Microsoft-Endpunkte ist das normalerweise gegeben.
2. Entra unterstützt PKCE mit `S256`.
3. Discovery und Token-Endpunkt müssen innerhalb von 10 s antworten, Refresh innerhalb von 30 s.

## 1. App-Registrierung anlegen

*Microsoft Entra Admin Center → App registrations → New registration*

| Feld | Wert |
|---|---|
| Name | `<APP_SLUG>` |
| Supported account types | „Accounts in this organizational directory only" (Single-Tenant), sofern kein Multi-Tenant-Zugriff gewünscht ist |
| Redirect URI | Plattform **Web**, `https://claude.ai/api/mcp/auth_callback` |

Nach dem Anlegen notieren: **Application (client) ID** = `<CLIENT_ID>`, **Directory (tenant) ID** = `<TENANT_ID>`.

Für lokale Tests mit Claude Code zusätzlich unter *Authentication → Add a platform → Mobile and desktop applications* die Loopback-URIs eintragen. Entra prüft Redirect-URIs exakt, **inklusive Port** — für jeden zu testenden Port ist ein eigener Eintrag nötig. Wer darauf verzichten kann, testet nur über die gehosteten Claude-Oberflächen.

## 2. Client-Secret erzeugen

*Certificates & secrets → New client secret*

Gültigkeit bewusst wählen, Wert **sofort** notieren (`<CLIENT_SECRET>`) — er ist danach nicht mehr abrufbar. Ablaufdatum vormerken: ein abgelaufenes Secret äußert sich als plötzlich nicht mehr funktionierender Connector, ohne Änderung am Server.

## 3. API exponieren

*Expose an API*

### Application ID URI

Entra schlägt standardmäßig `api://<CLIENT_ID>` vor. **Das reicht nicht.** Der Client sendet `resource=<MCP_URL>` ([RFC 8707](https://www.rfc-editor.org/rfc/rfc8707)), und Entra lehnt einen Resource-Wert ab, der nicht als Application ID URI registriert ist — der Token-Request scheitert dann mit **`AADSTS9010010`**.

`<MCP_URL>` deshalb **zusätzlich** als Application ID URI eintragen.

Für ein `https://`-Schema verlangt Entra eine im Tenant verifizierte Domain. Ist die Domain nicht verifizierbar, bleibt nur, den MCP-Server unter einem Host zu betreiben, dessen Domain verifiziert ist.

### Scope hinzufügen

*Add a scope*

| Feld | Wert |
|---|---|
| Scope name | `mcp.access` |
| Who can consent | Admins and users |
| Consent display name / description | sprechend formulieren — der Text erscheint im Consent-Dialog |
| State | Enabled |

Voller Scope-Wert: `<MCP_URL>/mcp.access` bzw. `api://<CLIENT_ID>/mcp.access`. Er wird im Protected-Resource-Metadata-Dokument des MCP-Servers unter `scopes_supported` veröffentlicht, damit der Client ihn anfordert.

## 4. Token-Version auf v2.0 stellen

*Manifest* öffnen und sicherstellen:

```json
"accessTokenAcceptedVersion": 2
```

Ohne diese Einstellung stellt Entra v1.0-Tokens mit abweichendem Issuer aus, und die Prüfung gegen `https://login.microsoftonline.com/<TENANT_ID>/v2.0` schlägt fehl.

## 5. Zugriff einschränken

*Enterprise applications → `<APP_SLUG>` → Properties* → **Assignment required = Yes**, danach unter *Users and groups* gezielt zuweisen.

Ohne diese Einstellung kann jeder Benutzer des Tenants den Connector verbinden.

## 6. Ermittelte Werte

| Wert | Pfad |
|---|---|
| Discovery | `https://login.microsoftonline.com/<TENANT_ID>/v2.0/.well-known/openid-configuration` |
| Issuer | `https://login.microsoftonline.com/<TENANT_ID>/v2.0` |
| JWKS | aus dem Discovery-Dokument (`jwks_uri`) |
| `aud` im v2-Token | `<CLIENT_ID>` |

```dotenv
MCP_AUTH_MODE=oidc
MCP_OIDC_ISSUER=https://login.microsoftonline.com/<TENANT_ID>/v2.0
MCP_OIDC_AUDIENCE=<CLIENT_ID>
MCP_OIDC_SUBJECT_CLAIM=sub
MCP_OIDC_REQUIRED_SCOPES=mcp.access
MCP_RESOURCE_URL=<MCP_URL>
MCP_ALLOWED_SUBJECTS=<sub- oder oid-Wert des berechtigten Benutzers>
```

### Zum Subject-Claim

`sub` ist bei Entra **paarweise pseudonymisiert**: derselbe Benutzer hat in einer anderen App-Registrierung einen anderen `sub`. Für die Konto-Zuordnung ist das unproblematisch, solange nur diese Anwendung genutzt wird.

Soll die Zuordnung über mehrere Anwendungen hinweg stabil sein, `MCP_OIDC_SUBJECT_CLAIM=oid` verwenden — die Objekt-ID ist tenant-weit eindeutig. `email` ist die schlechteste Wahl: der Wert ist änderbar und steht je nach Konfiguration gar nicht im Token.

### Zu den Scopes

Entra liefert den **kurzen** Namen im `scp`-Claim (`mcp.access`), während im Protected-Resource-Metadata-Dokument der volle URI-Scope veröffentlicht wird. `MCP_OIDC_REQUIRED_SCOPES` erwartet die kurze Form.

## 7. Verifikation

```bash
curl -s https://login.microsoftonline.com/<TENANT_ID>/v2.0/.well-known/openid-configuration \
  | jq '{issuer, code_challenge_methods_supported, token_endpoint}'
```

Nach dem ersten erfolgreichen Login das Token prüfen:

```bash
echo "<ACCESS_TOKEN>" | cut -d. -f2 | base64 -d 2>/dev/null | jq '{iss, aud, sub, oid, scp, exp}'
```

Erwartet: `iss` endet auf `/v2.0`, `aud` = `<CLIENT_ID>`, `scp` enthält `mcp.access`.

## 8. Bekannte Stolpersteine

| Symptom | Ursache | Behebung |
|---|---|---|
| `AADSTS9010010` beim Token-Request | `<MCP_URL>` ist nicht als Application ID URI registriert | Schritt 3 nachholen |
| `iss` endet nicht auf `/v2.0` | `accessTokenAcceptedVersion` steht nicht auf `2` | Manifest korrigieren |
| `AADSTS50011` (Redirect-URI-Mismatch) | Redirect-URI weicht ab, bei Claude Code der Port | exakte URI eintragen; Entra ignoriert den Port **nicht** |
| Connector funktioniert plötzlich nicht mehr | Client-Secret abgelaufen | neues Secret erzeugen und im Connector aktualisieren |
| Jeder Tenant-Benutzer kommt durch | „Assignment required" steht auf `No` | Schritt 5 nachholen |
| Kein DCR möglich | Entra unterstützt weder Dynamic Client Registration noch Client ID Metadata Documents | vorregistrierte Client-ID/Secret im Connector-Dialog eintragen — der vorgesehene Weg |
