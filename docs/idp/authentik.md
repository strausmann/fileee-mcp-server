# Authentik als Authorization Server

`fileee-mcp-server` ist ein reiner OAuth-2.1-Resource-Server. Er stellt selbst keine Tokens aus, sondern verweist Clients über [RFC 9728](https://www.rfc-editor.org/rfc/rfc9728) auf einen externen Identity Provider und prüft die dort ausgestellten Tokens gegen dessen JWKS. Im Code steckt nichts Authentik-Spezifisches — diese Anleitung ist eine von mehreren möglichen Umsetzungen. Für Microsoft Entra ID siehe [`entra-id.md`](entra-id.md).

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
2. Authentik unterstützt PKCE mit `S256`; die Metadaten weisen `"code_challenge_methods_supported": ["S256"]` aus.
3. Discovery und Token-Endpunkt müssen innerhalb von 10 s antworten, Refresh innerhalb von 30 s.

## 1. Provider anlegen

*Admin Interface → Applications → Providers → Create → „OAuth2/OpenID Provider"*

| Feld | Wert | Warum |
|---|---|---|
| Name | `<APP_SLUG>-provider` | – |
| Authorization flow | expliziter Consent-Flow | Jede Verbindung soll eine bewusste Zustimmung erfordern; ein impliziter Flow verschleiert, welche Anwendung Zugriff erhält |
| Client type | `Confidential` | Der Client kann ein Secret führen. Bei Claude ist das Secret-Feld optional, ein vertraulicher Client ist trotzdem die sicherere Wahl |
| Client ID / Client Secret | generieren lassen, **notieren** | wird später im Client eingetragen |
| Redirect URIs | `https://claude.ai/api/mcp/auth_callback` | Callback der gehosteten Claude-Oberflächen |
| Redirect URIs (optional, für lokale Tests) | als Regex: `^http://(localhost\|127\.0\.0\.1)(:\d+)?/callback$` | Claude Code nutzt einen RFC-8252-Loopback-Redirect auf wechselndem Port; die Portangabe muss ignoriert werden |
| Signing Key | vorhandenes Zertifikat auswählen | Ohne Signing Key werden Tokens nicht asymmetrisch signiert und der Resource Server kann sie nicht über JWKS prüfen |
| Scopes | `openid`, `profile`, `email` | `email` nur nötig, wenn `MCP_OIDC_SUBJECT_CLAIM=email` verwendet wird |
| Access-Token-Gültigkeit | kurz, z. B. `minutes=30` | Clients refreshen reaktiv bei 401 und proaktiv kurz vor Ablauf |
| Refresh-Token-Gültigkeit | z. B. `days=30` | Ohne Refresh-Token muss die Verbindung regelmäßig neu aufgebaut werden |

## 2. Application anlegen und an eine Gruppe binden

*Applications → Create*, mit dem Provider verknüpfen, Slug `<APP_SLUG>`.

**Pflicht:** Unter *Policy / Group / User Bindings* eine Gruppenbindung setzen. Ohne Bindung darf jeder Account der Instanz den Connector nutzen und damit auf die dahinterliegenden Dokumente zugreifen. Empfohlen ist eine eigene Gruppe, z. B. `<APP_SLUG>-users`.

Authentik trennt Provider und Application: Ein Provider ohne verknüpfte Application ist über die Discovery-URL nicht erreichbar.

## 3. Ermittelte Werte

| Wert | Pfad |
|---|---|
| Discovery | `https://<IDP_HOST>/application/o/<APP_SLUG>/.well-known/openid-configuration` |
| JWKS | `https://<IDP_HOST>/application/o/<APP_SLUG>/jwks/` |
| `MCP_OIDC_ISSUER` | der `issuer`-Wert aus dem Discovery-Dokument |
| `MCP_OIDC_AUDIENCE` | die Client-ID des Providers |

## 4. Audience-Verhalten prüfen

Authentik setzt `aud` im Standardfall auf die **Client-ID**. Clients senden zusätzlich `resource=<MCP_URL>` ([RFC 8707](https://www.rfc-editor.org/rfc/rfc8707)). Ob Authentik diesen Parameter verarbeitet, ignoriert oder ablehnt, ist nicht dokumentiert und muss vor dem Rollout gegen ein echtes Token geprüft werden.

```bash
# Token über den Authorization-Code-Flow beziehen, dann die Claims ansehen:
echo "<ACCESS_TOKEN>" | cut -d. -f2 | base64 -d 2>/dev/null | jq '{iss, aud, sub, exp, scope}'
```

| Ergebnis | Bewertung | Vorgehen |
|---|---|---|
| `aud` = Client-ID | Normalfall | Der Server akzeptiert `MCP_OIDC_AUDIENCE` — funktionsfähig, aber ohne Resource-Bindung |
| `aud` enthält `<MCP_URL>` | ideal | Nichts weiter nötig; `MCP_RESOURCE_URL` wird ohnehin immer als gültige Audience akzeptiert |
| Token-Request scheitert wegen `resource` | Blocker | Resource-Bindung nachrüsten, siehe unten |

### Resource-Bindung nachrüsten (empfohlen)

Ohne Resource-Bindung validiert **jedes** Token desselben Clients gegen diesen Server — auch eines, das für eine andere Anwendung ausgestellt wurde. Das ist die klassische Confused-Deputy-Situation, die RFC 8707 verhindern soll.

Abhilfe über ein *Scope Mapping*: *Customization → Property Mappings → Create → Scope Mapping*, Scope-Name z. B. `mcp`, Expression:

```python
return {
    "aud": ["<CLIENT_ID>", "<MCP_URL>"],
}
```

Das Mapping anschließend im Provider unter *Scopes* auswählen. Beide Werte in der Liste zu führen hält bestehende Clients funktionsfähig.

> Dieser Weg ist **nicht offiziell dokumentiert**. Nach dem Anlegen erneut ein Token ziehen und die Claims prüfen.

## 4a. Gruppen für den Funktionsumfang (optional, empfohlen)

Der Funktionsumfang lässt sich statt in der Server-Konfiguration im IdP verwalten. Dazu eine Gruppe je Capability-Gruppe anlegen und den `groups`-Claim ins Token aufnehmen — in Authentik über das mitgelieferte Scope-Mapping `groups`, das im Provider unter *Scopes* auszuwählen ist.

| Gruppenname | Wirkung |
|---|---|
| `read` | Lese-Tools |
| `write` | zusätzlich Upload und Änderungen |
| `share` | zusätzlich Freigaben und Export (optional) |

Zwei Gruppen genügen für die meisten Setups: `read` und `write`. Der Gruppenname muss wörtlich der Capability-Gruppe entsprechen; wer Präfixe braucht, kann sie über eine eigene Scope-Mapping-Expression abschneiden.

```dotenv
MCP_OIDC_CAPABILITY_CLAIM=groups
```

Die globale `FILEEE_CAPABILITIES`-Einstellung bleibt die Obergrenze — eine Gruppe kann nur freischalten, was global ohnehin erlaubt ist.

**Wichtig:** Sobald `MCP_OIDC_CAPABILITY_CLAIM` gesetzt ist, entscheidet der Claim allein. Wer in keiner der Gruppen ist, bekommt dann `read`, nicht den konfigurierten Standardumfang — andernfalls wäre eine vergessene Gruppenmitgliedschaft eine stille Rechteausweitung.

`destructive` lässt sich **nicht** über eine Gruppe vergeben; der Server ignoriert einen solchen Wert im Claim. Fileees Hard-DELETE ist unwiderruflich und bleibt eine bewusste Entscheidung am Server (`FILEEE_CAPABILITIES` plus `FILEEE_ALLOW_DESTRUCTIVE=true`).

Achtung: Der `groups`-Claim enthält **alle** Gruppen des Benutzers, nicht nur die für diese Anwendung relevanten. Der Server wertet deshalb nur exakt die konfigurierten Namen aus und ignoriert den Rest.

## 5. Werte in den MCP-Server

```dotenv
MCP_AUTH_MODE=oidc
MCP_OIDC_ISSUER=https://<IDP_HOST>/application/o/<APP_SLUG>/
MCP_OIDC_AUDIENCE=<CLIENT_ID>
MCP_OIDC_SUBJECT_CLAIM=sub
MCP_RESOURCE_URL=<MCP_URL>
MCP_ALLOWED_SUBJECTS=<sub-Wert des berechtigten Benutzers>
```

`MCP_ALLOWED_SUBJECTS` ist im `single`-Modus Pflicht — der Serverstart bricht sonst ab. Den `sub`-Wert liefert das Token aus Schritt 4.

## 6. Verifikation

```bash
curl -s https://<IDP_HOST>/application/o/<APP_SLUG>/.well-known/openid-configuration \
  | jq '{issuer, code_challenge_methods_supported, scopes_supported, token_endpoint}'
```

Erwartet: `code_challenge_methods_supported` enthält `S256`. Erscheint `offline_access` in `scopes_supported`, fordert Claude es automatisch mit an und erhält ein Refresh-Token.

Erreichbarkeit von außen — der IdP muss ohne Umleitung und ohne HTML antworten:

```bash
curl -sI https://<IDP_HOST>/application/o/<APP_SLUG>/.well-known/openid-configuration
```

Erwartet `200` mit `Content-Type: application/json`. Ein `302` auf eine Login-Seite oder eine HTML-Antwort bedeutet, dass ein Proxy davorsteht — der Flow bricht dann ab, ohne dass der MCP-Server je Traffic sieht.

## 7. Bekannte Stolpersteine

| Symptom | Ursache | Behebung |
|---|---|---|
| Client kann keinen Client registrieren | Authentik unterstützt kein Dynamic Client Registration ([Issue #8751](https://github.com/goauthentik/authentik/issues/8751)) | Unkritisch — Client-ID und Secret manuell im Connector-Dialog eintragen |
| Resource Server kann Token nicht prüfen | kein Signing Key im Provider gesetzt | Signing Key nachtragen, Token neu ziehen |
| Discovery-URL liefert 404 | Provider ohne verknüpfte Application | Application anlegen und verknüpfen |
| Jeder Account kommt durch | Gruppenbindung fehlt | Schritt 2 nachholen — häufigster Konfigurationsfehler |
