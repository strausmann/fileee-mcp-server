## Beschreibung

Was ändert dieser PR und warum?

## Bezug

Refs #… <!-- oder: Closes #… -->

## Checkliste

- [ ] **TDD strict eingehalten** — Test zuerst geschrieben, dann Implementierung.
- [ ] Mutations-Pfade (Tools der Gruppen `write`, `share`, `destructive`) decken Happy-Path,
      Error-Path (4xx/5xx) und Netzwerkfehler/Timeout ab, falls zutreffend.
- [ ] `go build ./...`, `go vet ./...` und `go test ./... -race` laufen lokal grün, `gofmt -l .`
      ist leer.
- [ ] `./scripts/coverage-gate-strict.sh` besteht für geänderte Dateien; **neue** Dateien haben
      ihre Schwelle in `.github/workflows/test.yml` bekommen.
- [ ] `./scripts/doc-coverage.sh` meldet 0 undokumentierte Exports.
- [ ] Doku nachgezogen — neue/geänderte Tools in `docs/tools.md`, neue Konfigurationsvariablen in
      `README.md`, Auth-Verhalten in `docs/idp/`.
- [ ] Neues ADR angelegt und in `docs/adr/README.md` registriert, falls eine Architektur-/
      Technologie-Entscheidung enthalten ist.
- [ ] Commit-Messages sind Conventional-Commits-konform (Kleinbuchstaben-Subject, gültiger Scope
      aus `.commitlintrc.json`).
- [ ] Keine Credentials, Tokens oder echten Dokumentdaten im Klartext in Code, Tests oder
      Compose-Dateien.

## Sicherheitsfragen (bei Änderungen an Auth, Konto-Auflösung oder Tools)

- [ ] Die Konto-Auflösung liest die Identität aus `req.GetExtra().TokenInfo` und **nicht** aus dem
      Handler-Kontext.
- [ ] `TokenInfo.UserID` wird vom Verifier gesetzt (Session-Hijacking-Schutz des SDK).
- [ ] Tool-Ausgaben mit Dokumentinhalten sind als nicht vertrauenswürdig gekennzeichnet.

## Testnachweis

Wie wurde die Änderung verifiziert (Testlauf-Output, manueller Tool-Aufruf, `curl` gegen
`/mcp` bzw. die Discovery-Pfade)?
