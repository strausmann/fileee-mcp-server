# ADR-0014: Self-Service-Onboarding der Fileee-Zugangsdaten über eine eigene Setup-Seite

**Status:** accepted
**Datum:** 2026-08-06
**Ersetzt:** —
**Ersetzt durch:** —
**Verwandt:** [ADR-0009](0009-resource-server-statt-eigener-authorization-server.md), [ADR-0010](0010-idp-agnostische-konfiguration.md), [ADR-0012](0012-multi-account-mapping.md), [ADR-0013](0013-prompt-injection-schutz.md)

## Kontext

ADR-0012 verlangt für jedes Fileee-Konto vier Secrets, einen Eintrag in `FILEEE_ACCOUNTS` und einen Neustart. Es nennt selbst den Punkt, an dem das zu kurz greift: „Wenn fremde Benutzer ihre Fileee-Credentials selbst pflegen sollen." Genau dieser Fall ist eingetreten. Zwei Kosten fallen an, die nichts mit Technik zu tun haben: der Container ist bei jedem neuen Konto kurz nicht erreichbar, und der Container-Admin sieht fremde Fileee-Passwörter und TOTP-Seeds.

Die naheliegende Lösung — nach dem OAuth-Login ein Formular zeigen — ist in dieser Architektur nicht baubar. Der Redirect-URI ist `https://claude.ai/api/mcp/auth_callback`; der Browser läuft von Claude.ai zum Identity Provider und zurück zu Claude.ai. Der MCP-Server ist nach ADR-0009 reiner Resource Server und liegt nicht in dieser Kette. Ein Redirect auf den MCP-Host wäre die Bauweise „eigener Authorization Server", vor deren Verwechslung `docs/idp/claude-connector.md` ausdrücklich warnt.

