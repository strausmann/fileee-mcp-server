# Beliebiger OpenID-Connect-Anbieter als Authorization Server

`fileee-mcp-server` ist ein reiner OAuth-2.1-Resource-Server. Er stellt selbst keine Tokens aus, sondern verweist Clients über [RFC 9728](https://www.rfc-editor.org/rfc/rfc9728) auf einen externen Identity Provider und prüft die dort ausgestellten Tokens gegen dessen JWKS.

Diese Anleitung gilt für **jeden standardkonformen OpenID-Connect-Anbieter ohne eigenen Zweig** — GitLab, Keycloak, Auth0, Google, Okta und andere. Für die beiden Anbieter mit eigener Anleitung siehe [`entra-id.md`](entra-id.md) und [`authentik.md`](authentik.md); dort trägst du weniger ein, weil der Server die Aussteller-URL aus anbieterspezifischen Angaben ableitet.

`generic` ist kein Notbehelf, sondern ein gleichrangiger Weg. Es ist allerdings **nicht** dafür gedacht, Sonderfälle von Entra oder Authentik zu umgehen — wer diese Anbieter nutzt, wählt sie auch aus und bekommt ihre Prüfungen.

## Platzhalter

| Platzhalter | Bedeutung | Beispiel |
|---|---|---|
| `<MCP_URL>` | vollständige MCP-Endpunkt-URL, exakt wie sie im Client eingetragen wird | `https://mcp.example.com/mcp` |
| `<ISSUER>` | der `issuer`-Wert aus dem Discovery-Dokument | `https://gitlab.example.com` |
| `<CLIENT_ID>` / `<CLIENT_SECRET>` | Kennung und Geheimnis der beim Anbieter angelegten Anwendung | – |

## Voraussetzungen

Der Anbieter muss vier Dinge erfüllen. Fehlt eines davon, funktioniert die Anbindung nicht:

1. **Discovery-Dokument** unter `<ISSUER>/.well-known/openid-configuration`, erreichbar ohne Anmeldung.
2. **JWKS**, in diesem Dokument als `jwks_uri` ausgewiesen.
3. **PKCE mit `S256`** — Pflicht für OAuth 2.1.
4. **Erreichbarkeit aus dem Egress-Bereich des Clients.** Bei Claude ist das `160.79.104.0/21`; ein Anbieter im privaten Netz ist von dort nicht erreichbar.

Antwortzeiten: Discovery und Token-Endpunkt innerhalb von 10 s, Refresh innerhalb von 30 s. Ein Reverse Proxy, der Antworten puffert, erzeugt sporadische Verbindungsfehler.

## 1. Anwendung beim Anbieter anlegen

Lege eine Anwendung (je nach Anbieter „Application", „Client" oder „App Registration") mit diesen Eigenschaften an:

| Eigenschaft | Wert |
|---|---|
| Typ | Vertraulich (Confidential) — der Client kann ein Geheimnis aufbewahren |
| Grant | Authorization Code mit PKCE |
| Redirect-URI | `https://claude.ai/api/mcp/auth_callback` |
| Scopes | mindestens `openid`; für dauerhafte Verbindungen zusätzlich `offline_access` |

Ohne `offline_access` bricht die Verbindung ab, sobald das erste Zugriffstoken abläuft — es gibt dann kein Erneuerungs-Token.

Für Claude Code statt der Web-Oberfläche gilt eine andere Rückruf-Adresse; siehe [`claude-connector.md`](claude-connector.md).

## 2. Aussteller und Audience ermitteln

Das Discovery-Dokument abrufen und den `issuer`-Wert übernehmen — **wörtlich**, einschließlich oder ohne abschließenden Schrägstrich, genau wie er dort steht:

```bash
curl -s https://<host>/.well-known/openid-configuration | jq -r '.issuer, .jwks_uri'
```

> Der Wert im Feld `issuer` ist maßgeblich, **nicht** die Adresse, unter der du das Dokument abgerufen hast. Manche Anbieter liefern hier etwas anderes zurück, als man erwartet — etwa eine kanonische Form ohne Port oder mit anderem Host. Weicht der eingetragene Wert davon ab, scheitert jede Prüfung mit „issuer mismatch".

Als Audience erwartet der Server die **Client-ID**. Prüfe nach dem ersten Login im ausgestellten Token, ob `aud` diesen Wert trägt (Abschnitt 5) — manche Anbieter setzen stattdessen eine eigens konfigurierte Kennung.

## 3. Zugriff einschränken

Im `single`-Modus bedient der Server genau **ein** Fileee-Konto. Die Liste der zugelassenen Subjects ist damit die einzige Stufe, die entscheidet, wer an dessen Dokumente kommt — ohne sie käme jeder authentifizierte Benutzer des Anbieters durch. Der Serverstart bricht deshalb ab, wenn `MCP_ALLOWED_SUBJECTS` fehlt.

Zusätzlich, soweit der Anbieter es unterstützt: die Anwendung auf eine Gruppe oder eine ausdrückliche Zuweisung beschränken. Beide Stufen zusammen sind belastbarer als eine.

## 4. Werte in den MCP-Server

```dotenv
MCP_AUTH_MODE=oidc
MCP_OIDC_PROVIDER=generic
MCP_OIDC_ISSUER=<ISSUER>
MCP_OIDC_CLIENT_ID=<CLIENT_ID>
MCP_OIDC_SUBJECT_CLAIM=sub
MCP_RESOURCE_URL=<MCP_URL>
MCP_ALLOWED_SUBJECTS=<sub-Wert des berechtigten Benutzers>
```

`MCP_OIDC_SUBJECT_CLAIM` bestimmt, welcher Claim als Identität gilt. `sub` ist die richtige Wahl, solange der Anbieter ihn stabil vergibt. Vorsicht bei `email`: der Wert ist änderbar und steht je nach Konfiguration gar nicht im Token.

**Variablen anderer Anbieter dürfen nicht gesetzt sein.** Stehen neben diesen Werten noch `MCP_ENTRA_*` oder `MCP_AUTHENTIK_*` aus einer früheren Konfiguration, bricht der Start mit deren Namen ab — sie würden sonst wirkungslos gesetzt herumliegen.

## 5. Verifikation

Vor dem ersten Login:

```bash
curl -i -X POST <MCP_URL>
# erwartet: 401 + WWW-Authenticate: Bearer resource_metadata="…"

curl -s https://<MCP_HOST>/.well-known/oauth-protected-resource | jq
# erwartet: "resource" ist exakt <MCP_URL>, "authorization_servers"[0] ist <ISSUER>
```

Nach dem ersten Login das Token prüfen:

```bash
echo "<ACCESS_TOKEN>" | cut -d. -f2 | base64 -d 2>/dev/null | jq '{iss, aud, sub, exp}'
```

Erwartet: `iss` ist exakt `<ISSUER>`, `aud` enthält `<CLIENT_ID>`, `sub` steht in `MCP_ALLOWED_SUBJECTS`.

Zuletzt ein Negativtest: Ein Konto, das **nicht** in `MCP_ALLOWED_SUBJECTS` steht, darf nicht durchkommen. Die Ablehnung sieht dabei nicht wie ein `403` aus, sondern wie ein fehlgeschlagenes Werkzeugergebnis über HTTP 200 — der Token ist gültig, erst die Konto-Auflösung schlägt fehl. Siehe [`claude-connector.md`](claude-connector.md), Abschnitt Negativtest.

## 6. Bekannte Stolpersteine

| Symptom | Ursache | Behebung |
|---|---|---|
| „issuer mismatch" | eingetragener Wert weicht vom `issuer` im Discovery-Dokument ab, oft nur im abschließenden Schrägstrich | Wert wörtlich aus dem Dokument übernehmen (Abschnitt 2) |
| 401-Schleife, „audience mismatch" | `aud` im Token ist nicht die Client-ID | `aud` im Token nachsehen (Abschnitt 5); manche Anbieter verlangen eine eigens konfigurierte Audience |
| Verbindung bricht nach der ersten Token-Laufzeit ab | `offline_access` fehlt, kein Erneuerungs-Token | Scope ergänzen und neu verbinden |
| Start bricht ab: „gesetzt sind auch Variablen anderer Anbieter" | `MCP_ENTRA_*`- oder `MCP_AUTHENTIK_*`-Reste in der Konfiguration | entfernen (Abschnitt 4) |
| Start bricht ab: „MCP_ALLOWED_SUBJECTS ist Pflicht" | im `single`-Modus fehlt die Freigabeliste | Abschnitt 3 |
| Login schlägt fehl, der Server sieht keine Anfrage | Redirect-URI weicht ab | exakt `https://claude.ai/api/mcp/auth_callback` eintragen |

## Weiter

[`claude-connector.md`](claude-connector.md) — Eintragen im Client, Zeitgrenzen, Negativtest.
