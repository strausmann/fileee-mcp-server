# Beitragen zu fileee-mcp-server

Danke für dein Interesse an diesem Projekt. `fileee-mcp-server` stellt Fileee-Inhalte als MCP-Server bereit und tritt dabei als OAuth-2.1-Resource-Server auf. Der Fileee-Protokoll-Code selbst liegt in der Core-Lib [`go-fileee`](https://github.com/strausmann/go-fileee) — dieses Repo enthält davon **nichts**.

## Bevor du anfängst

- **ADRs lesen:** Architektur-Entscheidungen zu diesem Server stehen unter [`docs/adr/`](docs/adr/) (Nummerierung ab `0009`, siehe [`docs/adr/README.md`](docs/adr/README.md)). Die grundlegenden Entscheidungen zur Core-Lib leben im [go-fileee-Repo](https://github.com/strausmann/go-fileee/tree/main/docs/adr) und werden hier **nicht** dupliziert. Besonders relevant sind dort ADR-0005 (schonender Betrieb / Rate-Limiting) und ADR-0007 (Ausschluss destruktiver Operationen).
- Neue Fileee-Protokoll-Abdeckung (neue Entity, neuer Endpunkt bei `my.fileee.com`) gehört ins go-fileee-Repo, nicht hierher.
- Für größere Änderungen erst ein Issue eröffnen und die Richtung abstimmen.

## Entwicklungs-Workflow

1. Branch anlegen — kein Direkt-Push auf `main`.
2. Änderungen **strikt TDD** umsetzen: zuerst einen fehlschlagenden Test schreiben, dann die Implementierung, bis der Test grün ist.
3. Mutations-Pfade (alle Tools der Gruppen `write`, `share`, `destructive`) decken mindestens ab: Happy-Path, Error-Path (4xx/5xx von go-fileee) und Netzwerkfehler/Timeout.
4. Lokal vor dem Commit prüfen:
   ```bash
   go build ./...
   go vet ./...
   go test ./... -race -coverprofile=cover.out
   ./scripts/coverage-gate-strict.sh cover.out cmd/fileee-mcp-server/<geänderte-datei>.go:<schwelle>
   ./scripts/doc-coverage.sh
   gofmt -l .    # muss leer bleiben
   ```
5. **Doku im selben PR nachziehen:** neue oder geänderte Tools in `docs/tools.md`, neue Konfigurationsvariablen in `README.md`, Änderungen am Auth-Verhalten in `docs/idp/`.
6. Enthält die Änderung eine Architektur- oder Technologie-Entscheidung: ADR unter `docs/adr/` anlegen (nächste freie Nummer) und in [`docs/adr/README.md`](docs/adr/README.md) registrieren.

## Coverage-Schwellen

Die Schwellen pro Datei stehen in [`.github/workflows/test.yml`](.github/workflows/test.yml). Anders als in den Schwester-Repos sind sie hier **keine** an den Ist-Stand angelehnten Floors, sondern kommen aus der Regel-Tabelle:

| Kategorie | Schwelle | Dateien |
|---|---|---|
| Auth/Permission | 90 | `auth_oidc.go`, `auth_token.go`, `accounts.go`, `capabilities.go`, `config.go` |
| Mutations-Logik | 85 | `clientpool.go`, `tools_destructive.go`, mutierende Tool-Pfade |
| Business-Logik | 80 | übrige `tools_*.go`, `server.go`, `errors.go` |
| Adapter/Boot | 60 | `main.go`, `secrets.go` |

Jede neue Datei bekommt ihre Schwelle **im selben PR**, in dem sie entsteht. Nachträglich nachziehen heißt, das Gate für diese Datei nie scharf zu schalten.

## Besondere Sorgfalt

Drei Bereiche sind sicherheitskritisch und brauchen im PR eine ausdrückliche Begründung:

- **Konto-Auflösung.** Die Benutzeridentität wird ausschließlich aus `req.GetExtra().TokenInfo` gelesen, das pro Request neu gesetzt wird — **nie** aus dem Handler-Kontext. `auth.TokenInfoFromContext` liefert im Tool-Handler das Token des `initialize`-Requests und würde bei mehreren Benutzern pro Sitzung auf das falsche Fileee-Konto auflösen.
- **`TokenInfo.UserID`.** Der Token-Verifier muss dieses Feld setzen, sonst greift der Session-Hijacking-Schutz des SDK nicht.
- **Tool-Ausgaben.** Dokumentinhalte und OCR-Text sind fremdbestimmte Daten und können an das Modell gerichtete Anweisungen enthalten. Sie werden als nicht vertrauenswürdig gekennzeichnet zurückgegeben.

## Commit-Konvention

Commit-Messages folgen [Conventional Commits](https://www.conventionalcommits.org/) und werden per Husky-Hook + [commitlint](https://commitlint.js.org/) geprüft (`.commitlintrc.json`, `@commitlint/config-conventional`):

- **Typ:** `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`, `ci:` — auf Deutsch formuliert, z. B. `fix(accounts): doppeltes subject bricht den start ab`.
- **Subject in Kleinbuchstaben** (`subject-case`-Regel) — kein großgeschriebener Satzanfang.
- **Scope** aus der festen Liste in `.commitlintrc.json` (`server`, `config`, `secrets`, `auth`, `accounts`, `tools`, `capabilities`, `deploy`, `adr`, `ci`, `deps`, `docs`, `release`) — kein neuer Scope ohne Anpassung der Datei.
- **Issue-Referenz:** `Refs #N` oder `Closes #N` in Commit oder PR-Beschreibung.

Der `commit-msg`-Hook läuft nach `npm install` (Husky via `"prepare": "husky"`). **Nie `git commit --no-verify` verwenden.**

## Pull Requests

- Ziel-Branch ist `main`. `main` ist geschützt — nur PRs mit grüner CI.
- CI-Gates: `test.yml` (build, vet, race-Tests, Coverage-Gate, Doc-Coverage) und `commitlint.yml`.
- PR-Beschreibung nutzt die [Vorlage](.github/PULL_REQUEST_TEMPLATE.md).
- Kleine, fokussierte PRs bevorzugt.

## Fragen oder Bugs melden

- **Bug:** [Bug-Report-Vorlage](.github/ISSUE_TEMPLATE/bug_report.md).
- **Feature-Wunsch:** [Feature-Request-Vorlage](.github/ISSUE_TEMPLATE/feature_request.md).

## Sicherheit

Fileee-Credentials, TOTP-Seeds, das statische API-Token und alle IdP-Secrets gehören niemals in Code, Tests, Issues oder Compose-Dateien im Klartext — Platzhalter (`CHANGE_ME`) in Vorlagen, echte Werte ausschließlich in `.env` oder einem Secret-Backend. Dasselbe gilt für Dokumentinhalte und Metadaten: das sind personenbezogene Daten und gehören nicht in Test-Fixtures.

Sicherheitsrelevante Funde bitte **nicht** als öffentliches Issue melden, sondern den Maintainer direkt kontaktieren.
