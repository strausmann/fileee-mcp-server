# fileee-mcp-server

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Ein **inoffizieller** MCP-Server für [Fileee](https://www.fileee.com), der die eigenen Dokumente für AI-Clients zugänglich macht — als lokaler Server über einen statischen Token oder als **Remote-Connector mit OAuth-Anmeldung**, etwa in der Claude.ai-Web-UI.

> **Stand:** Das Grundgerüst steht — Konfiguration, Anmeldung über [Gangway](https://gangway.strausmann.cloud), Zuordnung von Identität zu Fileee-Konto. Die lesenden Werkzeuge sind vollständig angemeldet: **32 Werkzeuge**, siehe [`docs/tools.md`](docs/tools.md). Schreibende, teilende und löschende Werkzeuge entstehen in den folgenden Umsetzungsschritten.

Der Server nutzt die Core-Lib [`strausmann/go-fileee`](https://github.com/strausmann/go-fileee) und ist damit Geschwisterprojekt von [`strausmann/fileee-server`](https://github.com/strausmann/fileee-server) (REST-API für n8n/CI). Der Unterschied: `fileee-server` kennt genau ein Fileee-Konto und ein statisches Token; dieser Server bindet die **Identität des anfragenden Benutzers** an ein Fileee-Konto.

*Dieses Projekt ist ein unabhängiges Community-Projekt und steht in keiner Verbindung zur fileee GmbH.*

## Was es kann

- **MCP über Streamable HTTP** (`POST /mcp`), auf Basis des offiziellen [Go-SDK](https://github.com/modelcontextprotocol/go-sdk)
- **OAuth 2.1 als Resource Server** nach [RFC 9728](https://www.rfc-editor.org/rfc/rfc9728) — der Identity Provider ist frei wählbar und reine Konfiguration
- **Statisches Bearer-Token** als Alternative, wenn kein IdP vorhanden ist
- **Ein oder mehrere Fileee-Konten**, zugeordnet über einen signierten Claim aus dem Token
- **Alle Werkzeuge angemeldet, Freigabe je Werkzeug beim Client** — jedes Werkzeug trägt einen Titel und die zutreffenden `ToolAnnotations` (`readOnlyHint`, `destructiveHint`, `idempotentHint`); Always allow / Needs approval / Blocked entscheidet der Client und dessen Benutzer, nicht der Server (siehe [ADR-0018](docs/adr/0018-werkzeug-freigabe-und-client-steuerung.md))

## Drei Betriebsarten

Derselbe Container bedient sehr verschiedene Setups. Die drei Achsen sind unabhängig voneinander schaltbar.

### Eine Person, ein Fileee-Konto, kein Identity Provider

```dotenv
MCP_AUTH_MODE=token
MCP_API_TOKEN=<openssl rand -hex 32>
FILEEE_MODE=single
FILEEE_USERNAME=…
FILEEE_PASSWORD=…
FILEEE_TOTP_SEED=…        # nur bei aktiver Zwei-Faktor-Authentifizierung
```

Drei Pflichtwerte, kein IdP, kein Reverse Proxy nötig. Für Claude Code lokal oder für Automatisierung im eigenen Netz.

### Remote-Connector mit OAuth

`MCP_OIDC_PROVIDER` wählt den Identity Provider. **Jeder Anbieter hat seinen eigenen Satz Variablen** — du trägst ein, was dein Anbieter dir zeigt, und der Server baut die Aussteller-URL daraus (siehe [ADR-0016](docs/adr/0016-anbieter-namensraeume-statt-roher-oidc-parameter.md)):

| `entra` | `authentik` | `generic` |
|---|---|---|
| `MCP_ENTRA_TENANT_ID` | `MCP_AUTHENTIK_BASE_URL` | `MCP_OIDC_ISSUER` |
| `MCP_ENTRA_CLIENT_ID` | `MCP_AUTHENTIK_APP_SLUG` | `MCP_OIDC_CLIENT_ID` |
| | `MCP_AUTHENTIK_CLIENT_ID` | |

Variablen eines anderen als des gewählten Anbieters lassen den Start abbrechen — sie wären sonst wirkungslos gesetzt.

```dotenv
MCP_AUTH_MODE=oidc
MCP_OIDC_PROVIDER=entra
MCP_ENTRA_TENANT_ID=<verzeichnis-id>
MCP_ENTRA_CLIENT_ID=<anwendungs-id>
MCP_RESOURCE_URL=https://<mcp-host>/mcp
MCP_ALLOWED_SUBJECTS=<subject des berechtigten Benutzers>
FILEEE_ALLOWED_ORIGIN_PREFIXES=<CIDR-Liste der zulässigen Herkunftsadressen>
FILEEE_MODE=single
```

Einrichtung des Identity Providers — eine Anleitung je Anbieter, jede nur mit ihren eigenen Variablen:

| Anbieter | Anleitung |
|---|---|
| Microsoft Entra ID | [`docs/idp/entra-id.md`](docs/idp/entra-id.md) |
| Authentik | [`docs/idp/authentik.md`](docs/idp/authentik.md) |
| GitLab, Keycloak, Auth0, Google und andere | [`docs/idp/generic.md`](docs/idp/generic.md) |

Danach in allen Fällen: [`docs/idp/claude-connector.md`](docs/idp/claude-connector.md).

> **Aktuell nur `oidc`.** Der Server läuft auf [Gangway](https://gangway.strausmann.cloud) (siehe [ADR-0015](docs/adr/0015-gangway-als-unterbau.md)) auf, und Gangway v0.2.0 baut intern ausschließlich einen OIDC-Verifier — es gibt (noch) keinen Weg, stattdessen ein statisches Bearer-Token zu verifizieren. `MCP_AUTH_MODE=token`/`both` werden von `LoadConfig` weiterhin akzeptiert, der Server verweigert den Start mit diesem Modus aber explizit. Details und der Ausblick auf eine Lösung stehen im Nachtrag zu ADR-0015.

Weitere netzwerkbezogene Variablen, die im Modus `oidc` Pflicht bzw. relevant sind:

| Variable | Zweck | Default |
|---|---|---|
| `FILEEE_ALLOWED_ORIGIN_PREFIXES` | CIDR-Liste (oder einzelne Adressen) der Herkunftsadressen, die `/mcp` überhaupt erreichen dürfen — Pflicht im Modus `oidc`, ohne sie verweigert Gangway den Start | — |
| `FILEEE_TRUSTED_PROXIES` | CIDR-Liste der Proxys, deren Weiterleitungs-Header (siehe `FILEEE_CLIENT_IP_HEADER_MODE`) geglaubt werden | leer — es zählt nur die Peer-Adresse |
| `FILEEE_CLIENT_IP_HEADER_MODE` | genau ein Weiterleitungs-Header als Quelle der Client-Adresse: `x-forwarded-for`, `x-real-ip` oder `cf-connecting-ip` | `cf-connecting-ip` — vor dem Produktivbetrieb gegen die tatsächliche Proxy-Kette (z. B. Pangolin/Traefik) prüfen |

Zwei weitere Variablen rund um Scopes — angefordert/geprüft und angekündigt sind bei den meisten Anbietern derselbe Wert, aber nicht bei jedem:

| Variable | Zweck | Default |
|---|---|---|
| `MCP_OIDC_REQUIRED_SCOPES` | Kommaliste der Scopes, die ein Token tragen muss — geprüft gegen den `scp`-Claim des verifizierten Tokens (ersatzweise `scope`) | leer — jeder authentifizierte Aufrufer erlaubt |
| `MCP_OIDC_ADVERTISED_SCOPES` | überschreibt, wenn gesetzt, ausschließlich das, was **vor** dem Token-Austausch angekündigt wird (`WWW-Authenticate`-`scope`-Parameter, RFC-9728-`scopes_supported`) — `MCP_OIDC_REQUIRED_SCOPES` bleibt davon unberührt und bleibt das, wogegen tatsächlich geprüft wird | leer — Ankündigung fällt auf `MCP_OIDC_REQUIRED_SCOPES` zurück |

Der Grund für die Trennung: ein Connector, der noch kein Token besitzt, erfährt den geforderten Scope ausschließlich über diese Ankündigung. Bei den meisten Anbietern (u. a. Authentik) ist der beim Anbieter angeforderte Scope-Name identisch mit dem später im Token ausgestellten `scp`-Wert — `MCP_OIDC_REQUIRED_SCOPES` allein reicht. Microsoft Entra ID ist die dokumentierte Ausnahme: angekündigt/angefordert werden muss dort eine vollqualifizierte Form (`https://<mcp-host>/mcp/<scope-name>`), während der ausgestellte `scp`-Claim weiterhin nur den kurzen Namen trägt — ein nackter Name scheitert beim Anbieter mit `AADSTS650053`. Details zur Entra-spezifischen Form: [`docs/idp/entra-id.md`](docs/idp/entra-id.md).

### Mehrere Benutzer, je eigenes Fileee-Konto

```dotenv
MCP_AUTH_MODE=oidc
FILEEE_MODE=multi
FILEEE_ACCOUNTS=alice,bob
FILEEE_ACCOUNT_ALICE_USERNAME=…
FILEEE_ACCOUNT_ALICE_PASSWORD=…
FILEEE_ACCOUNT_ALICE_SUBJECTS=alice@example.com
FILEEE_ACCOUNT_BOB_USERNAME=…
…
```

Die Zuordnung läuft über einen konfigurierbaren Claim aus dem Token (Default `sub`). Mehrere Identitäten dürfen auf **ein** Fileee-Konto zeigen; eine Identität auf zwei Konten ist ein Startup-Fehler, kein „first match wins". Ein unbekanntes Subject bekommt `403` — es gibt keinen Fallback auf ein Standardkonto.

## Funktionsumfang

Der Server registriert **alle** Werkzeuge für jeden authentifizierten Aufrufer — es gibt keine
serverseitige Einschränkung mehr, welche Werkzeuge ein Client zu sehen bekommt (siehe
[ADR-0018](docs/adr/0018-werkzeug-freigabe-und-client-steuerung.md)). Jedes Werkzeug trägt einen
sprechenden Titel und die zutreffenden Hinweise (`readOnlyHint`, `destructiveHint`,
`idempotentHint`) — die Freigabe je Werkzeug (Always allow / Needs approval / Blocked) trifft der
**Client und dessen Benutzer**, nicht der Server.

Destruktive Operationen (Hard-DELETE, unwiderruflich, kein Papierkorb) sind zusätzlich über ein
serverseitiges Audit-Log vor jeder Löschung abgesichert und daran gebunden, dass die zu löschende
ID aus einer vorangegangenen Leseantwort derselben geprüften Identität stammt — gebunden an
`serve.IdentityFrom(ctx)`, nicht an die MCP-Sitzung, die es unter der erzwungenen Statelessness
ohnehin nicht über den einzelnen Request hinaus gibt.

## Werkzeuge

Der Katalog entsteht schrittweise. Die lesenden Werkzeuge sind vollständig angemeldet — **32
Werkzeuge** über Dokumente, Stammdaten (Schlagworte, Firmen, Dokumenttypen, Dokumenttyp-Schemata),
Kontakte/Erinnerungen/Konversationen, Boxen, PDF-/Seitenbild-Download mit harter Größenobergrenze,
Seiten-OCR und Kontostand — vollständig in [`docs/tools.md`](docs/tools.md) dokumentiert,
inklusive der Absicherung gegen präparierte, fremdbestimmte Inhalte (Dokumenttitel, Firmen-/
Kontaktnamen, Erinnerungstexte, Konversationsbetreffs, erkannter OCR-Text). Schreibende, teilende
und löschende Werkzeuge entstehen in den folgenden Umsetzungsschritten — jedes davon wird, sobald
es existiert, ebenso angemeldet und über seine `ToolAnnotations` beschrieben (siehe
[ADR-0018](docs/adr/0018-werkzeug-freigabe-und-client-steuerung.md)).

## Sicherheit

- **Credentials** (Fileee-Zugangsdaten, TOTP-Seed, API-Token) gehören ausschließlich in einen Secret-Manager, nie in Code oder Commits. Der Container unterstützt neben `.env` einen Infisical-Modus: Sind `INFISICAL_UNIVERSAL_AUTH_CLIENT_ID` und `INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET` (oder `INFISICAL_TOKEN`) gesetzt, meldet sich der Entrypoint an und injiziert die Werte; sonst startet der Server direkt mit den vorhandenen Umgebungsvariablen. Ist nur **eine** der beiden Variablen gesetzt, bricht der Start ab — ein halbes Paar ist nie Absicht, und ein stiller Rückfall auf den Umgebungs-Weg würde den Fehler erst viel später sichtbar machen.
- **Session-Dateien** des Client-Pools sind Secrets (`0600`, je Konto getrennt) und werden nie geloggt.
- **Dokumentinhalte sind fremdbestimmte Daten.** OCR-Text kann Anweisungen enthalten, die an das Modell gerichtet sind. Tool-Ausgaben werden deshalb als nicht vertrauenswürdig markiert, und destruktive Operationen sind zusätzlich abgesichert.
- Die Core-Lib **schont Fileees Infrastruktur** über ihr eigenes Rate-Limiting und Backoff im HTTP-Transport ([`go-fileee` ADR-0005](https://github.com/strausmann/go-fileee/blob/main/docs/adr/0005-schonender-betrieb-rate-limiting.md)). Dieser Server ergänzt eine zweite, unabhängige Begrenzung auf Ebene der Werkzeugaufrufe selbst — siehe „Ratenbegrenzung" unten.

### Ratenbegrenzung

Jeder Aufruf eines Werkzeugs (`tools/call`) muss drei unabhängige Kontingente passieren, bevor er den Tool-Handler überhaupt erreicht — ein Aufrufer, der eines davon überschreitet, bekommt sofort einen JSON-RPC-Fehler (Code `-32011`), nie eine Wartezeit:

| Variable | Zweck | Default |
|---|---|---|
| `FILEEE_RATE_RPS` / `FILEEE_RATE_BURST` | Anfragerate **je Anrufer** — geschlüsselt auf das verifizierte Token-Subject, nicht auf die Client-Adresse (die hängt von `FILEEE_TRUSTED_PROXIES` ab und ist bei falscher Einstellung sogar vom Anrufer selbst wählbar) | `1` RPS, Burst `3` |
| `FILEEE_RATE_GLOBAL_RPS` / `FILEEE_RATE_GLOBAL_BURST` | Anfragerate **über alle Anrufer hinweg** — das globale Kontingent, das die README bereits vor dieser Einstellung beschrieb, tatsächlich durchgesetzt | `1` RPS, Burst `3` |
| `FILEEE_MAX_INFLIGHT` | Obergrenze **gleichzeitig laufender** Werkzeugaufrufe, über alle Anrufer hinweg — schützt die eine, je Fileee-Konto geteilte Verbindung ([`internal/clientpool`](internal/clientpool)) vor Überlastung durch Parallelität, unabhängig von der Rate | `8` |

`FILEEE_MAX_UPLOAD_BYTES` wird geladen, aber **noch nicht durchgesetzt** — es gibt noch kein Upload-Werkzeug (Teil B, `write`), das es aufrufen könnte.

`FILEEE_MAX_DOWNLOAD_BYTES` wird ebenfalls geladen, ist aber **nicht mit den beiden inzwischen existierenden Download-Werkzeugen verbunden**: `get_document_pdf` und `get_page_image` (siehe [`docs/tools.md`](docs/tools.md)) begrenzen ihren jeweiligen Datenstrom über eine eigene, im Code fest verdrahtete Obergrenze von 8 MiB (`maxBinaryBytes`, `internal/tools/read_binary.go`), unabhängig vom konfigurierten Wert dieser Variable (Default 1 MiB) — wer `FILEEE_MAX_DOWNLOAD_BYTES` setzt, ändert damit **nichts** am tatsächlichen Verhalten dieser beiden Werkzeuge. Das ist eine offene Inkonsistenz, keine bewusste Entscheidung; bis sie aufgelöst ist (entweder `maxBinaryBytes` auf `cfg.MaxDownloadBytes` umstellen oder die Variable als für diese Werkzeuge nicht zuständig dokumentieren), gilt für einen Betreiber: **die tatsächliche Grenze ist die feste 8-MiB-Konstante im Code, nicht `FILEEE_MAX_DOWNLOAD_BYTES`.**

Die von `FILEEE_MAX_UPLOAD_BYTES` abgeleitete `MaxRequestBodyBytes` bleibt aus einem unabhängigen Grund offen: Gangway v0.2.0 baut den HTTP-Handler intern ohne einen Weg, dessen Größenlimit zu überschreiben (siehe [ADR-0015](docs/adr/0015-gangway-als-unterbau.md)).

## Diagnose

`FILEEE_LOG_LEVEL` steuert das diagnostische Protokoll dieses Servers ([`internal/diag`](internal/diag)) — ein JSON-Objekt pro Zeile auf der Standardausgabe, unabhängig von Gangways eigenem Zugriffsprotokoll (NGINX-Format, siehe dessen Doku) und von den Start-/Fehlermeldungen in `cmd/fileee-mcp-server/main.go`, die weiterhin unverändert auf stdout/stderr laufen.

```dotenv
FILEEE_LOG_LEVEL=info     # Default
FILEEE_LOG_LEVEL=debug
```

| Stufe | Was protokolliert wird |
|---|---|
| `info` (Default) | Je Werkzeugaufruf: Werkzeugname, Dauer, Erfolg oder Fehlerart (`ok`, `invalid_input`, `access_denied`, `fileee_error` mit HTTP-Status, `error`), der aufgerufene Fileee-Endpunkt und bei Erfolg die Anzahl zurückgegebener Treffer. Zusätzlich je aufgelöster Anfrage: die vom OIDC-Selector ermittelte Fähigkeitsmenge und wie viele Werkzeuge die dafür gebaute Instanz hält — der Befund, wenn ein Client einen leeren Werkzeugkatalog sieht — sowie, bei `MCP_OIDC_REQUIRED_SCOPES`, der Name des fehlenden Scopes bei einer Ablehnung. |
| `debug` | Zusätzlich zu allem oben: die vom Aufrufer übergebenen Werkzeug-Argumente (Suchbegriffe, Limits, Paging-Offsets) sowie go-fileees eigenes Transport-Protokoll (Methode, Pfad, Statuscode je HTTP-Versuch gegen Fileee, `fileee.WithLogger`). |

**Niemals, auf keiner Stufe:** Zugangsdaten, Token, TOTP-Seed, Antwortkörper der Fileee-API, Dokumentinhalte oder -titel. Jedes Attribut, das dieser Logger schreibt — unabhängig davon, welcher Code es erzeugt hat, unabhängig von der Stufe — läuft durch eine einzige Maskierung (`internal/diag`, `redactingHandler`): ein Feldname, der wie ein Credential aussieht (`password`, `secret`, `token`, `totp`, `seed`, `authorization`, `apikey`, `credential`, `cookie`, als Teilstring, unabhängig von Groß-/Kleinschreibung), wird durch `***` ersetzt, bevor er die Ausgabe erreicht — auch verschachtelt in einer Argument-Gruppe.

> **`debug` enthält Suchbegriffe** (das Argument von `search_documents`) und ist damit selbst schon eine — wenn auch begrenzte — inhaltliche Angabe über die Dokumente des aufrufenden Kontos. Für den Dauerbetrieb ist `info` vorgesehen; `debug` ist ein befristetes Werkzeug zum Fehlersuchen, keine Dauereinstellung.

## Entwicklung

```bash
go build ./...
go vet ./...
go test ./... -race -count=1
gofmt -l .                                  # muss leer bleiben
./scripts/coverage-gate-strict.sh cover.out …
./scripts/doc-coverage.sh
```

Voraussetzung: **Go 1.25** oder neuer. Neuer Code folgt strikt TDD — erst ein fehlschlagender Test, dann die Implementierung. Details in [`CONTRIBUTING.md`](CONTRIBUTING.md), Architekturentscheidungen in [`docs/adr/`](docs/adr/).

## Disclaimer

Fileee bietet **kein offizielles API**. Die zugrunde liegende Core-Lib rekonstruiert das interne Protokoll der Web-App. Konsequenzen:

- Fileee kann das interne API jederzeit ohne Ankündigung ändern — dieser Server kann dadurch brechen.
- Die Nutzung ist für **eigene** Fileee-Konten vorgesehen, nicht für fremde Konten oder Massenzugriffe.
- Es gibt **keine Gewähr** für Vollständigkeit, Korrektheit oder Dauerhaftigkeit der Funktionalität.
- Nutzer sind selbst dafür verantwortlich, die Nutzungsbedingungen von Fileee einzuhalten.

## Lizenz

[MIT](LICENSE) — Copyright © 2026 Björn Strausmann
