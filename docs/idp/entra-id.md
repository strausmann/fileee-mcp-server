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

Voller Scope-Wert: `<MCP_URL>/mcp.access` bzw. `api://<CLIENT_ID>/mcp.access`. Dieser Server veröffentlicht ihn **nicht** im eigenen Protected-Resource-Metadata-Dokument (siehe Abschnitt "Zu den Scopes" unten) — der Client (bzw. der Connector-Dialog) muss ihn deshalb von hier oder aus der Connector-Konfiguration kennen.

> **Alternative `.default`:** Statt eines benannten Scopes lässt sich auch der Entra-eigene Sammel-Scope `api://<CLIENT_ID>/.default` anfordern, der alle statisch konfigurierten Berechtigungen der App auflöst. Er funktioniert nur, wenn mindestens ein Scope existiert — ein leeres „Expose an API" lässt `.default` ins Leere laufen. Wer diesen Weg geht, legt trotzdem einen Scope an (üblich: `user_impersonation`) und fordert dann `.default` an.

## 3a. App-Rollen für den Funktionsumfang (optional, empfohlen)

Statt den Funktionsumfang je Benutzer in der Server-Konfiguration zu pflegen, lässt er sich in Entra verwalten. *App roles → Create app role*, je Capability-Gruppe eine Rolle:

| Display name | Value | Allowed member types |
|---|---|---|
| `Reader` | `read` | Users/Groups |
| `Writer` | `write` | Users/Groups |
| `Sharer` | `share` | Users/Groups (optional) |

Zwei Rollen genügen für die meisten Setups: `Reader` und `Writer`. Der **Value** ist entscheidend, nicht der Anzeigename — er muss wörtlich der Capability-Gruppe entsprechen. Ein Benutzer mit beiden Rollen bekommt Lese- und Schreib-Tools.

Entra schreibt zugewiesene Rollen in den `roles`-Claim des Access-Tokens. Der Server kann daraus den Funktionsumfang ableiten, statt ihn pro Konto in einer Umgebungsvariable zu führen:

```dotenv
MCP_OIDC_CAPABILITY_CLAIM=roles
```

Die globale `FILEEE_CAPABILITIES`-Einstellung bleibt dabei die Obergrenze — eine Rolle kann nur freischalten, was global ohnehin erlaubt ist.

**Wichtig:** Sobald `MCP_OIDC_CAPABILITY_CLAIM` gesetzt ist, entscheidet der Claim allein. Ein Benutzer **ohne** zugewiesene Rolle bekommt dann `read`, nicht den konfigurierten Standardumfang — andernfalls wäre ein vergessener Rollen-Klick eine stille Rechteausweitung.

`destructive` lässt sich **nicht** über eine Rolle vergeben; der Server ignoriert einen solchen Wert im Claim. Fileees Hard-DELETE ist unwiderruflich und bleibt eine bewusste Entscheidung am Server (`FILEEE_CAPABILITIES` plus `FILEEE_ALLOW_DESTRUCTIVE=true`), keine Klick-Zuweisung im Portal.

## 4. Token-Version auf v2.0 stellen

*Manifest* öffnen und sicherstellen:

```json
"accessTokenAcceptedVersion": 2
```

Ohne diese Einstellung stellt Entra v1.0-Tokens mit abweichendem Issuer aus, und die Prüfung gegen `https://login.microsoftonline.com/<TENANT_ID>/v2.0` schlägt fehl.

## 5. Graph-Berechtigungen und Admin-Consent

*API permissions* → sicherstellen, dass die delegierten Microsoft-Graph-Berechtigungen `openid`, `profile` und **`offline_access`** vorhanden sind → **Grant admin consent**.

`offline_access` ist der Punkt, der leicht übersehen wird: ohne diese Berechtigung stellt Entra **kein Refresh-Token** aus. Der Connector funktioniert dann zunächst einwandfrei und bricht ab, sobald das erste Access-Token abläuft — ein Fehlerbild, das sich schwer auf die Ursache zurückführen lässt, weil zwischen Einrichtung und Symptom Stunden liegen.

## 6. Zugriff einschränken

*Enterprise applications → `<APP_SLUG>` → Properties* → **Assignment required = Yes**, danach unter *Users and groups* gezielt zuweisen. Bei Verwendung von App-Rollen (Abschnitt 3a) wird hier zugleich die Rolle vergeben.

Ohne diese Einstellung kann jeder Benutzer des Tenants den Connector verbinden.

## 7. Ermittelte Werte

| Wert | Pfad |
|---|---|
| Discovery | `https://login.microsoftonline.com/<TENANT_ID>/v2.0/.well-known/openid-configuration` |
| Issuer | `https://login.microsoftonline.com/<TENANT_ID>/v2.0` |
| JWKS | aus dem Discovery-Dokument (`jwks_uri`) |
| `aud` im v2-Token | `<CLIENT_ID>` |