MCP-Elicitation scheidet ebenfalls aus: die Spec 2025-06-18 verbietet sie für sensible Daten („Servers MUST NOT use elicitation to request sensitive information"). Passwörter und TOTP-Seeds dürfen nicht durch den Modellkontext laufen.

## Entscheidung

1. **Vierte Betriebsart `FILEEE_MODE=self-service`** als dritter Wert der Konto-Auflösungs-Achse aus ADR-0010. Sie erfordert `MCP_AUTH_MODE=oidc`; bei `both` gilt nur der JWT-Pfad. Die Kombination mit `token` bricht den Start ab — ein statisches Token trägt kein Subject, es gibt nichts zuzuordnen.

2. **Eine eigene Setup-Seite auf demselben Host, mit eigenem OIDC-Login.** Der Prozess bedient damit zwei getrennte OAuth-Rollen: Resource Server für `POST /mcp` (unverändert), Relying Party für `/setup` mit Authorization-Code-Flow, PKCE, State und Nonce. Der Redirect-URI `https://<mcp-host>/setup/callback` wird einmalig zusätzlich im Identity Provider eingetragen. Das ist bewusst **kein** Rückfall in die Bauweise „eigener Authorization Server": der MCP-Endpunkt stellt weiterhin keine Tokens aus und bleibt Resource Server.

3. **Die Berechtigung zerfällt in zwei Stufen.** „Darf onboarden" entscheidet der Identity Provider über Gruppe/Rolle bzw. `MCP_ALLOWED_SUBJECTS`; wer das nicht erfüllt, bekommt weiterhin `403` auf Transport-Ebene. „Ist eingerichtet" entscheidet der Datenbankeintrag; fehlt er, gelingt `initialize` und jeder Tool-Aufruf liefert einen Hinweistext mit dem Setup-Link. Ohne diese Trennung sieht der Benutzer den Link nie, weil Claude bei `403` auf `initialize` nur „Verbindung fehlgeschlagen" anzeigt.

4. **Der Hinweistext ist eine Server-Meldung, kein Dokumentinhalt.** Er wird als `isError` zurückgegeben und unterliegt nicht der Untrusted-Markierung aus ADR-0013. Ergänzend ein immer registriertes Meta-Tool `fileee_setup_status`, das Einrichtungsstand und Link liefert, unabhängig von `FILEEE_CAPABILITIES`.

5. **Zugangsdaten werden vor dem Speichern geprüft.** Der Server baut mit den eingegebenen Werten einen `*fileee.Client` mit flüchtigem Session-Store und ruft `EnsureSession`. Erst bei Erfolg wird gespeichert. Höchstens fünf Versuche je Subject in fünfzehn Minuten — sonst löst das Formular selbst die serverseitige Kontosperre von Fileee aus (`secondsBlocked`), also genau das Problem, das ADR-0012 Punkt 6 mit `singleflight` vermeidet.

6. **Ablage in SQLite über `modernc.org/sqlite`.** Reines Go, damit bleiben `CGO_ENABLED=0` und der statische Build erhalten. `mattn/go-sqlite3` scheidet deshalb aus. Schema-Aufbau beim Start über `PRAGMA user_version`, keine Migrationsbibliothek. Datei mit `0600` und `journal_mode=DELETE` — mit WAL lägen sensible Seiten in `-wal`/`-shm` mit Default-Rechten.

7. **Verschlüsselung at rest mit AES-256-GCM**, Schlüssel aus `SETUP_ENCRYPTION_KEY` über das bestehende `SECRET_BACKEND`. Je Wert eine eigene 12-Byte-Nonce als Präfix, **AAD ist der `subject_hash`**. Ohne AAD ließe sich eine Zeile übernehmen, indem man das Chiffrat einer fremden Zeile hineinkopiert. Fehlender oder zu kurzer Schlüssel ist ein Startup-Fehler.

8. **Kein Klartext-Subject in der Datenbank.** Lookup-Schlüssel ist `SHA-256(subject)`, das Subject selbst liegt verschlüsselt daneben und wird nur für die Admin-Ansicht entschlüsselt. ADR-0012 Punkt 10 verbietet das Klartext-Subject bereits im Log; bei `MCP_OIDC_SUBJECT_CLAIM=email` stünde sonst die Adresse in der Datei. Der `account_key` wird deterministisch aus den ersten 16 Hex-Zeichen des Hashes abgeleitet und erfüllt damit ohne Zusatzprüfung die Pfad-Traversal-Regel aus ADR-0012 Punkt 8.

9. **Der Client-Pool wird lazy und invalidierbar.** ADR-0012 Punkt 6 legt Clients beim Start an; das geht nicht mehr, wenn Konten zur Laufzeit entstehen. `Get(key)` erzeugt beim ersten Request, weiterhin über `singleflight` serialisiert. `Invalidate(key)` nach Änderung oder Löschung schließt den Client und löscht die Session-Datei — ohne das liefe der alte Client mit alten Zugangsdaten weiter. Der globale Rate-Limiter aus ADR-0012 Punkt 7 bleibt unverändert.

10. **ENV-Konten bleiben möglich und unveränderlich.** Beide Quellen dürfen koexistieren. Ein Subject, das in `FILEEE_ACCOUNT_<KEY>_SUBJECTS` **und** in der Datenbank steht, bricht den Start ab — dieselbe Regel wie ADR-0012 Punkt 4, kein „first match wins". Das Formular verweigert die Anlage für ein bereits per ENV abgedecktes Subject.

11. **Kein Einmal-Link als Einstieg.** Ein signierter Link im Tool-Fehler wäre bequemer und spart den zweiten Redirect-URI, ist aber ein Bearer-Credential, das durch den Modellkontext läuft. Nur als dokumentierter Rückfallweg denkbar, wenn ein Identity Provider keinen zweiten Redirect-URI zulässt.

## Konsequenzen

**Positiv**

- Ein neues Konto kostet keinen Neustart und keine Admin-Beteiligung. Die Downtime bei jeder Onboarding-Runde entfällt.
- Der Container-Admin sieht keine fremden Fileee-Passwörter mehr. Wer die Datenbankdatei ohne `SETUP_ENCRYPTION_KEY` erbeutet, hat nichts.
- Die Resource-Server-Eigenschaft aus ADR-0009 bleibt unangetastet; die zweite OAuth-Rolle sitzt auf einem eigenen Pfad.
- Punkt 3 macht den Fehlerfall „noch nicht eingerichtet" überhaupt erst sichtbar. Bisher wäre er als unspezifischer Verbindungsfehler geendet.

**Negativ**

- Der Server speichert fremde Passwörter und TOTP-Seeds. Das ist die eigentliche Kröte. Sie wird geschluckt, weil `go-fileee` sich nach einem `403` sonst nicht selbst erholen kann und der Benutzer bei jedem Session-Ablauf erneut durch das Formular müsste. `SETUP_ENCRYPTION_KEY` ist damit das wertvollste Secret des Deployments.
- Zwei OAuth-Rollen in einem Prozess sind erklärungsbedürftig und laden zur Verwechslung mit der Bauweise „eigener Authorization Server" ein. Gegenmaßnahme ist ein eigener Abschnitt in `docs/idp/claude-connector.md`.
- Der Identity Provider braucht einen zweiten Redirect-URI. Bei Entra ID geht das in derselben App-Registrierung, bei Authentik ebenfalls — aber es ist ein zusätzlicher Handgriff bei der Einrichtung.
- Erste Abhängigkeit des Repos überhaupt neben dem MCP-SDK: `modernc.org/sqlite`. Sie ist groß, aber reines Go.
- Die Testmatrix wächst um eine Betriebsart. Abgefedert dadurch, dass `self-service` dieselbe Konto-Auflösung nutzt und sich nur die Herkunft der Zugangsdaten unterscheidet.

**Wann diese Entscheidung neu zu bewerten ist**

Wenn Fileee ein offizielles API mit delegierter Autorisierung anbietet. Dann entfällt der Grund, überhaupt Passwörter zu speichern, und das Formular wird durch einen zweiten OAuth-Flow gegen Fileee ersetzt. Betroffen wären Punkt 5 bis 8.

## Referenzen

- [Issue #8](https://github.com/strausmann/fileee-mcp-server/issues/8) — vollständige Spezifikation inklusive Schema, Routen und Akzeptanzkriterien
- [MCP-Spec 2025-06-18, Elicitation](https://modelcontextprotocol.io/specification/2025-06-18/client/elicitation) — Verbot sensibler Daten
- [RFC 9728](https://www.rfc-editor.org/rfc/rfc9728) — Protected Resource Metadata
- [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite)
- `go-fileee` `fileee/auth.go` — `Credentials`, `EnsureSession`, `ErrTwoFactorInvalid`
