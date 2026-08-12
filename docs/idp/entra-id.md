# Microsoft Entra ID als Authorization Server

`fileee-mcp-server` ist ein reiner OAuth-2.1-Resource-Server. Er stellt selbst keine Tokens aus, sondern verweist Clients über [RFC 9728](https://www.rfc-editor.org/rfc/rfc9728) auf einen externen Identity Provider und prüft die dort ausgestellten Tokens gegen dessen JWKS. Für Authentik siehe [`authentik.md`](authentik.md), für jeden anderen standardkonformen Anbieter [`generic.md`](generic.md).

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
4. **Entra unterstützt keine automatische Client-Registrierung** — weder Dynamic Client Registration (RFC 7591) noch Client ID Metadata Documents. claude.ai meldet das nach dem Verbindungsversuch als eigene Fehlermeldung: „Automatische Client-Registrierung wird von `<APP_SLUG>` nicht unterstützt. Bearbeite den Connector und füge eine OAuth-Client-ID hinzu." (live beobachtet 09.08.2026). Nachprüfbar am Discovery-Dokument: Das Feld `registration_endpoint` fehlt darin, und der bei manchen Anbietern zusätzlich angebotene Pfad `/.well-known/oauth-authorization-server` liefert bei Entra **404** — `/.well-known/openid-configuration` ist der einzige gültige Discovery-Pfad. Es gibt keinen Weg darum herum: Client-ID (und ggf. Secret) müssen vorregistriert und im Connector-Dialog eingetragen werden (Schritte 1–2 unten, Eintragen in claude.ai siehe [`claude-connector.md`](claude-connector.md)).

## Reihenfolge im Überblick

Wer die folgende Reihenfolge abarbeitet, kommt ohne Fehlermeldung durch. Jede Zeile ist ein real durchlaufener Fehler — der Fehlercode entsteht, wenn genau dieser Schritt fehlt oder fehlerhaft ist, unabhängig davon, ob die übrigen Schritte schon stimmen. Die Spalte „Ort" trennt, wo der jeweilige Schritt stattfindet: im Entra-Portal, in Infisical (bzw. der Server-Konfiguration allgemein) oder in claude.ai selbst.

| # | Ort | Schritt | Bei Überspringen/Fehler |
|---|---|---|---|
| 1 | Entra-Portal | App-Registrierung anlegen, Redirect-URI eintragen (Abschnitt 1) | `AADSTS500113` |
| 2 | Entra-Portal | Client-Secret erzeugen, falls Confidential Client (Abschnitt 2) | `AADSTS7000218` |
| 3 | Entra-Portal | Application ID URI auf `<MCP_URL>` setzen (Abschnitt 3) | `AADSTS9010010` |
| 4 | Entra-Portal | Scope `mcp.access` anlegen (Abschnitt 3) | `AADSTS650053` |
| 5 | Entra-Portal | Eigene Berechtigung für den Scope erteilen, Admin-Consent (Abschnitt 3) | `AADSTS65001` |
| 6 | Entra-Portal | Token-Version auf `2` stellen (Abschnitt 4) | `401` vom Server, ohne Begründung im Protokoll |
| 7 | Entra-Portal | `offline_access` unter den Graph-Berechtigungen, Admin-Consent (Abschnitt 5) | Verbindung bricht erst nach der ersten Token-Laufzeit ab |
| 8 | Entra-Portal | Zugriff einschränken, Assignment (Abschnitt 6) | jeder Benutzer des Tenants kommt durch |
| 9 | Infisical / Server-Konfiguration | Ermittelte Werte eintragen, inklusive `MCP_OIDC_ADVERTISED_SCOPES` (Abschnitt 7) | Consent-Dialog zeigt nur `openid`, `profile`, `offline_access`; Token-Request scheitert mit `AADSTS650053` |
| 10 | claude.ai | Connector eintragen ([`claude-connector.md`](claude-connector.md)) | „Automatische Client-Registrierung wird … nicht unterstützt" |

## 1. App-Registrierung anlegen

*Microsoft Entra Admin Center → App registrations → New registration*

| Feld | Wert |
|---|---|
| Name | `<APP_SLUG>` |
| Supported account types | „Accounts in this organizational directory only" (Single-Tenant), sofern kein Multi-Tenant-Zugriff gewünscht ist |
| Redirect URI | Plattform **Web**, `https://claude.ai/api/mcp/auth_callback` |

Nach dem Anlegen notieren: **Application (client) ID** = `<CLIENT_ID>`, **Directory (tenant) ID** = `<TENANT_ID>`.

Für lokale Tests mit Claude Code zusätzlich unter *Authentication → Add a platform → Mobile and desktop applications* die Loopback-URIs eintragen. Entra prüft Redirect-URIs exakt, **inklusive Port** — für jeden zu testenden Port ist ein eigener Eintrag nötig. Wer darauf verzichten kann, testet nur über die gehosteten Claude-Oberflächen.

> **Nicht verwechseln: zwei verschiedene Felder wollen am Ende eine Adresse desselben Hosts.** Sowohl hier unter *Authentication* als auch weiter unten unter *Expose an API* (Abschnitt 3) trägst du eine URL mit dem MCP-Host ein — es ist aber nicht dieselbe Adresse und nicht dasselbe Feld:
>
> | Feld | Menü | Gehört dorthin | Fehler bei fehlendem/falschem Eintrag |
> |---|---|---|---|
> | Redirect URI | *Authentication → Web* | die **Rückrufadresse von claude.ai**: `https://claude.ai/api/mcp/auth_callback` | `AADSTS500113` |
> | Application ID URI | *Expose an API* | die **eigene MCP-Server-Adresse** `<MCP_URL>` | `AADSTS9010010` (Abschnitt 3) |
>
> Live beobachtet am 09.08.2026: Unter *Authentication* stand versehentlich `<MCP_URL>` eingetragen — dort gehört sie nicht hin, das Anmelden bricht dann mit `AADSTS500113` (keine Weiterleitungsadresse registriert) ab. Wegen der Namensähnlichkeit beider Felder ist die Verwechslung naheliegend.

## 2. Client-Secret erzeugen

*Certificates & secrets → New client secret*

Gültigkeit bewusst wählen, Wert **sofort** notieren (`<CLIENT_SECRET>`) — er ist danach nicht mehr abrufbar. Ablaufdatum vormerken: ein abgelaufenes Secret äußert sich als plötzlich nicht mehr funktionierender Connector, ohne Änderung am Server.

## 3. API exponieren

*Expose an API*

### Application ID URI

Entra schlägt standardmäßig `api://<CLIENT_ID>` vor. **Das reicht nicht.** Der Client sendet `resource=<MCP_URL>` ([RFC 8707](https://www.rfc-editor.org/rfc/rfc8707)), und Entra lehnt einen Resource-Wert ab, der nicht als Application ID URI registriert ist — der Token-Request scheitert dann mit **`AADSTS9010010`**.

`<MCP_URL>` deshalb **zusätzlich** als Application ID URI eintragen.

Microsofts eigene Dokumentation verlangt für ein `https://`-Schema eine im Tenant verifizierte Domain. **Live beobachtet am 09.08.2026:** Eine `https://`-Application-ID-URI wurde dennoch anstandslos akzeptiert, obwohl die Domain im betroffenen Tenant **nicht** als verifiziert geführt wird — die verbreitete Annahme „geht nur mit verifizierter Domain" hat sich in diesem Fall nicht bestätigt. Es ist deshalb kein verlässliches Vorab-Ausschlusskriterium mehr; einfach eintragen und ausprobieren. Wird der Wert dennoch abgelehnt, ist eine verifizierte Domain (oder der Umzug des MCP-Servers auf einen Host mit verifizierter Domain) der nächste Versuch.

### Scope hinzufügen

*Add a scope*

| Feld | Wert |
|---|---|
| Scope name | `mcp.access` |
| Who can consent | Admins and users |
| Consent display name / description | sprechend formulieren — der Text erscheint im Consent-Dialog |
| State | Enabled |

Voller Scope-Wert: `<MCP_URL>/mcp.access` bzw. `api://<CLIENT_ID>/mcp.access`. Diesen Wert brauchst du gleich wieder — als `MCP_OIDC_ADVERTISED_SCOPES` in Abschnitt 7 (siehe Abschnitt „Zu den Scopes" unten).

> **Alternative `.default`:** Statt eines benannten Scopes lässt sich auch der Entra-eigene Sammel-Scope `api://<CLIENT_ID>/.default` anfordern, der alle statisch konfigurierten Berechtigungen der App auflöst. Er funktioniert nur, wenn mindestens ein Scope existiert — ein leeres „Expose an API" lässt `.default` ins Leere laufen. Wer diesen Weg geht, legt trotzdem einen Scope an (üblich: `user_impersonation`) und fordert dann `.default` an.

### Eigene Berechtigung für den Scope erteilen (Pflicht)

*API permissions → Add a permission → My APIs → `<APP_SLUG>`*

Diese App-Registrierung steckt hier in einer Doppelrolle: Sie ist zugleich die **Ressource** (der gerade angelegte Scope) und der **Client** — dieselbe Client-ID trägst du gleich in claude.ai ein ([`claude-connector.md`](claude-connector.md)). Ohne diesen Schritt hat der Client keine Berechtigung, seinen eigenen Scope anzufordern, auch wenn der Scope selbst schon existiert.

- Unter *My APIs* die eigene App auswählen, den delegierten Scope `mcp.access` markieren, hinzufügen.
- **Grant admin consent** klicken. Zwei unterschiedliche Fehlercodes zeigen zwei unterschiedliche Lücken an, nicht dieselbe:
  - **`AADSTS650053`** (angeforderter Scope existiert nicht) — der Scope wurde weiter oben nicht angelegt, heißt anders, oder wird ohne die vollqualifizierte Form angefordert (siehe „Zu den Scopes" weiter unten).
  - **`AADSTS65001`** (Zustimmung fehlt) — der Scope existiert und ist korrekt benannt, aber „Grant admin consent" wurde nicht geklickt; die Berechtigung steht dann im Status „Not granted".

**Abkürzung (optional):** Unter *Expose an API → Authorized client applications* dieselbe Client-ID mit dem Scope `mcp.access` eintragen. Das erspart eine zweite Zustimmungsabfrage beim ersten Login — sinnvoll gerade weil App und Client hier identisch sind.

**Funktionierende Reihenfolge (live geprüft, 09.08.2026):** zuerst Application ID URI, dann Scope `mcp.access` anlegen, dann diesen Abschnitt.

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

*Manifest* öffnen und sicherstellen, dass die App v2.0-Tokens akzeptiert. Bleibt der Wert `null` (Portal-Vorgabe), stellt Entra v1.0-Tokens aus — deren Aussteller lautet `https://sts.windows.net/<TENANT_ID>/`, während dieser Server gegen `https://login.microsoftonline.com/<TENANT_ID>/v2.0` prüft (Abschnitt 7). Die Prüfung schlägt dann fehl, und zwar **ohne erkennbaren Grund im Protokoll**: Der Server beantwortet jede Ablehnung gleich, nur mit `401` und dem Text `unauthorized` (Abschnitt 8a) — der Connector zeigt sich in claude.ai als verbunden, jeder Werkzeugaufruf scheitert trotzdem.

> **Zwei Ansichten, ein Feld, zwei Namen.** Das Entra-Portal zeigt das Manifest wahlweise im klassischen Azure-AD-Graph-Format oder im neueren Microsoft-Graph-Format — je nachdem, welche Ansicht gerade aktiv ist, heißt dasselbe Feld anders. Offiziell dokumentiert als Umbenennung, nicht als zwei getrennte Einstellungen ([App manifest (Azure AD Graph format) deprecation](https://learn.microsoft.com/en-us/entra/identity-platform/azure-active-directory-graph-app-manifest-deprecation#attribute-differences-between-azure-ad-graph-and-microsoft-graph-formats)):
>
> | Ansicht | Ort im Manifest | Feldname |
> |---|---|---|
> | klassisch (Azure-AD-Graph-Format) | oberste Ebene | `accessTokenAcceptedVersion` |
> | Microsoft-Graph-Format | verschachtelt im `api`-Objekt | `requestedAccessTokenVersion` |
>
> Je nachdem, welche Ansicht das Portal gerade zeigt:
>
> ```json
> "accessTokenAcceptedVersion": 2
> ```
>
> oder, im `api`-Objekt der Graph-Ansicht:
>
> ```json
> "api": {
>   "requestedAccessTokenVersion": 2
> }
> ```

**Nach dem Speichern den Connector in claude.ai trennen und neu verbinden.** Ein bereits ausgestelltes v1.0-Token bleibt sonst in Gebrauch, bis es abläuft — die Manifest-Änderung wirkt dann scheinbar nicht, obwohl sie korrekt gespeichert wurde.

## 5. Graph-Berechtigungen und Admin-Consent

*API permissions* → sicherstellen, dass die delegierten Microsoft-Graph-Berechtigungen `openid`, `profile` und **`offline_access`** vorhanden sind → **Grant admin consent**.

`offline_access` ist der Punkt, der leicht übersehen wird: ohne diese Berechtigung stellt Entra **kein Refresh-Token** aus. Der Connector funktioniert dann zunächst einwandfrei und bricht ab, sobald das erste Access-Token abläuft — ein Fehlerbild, das sich schwer auf die Ursache zurückführen lässt, weil zwischen Einrichtung und Symptom Stunden liegen.

## 6. Zugriff einschränken

*Enterprise applications → `<APP_SLUG>` → Properties* → **Assignment required = Yes**, danach unter *Users and groups* gezielt zuweisen. Bei Verwendung von App-Rollen (Abschnitt 3a) wird hier zugleich die Rolle vergeben.

Ohne diese Einstellung kann jeder Benutzer des Tenants den Connector verbinden.

## 7. Ermittelte Werte

*Ort: Infisical bzw. die Server-Konfiguration — nicht mehr das Entra-Portal.*

| Wert | Pfad |
|---|---|
| Discovery | `https://login.microsoftonline.com/<TENANT_ID>/v2.0/.well-known/openid-configuration` |
| Issuer | `https://login.microsoftonline.com/<TENANT_ID>/v2.0` |
| JWKS | aus dem Discovery-Dokument (`jwks_uri`) |
| `aud` im v2-Token | `<CLIENT_ID>` |

Du trägst **drei Werte aus dem Portal** ein — die Aussteller-URL baut der Server daraus selbst. Dazu die vollqualifizierte Scope-Form aus dem Abschnitt „Zu den Scopes" unten:

```dotenv
MCP_AUTH_MODE=oidc
MCP_OIDC_PROVIDER=entra
MCP_ENTRA_TENANT_ID=<TENANT_ID>
MCP_ENTRA_CLIENT_ID=<CLIENT_ID>
MCP_OIDC_REQUIRED_SCOPES=mcp.access
MCP_OIDC_ADVERTISED_SCOPES=<MCP_URL>/mcp.access
MCP_RESOURCE_URL=<MCP_URL>
MCP_ALLOWED_SUBJECTS=<OBJECT_ID des berechtigten Benutzers>
```

`MCP_ENTRA_TENANT_ID` muss die **Verzeichnis-ID als GUID** sein. Eine verifizierte Domain oder `common`/`organizations` funktioniert hier nicht und wird beim Start abgewiesen: Der Aussteller im ausgestellten Token trägt immer die GUID; `common` liefert im Discovery-Dokument nur die Vorlage `{tenantid}`, und eine Domain liefert die GUID zurück statt sich selbst. Eine daraus gebaute URL passt also nie zum Token. Ohne diese Prüfung wäre das Symptom eine 401-Schleife, die im Client nur als „Authorization failed" ankommt.

`MCP_ENTRA_CLIENT_ID` ist zugleich die erwartete Audience. Gangways OIDC-Verifier (`github.com/coreos/go-oidc`) prüft, ob dieser Wert im `aud`-Claim steht — es gibt **keine** zusätzliche Prüfung gegen `MCP_RESOURCE_URL` oder `api://<CLIENT_ID>`. Bei einem v2.0-Token für den in Abschnitt 3 angelegten Scope ist `aud` die reine `<CLIENT_ID>` ohne `api://`-Präfix. Fordert ein Client stattdessen den Application-ID-URI-Scope an, setzt Entra `api://<CLIENT_ID>` als `aud` und die Prüfung scheitert — dann fordert der **Client** den falschen Scope an, nicht der Server den falschen Wert. Abschnitt 8 zeigt, wie man `aud` im ausgestellten Token nachsieht.

### Zum Subject-Claim

**Du musst hier nichts eintragen.** Bei `MCP_OIDC_PROVIDER=entra` verwendet der Server automatisch `oid` — aus einem praktischen Grund: Die **Objekt-ID steht im Portal** (Microsoft Entra ID → Benutzer → der Benutzer → Objekt-ID) und lässt sich damit vorab in `MCP_ALLOWED_SUBJECTS` eintragen.

Wer es dennoch anders will, setzt `MCP_OIDC_SUBJECT_CLAIM` ausdrücklich — eine Angabe schlägt den Vorgabewert immer.

`sub` ist bei Entra dagegen **paarweise pseudonymisiert**: derselbe Benutzer hat in einer anderen App-Registrierung einen anderen `sub`, und der Wert steht **nirgends im Portal**. Wer `sub` verwendet, muss sich erst anmelden, das ausgestellte Token dekodieren (Abschnitt 8), den Wert ablesen, eintragen und neu starten. Für die Konto-Zuordnung ist `sub` unproblematisch, solange nur diese eine Anwendung genutzt wird — es ist nur umständlicher einzurichten.

`email` ist die schlechteste Wahl: der Wert ist änderbar und steht je nach Konfiguration gar nicht im Token.

**Selbstcheck, falls der Werkzeugkatalog trotz erfolgreicher Anmeldung leer bleibt:** Prüfe, ob irgendwo — etwa aus einer kopierten `generic.md`- oder `authentik.md`-Konfiguration — versehentlich `MCP_OIDC_SUBJECT_CLAIM=sub` oder ein anderer Wert stehengeblieben ist. Eine gesetzte Variable überschreibt den Entra-Vorgabewert `oid` immer, auch wenn sie nur aus Versehen dort steht.

### Zu den Scopes

Entra unterscheidet, was **angekündigt und beim Anbieter angefordert** wird, von dem, was **im ausgestellten Token geprüft** wird — zwei verschiedene Zeichenketten für denselben Scope, offiziell dokumentiert ([Expose an API](https://learn.microsoft.com/en-us/entra/identity-platform/quickstart-configure-app-expose-web-apis), [Access token claims](https://learn.microsoft.com/en-us/entra/identity-platform/access-token-claims-reference)):

| | Wert | Variable |
|---|---|---|
| Angekündigt (`WWW-Authenticate`-`scope`-Parameter, RFC-9728-`scopes_supported`) und beim Anbieter angefordert | **vollqualifiziert**: `<MCP_URL>/mcp.access` — identisch mit `api://<CLIENT_ID>/mcp.access`, weil Abschnitt 3 die Application ID URI auf `<MCP_URL>` gesetzt hat | `MCP_OIDC_ADVERTISED_SCOPES` |
| Im `scp`-Claim des ausgestellten Tokens | **kurzer Name ohne Präfix**: `mcp.access` | `MCP_OIDC_REQUIRED_SCOPES` |

Ein Client, der nur den kurzen Namen ankündigt oder anfordert, ordnet Entra mangels erkennbarer Ziel-API der Standardressource Microsoft Graph zu, wo dieser Scope nicht existiert: `AADSTS650053`. Deshalb steht in Abschnitt 7 **zusätzlich** `MCP_OIDC_ADVERTISED_SCOPES` mit der vollqualifizierten Form — `MCP_OIDC_REQUIRED_SCOPES` bleibt unverändert die kurze Form, gegen die der Server den `scp`-Claim prüft (ersatzweise einen `scope`-Claim, falls ein anderer Aussteller stattdessen diesen setzt). Beide aus derselben Variable zu speisen kann bei Entra nur eine der beiden Anforderungen gleichzeitig erfüllen.

Ohne gesetztes `MCP_OIDC_ADVERTISED_SCOPES` fällt die Ankündigung (`WWW-Authenticate`-Kopfzeile **und** `scopes_supported` im Protected-Resource-Metadata-Dokument) auf `MCP_OIDC_REQUIRED_SCOPES` zurück — bei den meisten anderen Anbietern (u. a. Authentik) ist das korrekt, weil dort beide Formen identisch sind. Bei Entra ist das die dokumentierte Ausnahme; ein leer gelassenes `MCP_OIDC_ADVERTISED_SCOPES` äußert sich beim ersten Verbindungsaufbau so: Der Consent-Dialog von claude.ai listet nur `openid`, `profile`, `offline_access` auf, nicht `mcp.access`, und der anschließende Token-Request scheitert mit `AADSTS650053`.

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

### Woran man erkennt, dass es steht

In claude.ai erscheinen unter *Tool-Berechtigungen* zwei schreibgeschützte Werkzeuge: **List documents** und **Search documents**. Von außen, ohne claude.ai zu öffnen, prüfbar:

```bash
curl -i -X POST <MCP_URL>
# WWW-Authenticate: Bearer scope="<MCP_URL>/mcp.access", resource_metadata="…"

curl -s https://<MCP_HOST>/.well-known/oauth-protected-resource | jq .scopes_supported
# ["<MCP_URL>/mcp.access"]
```

Beide Werte tragen die vollqualifizierte Form aus `MCP_OIDC_ADVERTISED_SCOPES` — nicht den kurzen Namen aus `MCP_OIDC_REQUIRED_SCOPES`, gegen den weiterhin nur der `scp`-Claim des Tokens geprüft wird.

## 8a. Fehlersuche, wenn der Server nur „unauthorized" antwortet

Jede vom Server abgelehnte Anfrage beantwortet er mit **`401`** und dem immer gleichen Text `unauthorized` — unabhängig davon, ob kein Token mitgeschickt wurde, die Audience nicht passt, der Scope fehlt oder das Token abgelaufen ist. Es gibt keine serverseitige Protokollzeile, die zwischen diesen Ursachen unterscheidet; am Server selbst ist hier nichts zu debuggen.

Der belastbare Weg führt über das **Entra-Anmeldeprotokoll**: *Microsoft Entra Admin Center → Identity → Monitoring & health → Sign-in logs*, gefiltert auf die Correlation-ID aus der Fehlermeldung von claude.ai. Dabei **beide** Reiter prüfen:

- **User sign-ins** — der interaktive Login-Teil des Flows.
- **Service principal sign-ins** — der anschließende Token-Austausch läuft **ohne** Benutzerkontext und erscheint ausschließlich hier. Wer nur den ersten Reiter ansieht, sieht scheinbar einen erfolgreichen Login und findet den eigentlichen Fehler nicht.

Der dort sichtbare `AADSTS`-Fehlercode führt in die Tabelle in Abschnitt 9.

## 9. Bekannte Stolpersteine

| Symptom | Ursache | Behebung |
|---|---|---|
| `AADSTS500113` (keine Weiterleitungsadresse registriert) | Redirect URI fehlt unter *Authentication*, oder dort steht versehentlich die Server-Adresse `<MCP_URL>` statt der claude.ai-Rückrufadresse | *Authentication → Web*: `https://claude.ai/api/mcp/auth_callback` eintragen (Schritt 1) |
| `AADSTS9010010` beim Token-Request | `<MCP_URL>` ist nicht als Application ID URI registriert | Schritt 3 nachholen |
| `AADSTS650053` (angeforderter Scope existiert nicht) | Scope `mcp.access` fehlt unter „Expose an API", der Name weicht ab, oder `MCP_OIDC_ADVERTISED_SCOPES` fehlt bzw. trägt die kurze statt die vollqualifizierte Form | Schritt 3 (Scope) nachholen; Abschnitt 7 und „Zu den Scopes" prüfen |
| `AADSTS65001` (Zustimmung fehlt) | Scope korrekt angelegt, aber „Grant admin consent" wurde nicht geklickt — die Berechtigung steht im Status „Not granted" | Abschnitt 3, „Eigene Berechtigung für den Scope erteilen" nachholen |
| `AADSTS7000218` (Client-Secret verlangt) | die Anfrage kommt von einem Confidential Client, aber claude.ai hat kein Secret mitgeschickt | Secret im Connector-Dialog nachtragen (Schritt 2), oder App bewusst als Public Client ohne Secret-Pflicht führen |
| `iss` endet nicht auf `/v2.0`, oder Server antwortet nach Manifest-Änderung weiter mit `401 unauthorized` | `accessTokenAcceptedVersion`/`requestedAccessTokenVersion` steht nicht auf `2` (Abschnitt 4), oder ein bereits ausgestelltes v1.0-Token ist noch in Gebrauch | Manifest korrigieren; danach Connector in claude.ai trennen und neu verbinden |
| Consent-Dialog zeigt nur `openid`, `profile`, `offline_access`, nicht `mcp.access` | `MCP_OIDC_ADVERTISED_SCOPES` ist nicht gesetzt — die Ankündigung fällt auf die kurze Form aus `MCP_OIDC_REQUIRED_SCOPES` zurück, die Entra nicht auflösen kann | Abschnitt 7 nachholen, vollqualifizierte Form `<MCP_URL>/mcp.access` |
| `AADSTS50011` (Redirect-URI-Mismatch) | Redirect-URI weicht ab, bei Claude Code der Port | exakte URI eintragen; Entra ignoriert den Port **nicht** |
| Connector funktioniert plötzlich nicht mehr | Client-Secret abgelaufen | neues Secret erzeugen und im Connector aktualisieren |
| Verbindung bricht nach der ersten Token-Laufzeit ab | `offline_access` fehlt, kein Refresh-Token | Schritt 5 nachholen, inklusive Admin-Consent |
| 401-Schleife, „audience mismatch" | `aud` im ausgestellten Token entspricht nicht `MCP_ENTRA_CLIENT_ID` (der Server prüft nur diesen einen Wert, siehe Abschnitt 7) | `accessTokenAcceptedVersion: 2` prüfen; `aud` im Token nachsehen (Abschnitt 8). Steht dort `api://<CLIENT_ID>`, fordert der **Client** den falschen Scope an — im Connector den Scope aus Abschnitt 3 verwenden |
| Write-Tools erscheinen nicht | App-Rolle nicht zugewiesen oder `MCP_OIDC_CAPABILITY_CLAIM` nicht gesetzt | Abschnitte 3a und 6 prüfen; `roles`-Claim im Token gegenprüfen |
| Jeder Tenant-Benutzer kommt durch | „Assignment required" steht auf `No` | Schritt 6 nachholen |
| Start bricht ab: „ist keine Verzeichnis-ID" | in `MCP_ENTRA_TENANT_ID` steht eine Domain oder `common`/`organizations` | die GUID aus der Portal-Übersicht eintragen (Abschnitt 7) |
| Start bricht ab: „gesetzt sind auch Variablen anderer Anbieter" | neben den `MCP_ENTRA_*`-Werten stehen noch `MCP_AUTHENTIK_*`- oder `MCP_OIDC_ISSUER`-Reste aus einer früheren Konfiguration | entfernen — bei `MCP_OIDC_PROVIDER=entra` werden sie nicht gelesen |
| claude.ai: „Automatische Client-Registrierung wird von … nicht unterstützt" | Entra unterstützt weder Dynamic Client Registration noch Client ID Metadata Documents (Voraussetzungen, Punkt 4) | vorregistrierte Client-ID/Secret im Connector-Dialog eintragen — der vorgesehene Weg |
| Server antwortet nur mit `401 unauthorized`, keine weitere Auskunft | der Server unterscheidet die Ablehnungsgründe nicht in seiner Antwort | Entra-Anmeldeprotokolle prüfen, **beide** Reiter (Abschnitt 8a) |