Der Server akzeptiert **genau einen** Audience-Wert: `MCP_OIDC_AUDIENCE`. Gangways OIDC-Verifier (`github.com/coreos/go-oidc`) prüft, ob dieser eine konfigurierte Wert im `aud`-Claim des Tokens enthalten ist — es gibt **keine** zusätzliche Prüfung gegen `MCP_RESOURCE_URL` oder `api://<CLIENT_ID>`. `MCP_OIDC_AUDIENCE` muss deshalb exakt dem `aud`-Wert entsprechen, den Entra für das tatsächlich angeforderte Token ausstellt — bei einem v2.0-Token für den in Abschnitt 3 angelegten Scope ist das die reine `<CLIENT_ID>` (kein `api://`-Präfix, siehe Beispiel unten). Fordert ein Client stattdessen den Application-ID-URI-Scope an und Entra setzt dafür `api://<CLIENT_ID>` als `aud`, scheitert die Prüfung — sichtbar nur als 401-Schleife, die im Client als „Authorization failed" ankommt. In diesem Fall `MCP_OIDC_AUDIENCE` auf den tatsächlich ausgestellten Wert anpassen; Abschnitt 8 zeigt, wie man `aud` im ausgestellten Token nachsieht.

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

Entra liefert den **kurzen** Namen im `scp`-Claim (`mcp.access`). `MCP_OIDC_REQUIRED_SCOPES` erwartet ebenfalls die kurze Form und prüft sie gegen den `scp`-Claim des verifizierten Tokens (ersatzweise gegen einen `scope`-Claim, falls ein anderer Aussteller stattdessen diesen setzt).

Das Protected-Resource-Metadata-Dokument (`/.well-known/oauth-protected-resource`) veröffentlicht **keinen** Scope — das Feld `scopes_supported` fehlt in der Antwort dieses Servers vollständig, es wird von Gangway derzeit nicht befüllt. Ein Client kann den anzufordernden Scope also nicht aus diesem Dokument ablesen; er muss ihn kennen (z. B. aus dieser Anleitung oder der Connector-Konfiguration).

## 8. Verifikation

```bash
curl -s https://login.microsoftonline.com/<TENANT_ID>/v2.0/.well-known/openid-configuration \
  | jq '{issuer, code_challenge_methods_supported, token_endpoint}'
```

Nach dem ersten erfolgreichen Login das Token prüfen:

```bash
echo "<ACCESS_TOKEN>" | cut -d. -f2 | base64 -d 2>/dev/null | jq '{iss, aud, sub, oid, scp, roles, exp}'
```

Erwartet: `iss` endet auf `/v2.0`, `aud` = `<CLIENT_ID>`, `scp` enthält `mcp.access`. Bei Verwendung von App-Rollen enthält `roles` die zugewiesenen Werte — fehlt der Claim, ist die Rollenzuweisung aus Abschnitt 6 nicht erfolgt.

## 9. Bekannte Stolpersteine

| Symptom | Ursache | Behebung |
|---|---|---|
| `AADSTS9010010` beim Token-Request | `<MCP_URL>` ist nicht als Application ID URI registriert | Schritt 3 nachholen |
| `iss` endet nicht auf `/v2.0` | `accessTokenAcceptedVersion` steht nicht auf `2` | Manifest korrigieren |
| `AADSTS50011` (Redirect-URI-Mismatch) | Redirect-URI weicht ab, bei Claude Code der Port | exakte URI eintragen; Entra ignoriert den Port **nicht** |
| Connector funktioniert plötzlich nicht mehr | Client-Secret abgelaufen | neues Secret erzeugen und im Connector aktualisieren |
| Verbindung bricht nach der ersten Token-Laufzeit ab | `offline_access` fehlt, kein Refresh-Token | Schritt 5 nachholen, inklusive Admin-Consent |
| 401-Schleife, „audience mismatch" | `aud` im ausgestellten Token entspricht nicht `MCP_OIDC_AUDIENCE` (der Server prüft nur diesen einen Wert, siehe Abschnitt 7) | `accessTokenAcceptedVersion: 2` prüfen; `aud` im Token nachsehen (Abschnitt 8) und `MCP_OIDC_AUDIENCE` exakt darauf setzen |
| Write-Tools erscheinen nicht | App-Rolle nicht zugewiesen oder `MCP_OIDC_CAPABILITY_CLAIM` nicht gesetzt | Abschnitte 3a und 6 prüfen; `roles`-Claim im Token gegenprüfen |
| Jeder Tenant-Benutzer kommt durch | „Assignment required" steht auf `No` | Schritt 6 nachholen |
| Kein DCR möglich | Entra unterstützt weder Dynamic Client Registration noch Client ID Metadata Documents | vorregistrierte Client-ID/Secret im Connector-Dialog eintragen — der vorgesehene Weg |
