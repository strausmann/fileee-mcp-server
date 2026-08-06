# fileee-mcp-server

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Ein **inoffizieller** MCP-Server für [Fileee](https://www.fileee.com), der die eigenen Dokumente für AI-Clients zugänglich macht — als lokaler Server über einen statischen Token oder als **Remote-Connector mit OAuth-Anmeldung**, etwa in der Claude.ai-Web-UI.

> **Status:** Gerüst. Konfiguration, Auth, Konto-Auflösung und Tools entstehen in den folgenden Umsetzungsschritten. Dieses README beschreibt das Zielbild und wird schrittweise konkretisiert.

Der Server nutzt die Core-Lib [`strausmann/go-fileee`](https://github.com/strausmann/go-fileee) und ist damit Geschwisterprojekt von [`strausmann/fileee-server`](https://github.com/strausmann/fileee-server) (REST-API für n8n/CI). Der Unterschied: `fileee-server` kennt genau ein Fileee-Konto und ein statisches Token; dieser Server bindet die **Identität des anfragenden Benutzers** an ein Fileee-Konto.

*Dieses Projekt ist ein unabhängiges Community-Projekt und steht in keiner Verbindung zur fileee GmbH.*

## Was es kann

- **MCP über Streamable HTTP** (`POST /mcp`), auf Basis des offiziellen [Go-SDK](https://github.com/modelcontextprotocol/go-sdk)
- **OAuth 2.1 als Resource Server** nach [RFC 9728](https://www.rfc-editor.org/rfc/rfc9728) — der Identity Provider ist frei wählbar und reine Konfiguration
- **Statisches Bearer-Token** als Alternative, wenn kein IdP vorhanden ist
- **Ein oder mehrere Fileee-Konten**, zugeordnet über einen signierten Claim aus dem Token
- **Konfigurierbarer Funktionsumfang** über Capability-Gruppen — nicht freigeschaltete Tools werden gar nicht erst registriert

## Vier Betriebsarten

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
FILEEE_MODE=single
```

Einrichtung des Identity Providers: [`docs/idp/authentik.md`](docs/idp/authentik.md), [`docs/idp/entra-id.md`](docs/idp/entra-id.md), danach [`docs/idp/claude-connector.md`](docs/idp/claude-connector.md).

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

### Mehrere Benutzer, die ihre Zugangsdaten selbst hinterlegen

```dotenv
MCP_AUTH_MODE=oidc
FILEEE_MODE=self-service
SETUP_BASE_URL=https://<mcp-host>
SETUP_OIDC_CLIENT_ID=<client-id>
SETUP_OIDC_CLIENT_SECRET=CHANGE_ME
SETUP_ENCRYPTION_KEY=CHANGE_ME        # openssl rand -base64 32
SETUP_DB_PATH=/data/accounts.db       # Default
```

Statt je Konto vier Secrets und einen Neustart hinterlegt jede Person ihre Fileee-Zugangsdaten selbst unter `https://<mcp-host>/setup`. Der Ablauf: Connector in Claude eintragen, verbinden, ein Tool aufrufen — der Server antwortet mit einem Hinweis samt Setup-Link. Dort meldet sich die Person nochmals am Identity Provider an und trägt Benutzername, Passwort und optional den TOTP-Seed ein. Die Daten werden gegen Fileee geprüft, verschlüsselt gespeichert und an das Subject aus dem Token gebunden.

Dafür braucht der Identity Provider einen **zweiten Redirect-URI** `https://<mcp-host>/setup/callback` — der MCP-Endpunkt selbst bleibt reiner Resource Server. Wer nicht in der Gruppenbindung bzw. `MCP_ALLOWED_SUBJECTS` steht, kommt gar nicht erst bis zum Formular und bekommt weiterhin `403`.

ENV-Konten und Self-Service dürfen nebeneinander bestehen; ein Subject in beiden Quellen bricht den Start ab. Details in [ADR-0014](docs/adr/0014-self-service-onboarding.md).

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

## Sicherheit

- **Credentials** (Fileee-Zugangsdaten, TOTP-Seed, API-Token) gehören ausschließlich in einen Secret-Manager, nie in Code oder Commits. Der Container unterstützt neben `.env` einen Infisical-Modus.
- **Session-Dateien** des Client-Pools sind Secrets (`0600`, je Konto getrennt) und werden nie geloggt.
- **Self-Service-Zugangsdaten** liegen AES-256-GCM-verschlüsselt in der SQLite-Datei (`0600`), der Schlüssel kommt aus `SETUP_ENCRYPTION_KEY` über das Secret-Backend. Das Subject steht nur als SHA-256-Hash im Klartext. Ohne den Schlüssel ist die Datei wertlos — er ist damit das wertvollste Secret des Deployments und gehört nicht neben die Datenbank auf dasselbe Volume.
- **Dokumentinhalte sind fremdbestimmte Daten.** OCR-Text kann Anweisungen enthalten, die an das Modell gerichtet sind. Tool-Ausgaben werden deshalb als nicht vertrauenswürdig markiert, und destruktive Operationen sind zusätzlich abgesichert.
- Die Core-Lib **schont Fileees Infrastruktur** über Rate-Limiting und Backoff. Dieser Server ergänzt einen globalen Deckel über alle Konten hinweg, damit mehrere Konten die Last nicht vervielfachen.

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
