# Authentik als Authorization Server

`fileee-mcp-server` ist ein reiner OAuth-2.1-Resource-Server. Er stellt selbst keine Tokens aus, sondern verweist Clients über [RFC 9728](https://www.rfc-editor.org/rfc/rfc9728) auf einen externen Identity Provider und prüft die dort ausgestellten Tokens gegen dessen JWKS. Im Code steckt nichts Authentik-Spezifisches — diese Anleitung ist eine von mehreren möglichen Umsetzungen. Für Microsoft Entra ID siehe [`entra-id.md`](entra-id.md).

Die Angaben unten sind gegen **Authentik 2026.2** verifiziert, teils über die API, teils am Quelltext der Version.

## Platzhalter

| Platzhalter | Bedeutung | Beispiel |
|---|---|---|
| `<MCP_URL>` | vollständige MCP-Endpunkt-URL, exakt wie sie im Client eingetragen wird | `https://mcp.example.com/mcp` |
| `<MCP_HOST>` | nur der Host davon | `mcp.example.com` |
| `<IDP_HOST>` | Host der Authentik-Instanz | `idp.example.com` |
| `<APP_SLUG>` | Slug der Anwendung in Authentik | `fileee-mcp` |
| `<CLIENT_ID>` / `<CLIENT_SECRET>` | Zugangsdaten des OAuth-Clients | – |

## Voraussetzungen

1. Die Authentik-Instanz muss aus dem Egress-Bereich des MCP-Clients erreichbar sein. Bei Claude ist das `160.79.104.0/21` — die Discovery-Requests an den IdP kommen aus derselben Range wie die an den MCP-Server. Kein vorgelagerter SSO-Zwang, keine WAF, kein Bot-Schutz und kein Geo-Block vor den `/.well-known/`-Pfaden.
2. Authentik unterstützt PKCE mit `S256`.
3. Discovery und Token-Endpunkt müssen innerhalb von 10 s antworten, Refresh innerhalb von 30 s.

## 1. Provider anlegen

*Admin Interface → Applications → Providers → Create → „OAuth2/OpenID Provider"*

| Feld | Wert | Warum |
|---|---|---|
| Name | `<APP_SLUG>-provider` | – |
| Authorization flow | expliziter Consent-Flow (`default-provider-authorization-explicit-consent`) | Jede Verbindung soll eine bewusste Zustimmung erfordern; ein impliziter Flow verschleiert, welche Anwendung Zugriff erhält |
| Invalidation flow | `default-provider-invalidation-flow` | Seit Authentik 2024.10 **Pflichtfeld**; ohne Wert schlägt das Anlegen über die API fehl |
| Client type | `Confidential` | Der Client kann ein Secret führen. Bei Claude ist das Secret-Feld optional, ein vertraulicher Client ist trotzdem die sicherere Wahl |
| Client ID / Client Secret | generieren lassen, **notieren** | wird später im Client eingetragen |
| Redirect URIs | `https://claude.ai/api/mcp/auth_callback` | Callback der gehosteten Claude-Oberflächen |
| Redirect URIs (optional, für lokale Tests) | Regex: `^http://(localhost\|127\.0\.0\.1)(:\d+)?/callback$` | Claude Code nutzt einen RFC-8252-Loopback-Redirect auf wechselndem Port; die Portangabe muss ignoriert werden |
| Redirect URIs (nur bei `FILEEE_MODE=self-service`) | `https://<mcp-host>/setup/callback` | Callback der Setup-Seite, auf der Benutzer ihre Fileee-Zugangsdaten selbst hinterlegen — siehe [ADR-0014](../adr/0014-self-service-onboarding.md) |
| Signing Key | vorhandenes Zertifikat auswählen | Ohne Signing Key werden Tokens nicht asymmetrisch signiert und der Resource Server kann sie nicht über JWKS prüfen |
| Scopes | `openid`, `profile`, `email`, **`offline_access`** | siehe Kasten unten — ohne `offline_access` gibt es kein Refresh-Token |
| Access-Token-Gültigkeit | kurz, z. B. `minutes=30` | Clients refreshen reaktiv bei 401 und proaktiv kurz vor Ablauf |
| Refresh-Token-Gültigkeit | z. B. `days=30` | wirkt nur zusammen mit dem `offline_access`-Mapping |

> **`offline_access` ist nicht optional.** Authentik gibt ein Refresh-Token ausschließlich aus, wenn `offline_access` im angeforderten Scope steht, und veröffentlicht den Scope nur dann in `scopes_supported`, wenn das zugehörige Mapping am Provider hängt. Claude fordert den Scope wiederum nur an, wenn er in `scopes_supported` auftaucht. Fehlt das Mapping, ist eine noch so großzügige Refresh-Token-Gültigkeit wirkungslos — die Verbindung bricht nach der Access-Token-Laufzeit ab und muss neu aufgebaut werden.

**Bei Einrichtung über die API:** `redirect_uris` ist seit Authentik 2024.6 kein String mehr, sondern eine Liste von Objekten:

```json
"redirect_uris": [
  {"matching_mode": "strict", "url": "https://claude.ai/api/mcp/auth_callback"},
  {"matching_mode": "strict", "url": "https://<mcp-host>/setup/callback"},
  {"matching_mode": "regex",  "url": "^http://(localhost|127\\.0\\.0\\.1)(:\\d+)?/callback$"}
]
```

**Zwei Rollen, ein Provider.** Bei `FILEEE_MODE=self-service` nutzt derselbe Provider zwei Callbacks: den von Claude für den MCP-Zugriff und den der Setup-Seite des Servers. Deren Client-Rolle ist eine andere — sie ist Relying Party und verwendet das Client-Secret des Providers als `SETUP_OIDC_CLIENT_SECRET`. Der MCP-Endpunkt bleibt davon unberührt reiner Resource Server; die Unterscheidung ist in [`claude-connector.md`](claude-connector.md) und [ADR-0014](../adr/0014-self-service-onboarding.md) beschrieben.

## 2. Application anlegen und an eine Gruppe binden

*Applications → Create*, mit dem Provider verknüpfen, Slug `<APP_SLUG>`.

**Pflicht:** Unter *Policy / Group / User Bindings* eine Gruppenbindung setzen. Ohne Bindung darf jeder Account der Instanz den Connector nutzen und damit auf die dahinterliegenden Dokumente zugreifen. Empfohlen ist eine eigene Gruppe, z. B. `<APP_SLUG>-users`.

Über die API läuft das über **`/api/v3/policies/bindings/`** (`target` = Application-UUID, `group` = Gruppen-UUID) — nicht über `/api/v3/core/policybindings/`, diesen Pfad gibt es nicht.

Wirksamkeit prüfen: `GET /api/v3/core/applications/` darf die Anwendung für einen Benutzer außerhalb der Gruppe **nicht** mehr zurückliefern. Erscheint sie nur mit `superuser_full_list=true`, greift die Bindung.

Authentik trennt Provider und Application: Ein Provider ohne verknüpfte Application ist über die Discovery-URL nicht erreichbar.

> **Wechselwirkung mit `client_credentials`:** Die Gruppenbindung blockiert auch den Machine-to-Machine-Grant. Authentik legt beim ersten `client_credentials`-Request automatisch einen Service-Account `ak-<provider-name>-client_credentials` an und prüft ihn anschließend gegen dieselbe Policy — ohne Gruppenmitgliedschaft bekommt er `invalid_grant`. Wer M2M-Zugriff braucht, nimmt diesen Account ausdrücklich in die Gruppe auf. Zu beachten: **schon ein fehlgeschlagener Versuch legt den Benutzer an**, weil er vor der Policy-Prüfung erzeugt wird.

## 3. Ermittelte Werte

| Wert | Pfad |
|---|---|
| Discovery | `https://<IDP_HOST>/application/o/<APP_SLUG>/.well-known/openid-configuration` |
| JWKS | `https://<IDP_HOST>/application/o/<APP_SLUG>/jwks/` |
| `MCP_OIDC_ISSUER` | der `issuer`-Wert aus dem Discovery-Dokument |
| `MCP_OIDC_AUDIENCE` | die Client-ID des Providers |

## 4. Audience-Verhalten

**Das Ergebnis steht fest, es muss nicht mehr ermittelt werden** (verifiziert gegen Authentik 2026.2, API und Quelltext):

| Frage | Antwort |
|---|---|
| Wie füllt Authentik `aud`? | Hart mit der **Client-ID**. `authentik/providers/oauth2/id_token.py` setzt `id_token.aud = provider.client_id`; einen anderen Codepfad gibt es nicht. Der Access-Token nutzt denselben JWT-Aufbau. |
| Wie reagiert Authentik auf `resource` ([RFC 8707](https://www.rfc-editor.org/rfc/rfc8707))? | Es **ignoriert** den Parameter — weder Verarbeitung noch Fehler. Ein bewusst ungültiger Wert wie `resource=not-a-uri` müsste nach RFC 8707 §2 zu `invalid_target` führen; Authentik antwortet stattdessen mit dem normalen Redirect. In `views/token.py` und `views/authorize.py` kommen `resource` und `audience` nicht vor. |
| Ändert sich das absehbar? | Nein. Im noch nicht veröffentlichten `main`-Branch taucht der Parameter erstmals auf — und zwar als ausdrückliche **Ablehnung** mit `invalid_target` im RFC-8693-Token-Exchange. |

**Konsequenz:** Der MCP-Server funktioniert ohne weitere Maßnahme, weil er `MCP_OIDC_AUDIENCE` als gültige Audience akzeptiert. Eine Resource-Bindung im Sinne von RFC 8707 gibt es damit aber nicht.

### Resource-Bindung nachrüsten (empfohlen)

Ohne Resource-Bindung validiert **jedes** Token desselben Clients gegen diesen Server — auch eines, das für eine andere Anwendung ausgestellt wurde. Das ist die klassische Confused-Deputy-Situation, die RFC 8707 verhindern soll. Der Handlungsbedarf steigt mit der Zahl der Anwendungen auf derselben Instanz.

Abhilfe über ein *Scope Mapping*: *Customization → Property Mappings → Create → Scope Mapping*, Scope-Name z. B. `mcp`, Expression:

```python
return {
    "aud": ["<CLIENT_ID>", "<MCP_URL>"],
}
```

Das Mapping anschließend im Provider unter *Scopes* auswählen. Beide Werte in der Liste zu führen hält bestehende Clients funktionsfähig.

> Dieser Weg ist **nicht offiziell dokumentiert**. Nach dem Anlegen ein Token ziehen und die Claims prüfen.

## 5. Werte in den MCP-Server

```dotenv
MCP_AUTH_MODE=oidc
MCP_OIDC_ISSUER=https://<IDP_HOST>/application/o/<APP_SLUG>/
MCP_OIDC_AUDIENCE=<CLIENT_ID>
MCP_OIDC_SUBJECT_CLAIM=sub
MCP_RESOURCE_URL=<MCP_URL>
MCP_ALLOWED_SUBJECTS=<sub-Wert des berechtigten Benutzers>
```

`MCP_ALLOWED_SUBJECTS` ist im `single`-Modus Pflicht — der Serverstart bricht sonst ab.

**`sub` ohne Login ermitteln:** Steht der Provider auf `sub_mode=hashed_user_id` (Standard), ist `sub` identisch mit dem `uid`-Feld des Benutzers. Damit erspart man sich den Umweg über einen Token:

```bash
curl -s -H "Authorization: Bearer <API_TOKEN>" \
  "https://<IDP_HOST>/api/v3/core/users/?username=<benutzer>" | jq -r '.results[].uid'
```

Der Wert ist ein 64-stelliger Hex-String und über die Lebensdauer des Kontos stabil. Bei einem abweichenden `sub_mode` gilt das nicht — dann bleibt nur der Weg über ein echtes Token.

## 6. Verifikation

```bash
curl -s https://<IDP_HOST>/application/o/<APP_SLUG>/.well-known/openid-configuration \
  | jq '{issuer, code_challenge_methods_supported, scopes_supported, token_endpoint}'
```

Erwartet:

- `code_challenge_methods_supported` enthält `S256`. Authentik bietet daneben auch `plain` an — das ist unkritisch, weil Claude ohnehin immer `S256` sendet, aber ein eigener Client muss `S256` selbst durchsetzen.
- `scopes_supported` enthält `offline_access`. Fehlt es, ist das Mapping aus Abschnitt 1 nicht gesetzt und es gibt keine Refresh-Tokens.

Erreichbarkeit von außen — der IdP muss ohne Umleitung und ohne HTML antworten:

```bash
curl -sI https://<IDP_HOST>/application/o/<APP_SLUG>/.well-known/openid-configuration
```

Erwartet `200` mit `Content-Type: application/json`. Ein `302` auf eine Login-Seite oder eine HTML-Antwort bedeutet, dass ein Proxy davorsteht — der Flow bricht dann ab, ohne dass der MCP-Server je Traffic sieht.

**Ein echtes Token braucht einen Browser-Login.** Alle browserlosen Wege sind versperrt: `client_credentials` scheitert an der Gruppenbindung aus Abschnitt 2, `password` und `device_code` brauchen Benutzerkredentiale, und Authentik unterstützt kein Dynamic Client Registration. Für die Konfiguration ist das kein Hindernis — `aud` und `sub` sind wie oben beschrieben auch ohne Token bestimmbar.

## 7. Bekannte Stolpersteine

| Symptom | Ursache | Behebung |
|---|---|---|
| Verbindung bricht nach der Access-Token-Laufzeit ab | `offline_access`-Mapping fehlt am Provider, kein Refresh-Token | Mapping in Abschnitt 1 nachtragen und in `scopes_supported` gegenprüfen |
| Jeder Account kommt durch | Gruppenbindung fehlt | Abschnitt 2 nachholen — häufigster Konfigurationsfehler |
| Niemand kommt durch, Gruppe existiert | Gruppe ist leer | Benutzer aufnehmen |
| `invalid_grant` bei `client_credentials` | Gruppenbindung blockiert den autogenerierten Service-Account | Account in die Gruppe aufnehmen oder auf M2M verzichten |
| Unerwarteter Benutzer `ak-…-client_credentials` in der Instanz | Authentik legt ihn beim ersten M2M-Versuch an, auch bei Fehlschlag | Kann gelöscht werden, wenn M2M nicht gebraucht wird |
| Resource Server kann Token nicht prüfen | kein Signing Key im Provider gesetzt | Signing Key nachtragen, Token neu ziehen |
| Discovery-URL liefert 404 | Provider ohne verknüpfte Application | Application anlegen und verknüpfen |
| `POST /providers/oauth2/` schlägt fehl | `invalidation_flow` fehlt | Pflichtfeld seit 2024.10, siehe Abschnitt 1 |
| Client kann keinen Client registrieren | Authentik unterstützt kein Dynamic Client Registration ([Issue #8751](https://github.com/goauthentik/authentik/issues/8751)) | Unkritisch — Client-ID und Secret manuell im Connector-Dialog eintragen |
