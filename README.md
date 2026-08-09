# fileee-mcp-server

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Ein **inoffizieller** MCP-Server für [Fileee](https://www.fileee.com), der die eigenen Dokumente für AI-Clients zugänglich macht — als lokaler Server über einen statischen Token oder als **Remote-Connector mit OAuth-Anmeldung**, etwa in der Claude.ai-Web-UI.

> **Stand:** Das Grundgerüst steht — Konfiguration, Anmeldung über [Gangway](https://gangway.strausmann.cloud), Zuordnung von Identität zu Fileee-Konto und die ersten lesenden Werkzeuge sind eingerichtet. Weitere Werkzeuge und Capability-Gruppen (`write`, `share`, `destructive`) entstehen in den folgenden Umsetzungsschritten.

Der Server nutzt die Core-Lib [`strausmann/go-fileee`](https://github.com/strausmann/go-fileee) und ist damit Geschwisterprojekt von [`strausmann/fileee-server`](https://github.com/strausmann/fileee-server) (REST-API für n8n/CI). Der Unterschied: `fileee-server` kennt genau ein Fileee-Konto und ein statisches Token; dieser Server bindet die **Identität des anfragenden Benutzers** an ein Fileee-Konto.

*Dieses Projekt ist ein unabhängiges Community-Projekt und steht in keiner Verbindung zur fileee GmbH.*

## Was es kann

- **MCP über Streamable HTTP** (`POST /mcp`), auf Basis des offiziellen [Go-SDK](https://github.com/modelcontextprotocol/go-sdk)
- **OAuth 2.1 als Resource Server** nach [RFC 9728](https://www.rfc-editor.org/rfc/rfc9728) — der Identity Provider ist frei wählbar und reine Konfiguration
- **Statisches Bearer-Token** als Alternative, wenn kein IdP vorhanden ist
- **Ein oder mehrere Fileee-Konten**, zugeordnet über einen signierten Claim aus dem Token
- **Konfigurierbarer Funktionsumfang** über Capability-Gruppen — nicht freigeschaltete Tools werden gar nicht erst registriert

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
FILEEE_CAPABILITIES=read
```

Drei Pflichtwerte, kein IdP, kein Reverse Proxy nötig. Für Claude Code lokal oder für Automatisierung im eigenen Netz.

### Remote-Connector mit OAuth

```dotenv
MCP_AUTH_MODE=oidc
MCP_OIDC_ISSUER=https://<idp-host>/…
MCP_OIDC_AUDIENCE=<client-id>
MCP_RESOURCE_URL=https://<mcp-host>/mcp
MCP_ALLOWED_SUBJECTS=<sub des berechtigten Benutzers>
FILEEE_ALLOWED_ORIGIN_PREFIXES=<CIDR-Liste der zulässigen Herkunftsadressen>
FILEEE_MODE=single
```

Einrichtung des Identity Providers: [`docs/idp/authentik.md`](docs/idp/authentik.md), [`docs/idp/entra-id.md`](docs/idp/entra-id.md), danach [`docs/idp/claude-connector.md`](docs/idp/claude-connector.md).

> **Aktuell nur `oidc`.** Der Server läuft auf [Gangway](https://gangway.strausmann.cloud) (siehe [ADR-0015](docs/adr/0015-gangway-als-unterbau.md)) auf, und Gangway v0.2.0 baut intern ausschließlich einen OIDC-Verifier — es gibt (noch) keinen Weg, stattdessen ein statisches Bearer-Token zu verifizieren. `MCP_AUTH_MODE=token`/`both` werden von `LoadConfig` weiterhin akzeptiert, der Server verweigert den Start mit diesem Modus aber explizit. Details und der Ausblick auf eine Lösung stehen im Nachtrag zu ADR-0015.

Weitere netzwerkbezogene Variablen, die im Modus `oidc` Pflicht bzw. relevant sind:

| Variable | Zweck | Default |
|---|---|---|
| `FILEEE_ALLOWED_ORIGIN_PREFIXES` | CIDR-Liste (oder einzelne Adressen) der Herkunftsadressen, die `/mcp` überhaupt erreichen dürfen — Pflicht im Modus `oidc`, ohne sie verweigert Gangway den Start | — |
| `FILEEE_TRUSTED_PROXIES` | CIDR-Liste der Proxys, deren Weiterleitungs-Header (siehe `FILEEE_CLIENT_IP_HEADER_MODE`) geglaubt werden | leer — es zählt nur die Peer-Adresse |
| `FILEEE_CLIENT_IP_HEADER_MODE` | genau ein Weiterleitungs-Header als Quelle der Client-Adresse: `x-forwarded-for`, `x-real-ip` oder `cf-connecting-ip` | `cf-connecting-ip` — vor dem Produktivbetrieb gegen die tatsächliche Proxy-Kette (z. B. Pangolin/Traefik) prüfen |

### Mehrere Benutzer, je eigenes Fileee-Konto

```dotenv
MCP_AUTH_MODE=oidc
FILEEE_MODE=multi
FILEEE_ACCOUNTS=alice,bob
FILEEE_ACCOUNT_ALICE_USERNAME=…
FILEEE_ACCOUNT_ALICE_PASSWORD=…
FILEEE_ACCOUNT_ALICE_SUBJECTS=alice@example.com
FILEEE_ACCOUNT_ALICE_CAPABILITIES=read
FILEEE_ACCOUNT_BOB_USERNAME=…
…
```

Die Zuordnung läuft über einen konfigurierbaren Claim aus dem Token (Default `sub`). Mehrere Identitäten dürfen auf **ein** Fileee-Konto zeigen; eine Identität auf zwei Konten ist ein Startup-Fehler, kein „first match wins". Ein unbekanntes Subject bekommt `403` — es gibt keinen Fallback auf ein Standardkonto.

## Funktionsumfang festlegen

```dotenv
FILEEE_CAPABILITIES=read                              # Default
FILEEE_CAPABILITIES=read,write
FILEEE_CAPABILITIES=read,write,share
FILEEE_CAPABILITIES=read,write,share,destructive      # zusätzlich FILEEE_ALLOW_DESTRUCTIVE=true
```

| Gruppe | Umfang |
|---|---|
| `read` | Suche, Dokument-Metadaten, OCR-Text, PDF-/Seiten-Download, Stammdaten, Kontakte/Erinnerungen/Boxen/Konversationen lesen |
| `write` | Upload, Metadaten ändern, Erinnerungen und Kontakte anlegen/ändern, Box-Zuordnung |
| `share` | Freigabe-Links, ZIP-Export, Konversations-Nachrichten und -Teilnehmer |
| `destructive` | Hard-DELETE von Dokumenten, Kontakten und Erinnerungen — **doppeltes Gate** |

Nicht freigeschaltete Tools werden dem Client gar nicht erst angeboten.

### Wer wie viel darf

Der Umfang kann aus drei Quellen kommen. Es gilt eine feste Rangfolge, keine Vermischung:

1. **`FILEEE_CAPABILITIES` ist die Obergrenze.** Keine andere Quelle schaltet darüber hinaus etwas frei.
2. **Der Identity Provider entscheidet**, sofern `MCP_OIDC_CAPABILITY_CLAIM` gesetzt ist — Entra über App-Rollen (`roles`), Authentik über Gruppen (`groups`). Damit werden Berechtigungen dort gepflegt, wo Benutzer ohnehin verwaltet werden. Für die meisten Setups genügen zwei Stufen: `read` und `write`.
3. **Sonst** gilt `FILEEE_ACCOUNT_<KEY>_CAPABILITIES`, sonst die Obergrenze.

Ist der Claim konfiguriert, der Benutzer hat aber keine passende Rolle oder Gruppe, bekommt er **`read`** — nicht den konfigurierten Standardumfang. Andernfalls wäre eine vergessene Zuweisung eine stille Rechteausweitung.

`destructive` ist über keinen Claim erreichbar und bleibt eine bewusste Entscheidung am Server.

Fileees Hard-DELETE ist unwiderruflich und kennt keinen Papierkorb. Deshalb die zwei Schalter, ein Audit-Log vor jeder Löschung und die Regel, dass eine zu löschende ID aus einer vorangegangenen Leseantwort derselben Sitzung stammen muss.

## Werkzeuge

Der Katalog entsteht schrittweise. Was heute existiert (`read`: `list_documents`, `search_documents`) steht in [`docs/tools.md`](docs/tools.md), inklusive der Absicherung gegen präparierte Dokumenttitel.

## Sicherheit

- **Credentials** (Fileee-Zugangsdaten, TOTP-Seed, API-Token) gehören ausschließlich in einen Secret-Manager, nie in Code oder Commits. Der Container unterstützt neben `.env` einen Infisical-Modus.
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

`FILEEE_MAX_DOWNLOAD_BYTES` und `FILEEE_MAX_UPLOAD_BYTES` werden geladen, aber **noch nicht durchgesetzt** — nicht weil die Fähigkeit fehlt (`go-fileee`s `DocumentService.Upload`/`DownloadPDF`/`DownloadPageImage` sowie die freigabe-seitigen Gegenstücke in `ShareClient` existieren bereits), sondern weil dieser Server noch kein Werkzeug registriert, das sie aufruft — die Grenze bekommt ihren Platz um den `io.Reader`/`io.ReadCloser` dieser Methoden herum, sobald das erste Upload-/Download-Werkzeug entsteht. Die davon unabhängig abgeleitete `MaxRequestBodyBytes` bleibt aus einem anderen Grund offen: Gangway v0.2.0 baut den HTTP-Handler intern ohne einen Weg, dessen Größenlimit zu überschreiben (siehe [ADR-0015](docs/adr/0015-gangway-als-unterbau.md)).

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
