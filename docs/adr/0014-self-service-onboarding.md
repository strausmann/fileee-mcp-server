# ADR-0014: Self-Service-Onboarding der Fileee-Zugangsdaten über eine eigene Setup-Seite

**Status:** proposed
**Datum:** 2026-08-26
**Ersetzt:** —
**Ersetzt durch:** —
**Überarbeitet:** —
**Überarbeitet durch:** —
**Verwandt:** [ADR-0009](0009-resource-server-statt-eigener-authorization-server.md), [ADR-0010](0010-idp-agnostische-konfiguration.md), [ADR-0012](0012-multi-account-mapping.md), [ADR-0013](0013-prompt-injection-schutz.md), [ADR-0015](0015-gangway-als-unterbau.md), [ADR-0016](0016-anbieter-namensraeume-statt-roher-oidc-parameter.md), [ADR-0018](0018-werkzeug-freigabe-und-client-steuerung.md)

## Kontext

[ADR-0012](0012-multi-account-mapping.md) verlangt für jedes Fileee-Konto vier Secrets, einen
Eintrag in `FILEEE_ACCOUNTS` und einen Neustart. Es nennt selbst den Punkt, an dem das zu kurz
greift: „Wenn fremde Benutzer ihre Fileee-Credentials selbst pflegen sollen." Genau dieser Fall ist
eingetreten. Zwei Kosten fallen an, die nichts mit Technik zu tun haben: der Container ist bei jedem
neuen Konto kurz nicht erreichbar, und der Container-Admin sieht fremde Fileee-Passwörter und
TOTP-Seeds.

Die naheliegende Lösung — nach dem OAuth-Login ein Formular zeigen — ist in dieser Architektur nicht
baubar. Der Redirect-URI ist `https://claude.ai/api/mcp/auth_callback`; der Browser läuft von
Claude.ai zum Identity Provider und zurück zu Claude.ai. Der MCP-Server ist nach
[ADR-0009](0009-resource-server-statt-eigener-authorization-server.md) reiner Resource Server und
liegt nicht in dieser Kette. Ein Redirect auf den MCP-Host wäre die Bauweise „eigener Authorization
Server", vor deren Verwechslung [`docs/idp/claude-connector.md`](../idp/claude-connector.md)
ausdrücklich warnt.

MCP-Elicitation scheidet ebenfalls aus: die Spec 2025-06-18 verbietet sie für sensible Daten
(„Servers MUST NOT use elicitation to request sensitive information"). Passwörter und TOTP-Seeds
dürfen nicht durch den Modellkontext laufen.

**Was sich seit dem ersten Entwurf geändert hat.** Dieser Entwurf entstand am 2026-08-06 gegen ein
Repo, das noch keinen Server hatte. Inzwischen läuft der Server auf Gangway
([ADR-0015](0015-gangway-als-unterbau.md)), die Konfigurations-Oberfläche ist nach Anbietern
getrennt ([ADR-0016](0016-anbieter-namensraeume-statt-roher-oidc-parameter.md)), und die
serverseitige Werkzeug-Freigabe ist entfallen
([ADR-0018](0018-werkzeug-freigabe-und-client-steuerung.md)). Drei Annahmen des ursprünglichen
Entwurfs sind dadurch hinfällig geworden und unten korrigiert: die Ablehnung eines unbekannten
Subjects sieht anders aus als angenommen (Punkt 3), rohe OIDC-Parameter als Konfiguration sind nicht
mehr zulässig (Punkt 2), und der Client-Pool ist bereits bedarfsgesteuert (Punkt 9).

## Entscheidung

1. **Vierte Betriebsart `FILEEE_MODE=self-service`** als dritter Wert der Konto-Auflösungs-Achse aus
   [ADR-0010](0010-idp-agnostische-konfiguration.md). Sie setzt eine verifizierte Identität voraus
   und ist damit nur mit `MCP_AUTH_MODE=oidc` sinnvoll — der Start bricht sonst ab. Diese Regel ist
   derzeit ohne praktische Wirkung, weil der Server `token`/`both` seit ADR-0015 ohnehin ablehnt;
   sie wird trotzdem festgeschrieben, damit sie beim Wiedereinführen des Token-Modus nicht
   vergessen wird.

2. **Eine eigene Setup-Seite auf demselben Host, mit eigenem OIDC-Login.** Der Prozess bedient damit
   zwei getrennte OAuth-Rollen: Resource Server für `POST /mcp` (unverändert, über Gangways
   `AttachMCP`), Relying Party für `/setup` mit Authorization-Code-Flow, PKCE, State und Nonce. Der
   Redirect-URI `https://<mcp-host>/setup/callback` wird einmalig zusätzlich im Identity Provider
   eingetragen. Das ist bewusst **kein** Rückfall in die Bauweise „eigener Authorization Server":
   der MCP-Endpunkt stellt weiterhin keine Tokens aus und bleibt Resource Server.

   **Die Konfiguration folgt ADR-0016, nicht rohen OIDC-Parametern.** Der ursprüngliche Entwurf sah
   `SETUP_OIDC_CLIENT_ID`/`SETUP_OIDC_CLIENT_SECRET` vor — das wäre genau die Oberfläche, die
   ADR-0016 abgeschafft hat. Stattdessen nutzt die Setup-Seite den bereits gewählten
   `MCP_OIDC_PROVIDER` samt dessen Namensraum (`MCP_ENTRA_*`, `MCP_AUTHENTIK_*`, `MCP_OIDC_*`) und
   ergänzt nur, was die Relying-Party-Rolle zusätzlich braucht: ein Client-Secret, sofern der
   Anbieter für den Code-Flow eines verlangt. Der Variablenname liegt damit im Namensraum des
   Anbieters, nicht in einem eigenen `SETUP_OIDC_`-Zweig.

3. **Der Einstiegspunkt ist die bestehende Ablehnung, nicht eine neue Zweistufigkeit.** Der
   ursprüngliche Entwurf ging davon aus, ein unbekanntes Subject bekomme `403` auf `initialize` und
   sähe deshalb nie einen Hinweis — daraus folgte eine aufwendige Aufteilung in „darf onboarden" und
   „ist eingerichtet". **Diese Annahme trifft nicht mehr zu.** Seit ADR-0015 lässt Gangway eine
   Anfrage mit gültigem Token bis zum Werkzeug durch; erst dort schlägt die Konto-Auflösung mit
   `accounts.ErrNoAccount` fehl, und der Aufrufer bekommt **HTTP 200 mit einem fehlgeschlagenen
   Werkzeugergebnis** („access denied"), kein `403`. Ein echtes `403` liefert nur die
   Herkunfts-Freigabeliste `FILEEE_ALLOWED_ORIGIN_PREFIXES` vor der Authentifizierung.

   Die Entscheidung reduziert sich damit auf: **dieses vorhandene Fehlerergebnis trägt im Modus
   `self-service` zusätzlich den Setup-Link.** Kein Eingriff in den Transport-Pfad, keine zweite
   Berechtigungsstufe. Wer im Identity Provider nicht freigegeben ist, scheitert weiterhin vorher —
   die Freigabe, wer überhaupt onboarden darf, bleibt vollständig Sache des Identity Providers bzw.
   `MCP_ALLOWED_SUBJECTS`.

4. **Der Hinweistext ist eine Server-Meldung, kein Dokumentinhalt.** Er unterliegt nicht der
   Untrusted-Markierung aus [ADR-0013](0013-prompt-injection-schutz.md). Ergänzend ein
   Betriebswerkzeug **`setup_status`** — Namenskonvention wie `whoami`, `self_check`,
   `get_tool_manifest`, `get_runtime_stats`, also ohne `fileee_`-Präfix. Nach
   [ADR-0018](0018-werkzeug-freigabe-und-client-steuerung.md) wird es wie jedes Werkzeug für jeden
   authentifizierten Aufrufer angemeldet; die Freigabe trifft der Client.

5. **Zugangsdaten werden vor dem Speichern geprüft.** Der Server baut mit den eingegebenen Werten
   einen `*fileee.Client` mit flüchtigem Session-Store und ruft `EnsureSession`. Erst bei Erfolg
   wird gespeichert. Höchstens fünf Versuche je Subject in fünfzehn Minuten — sonst löst das
   Formular selbst die serverseitige Kontosperre von Fileee aus (`secondsBlocked`), also genau das
   Problem, das der Login-Lock im Client-Pool vermeidet.

6. **Ablage in SQLite über `modernc.org/sqlite`.** Reines Go, damit bleiben `CGO_ENABLED=0` und der
   statische Build aus `deploy/Dockerfile` erhalten. `mattn/go-sqlite3` scheidet deshalb aus.
   Schema-Aufbau beim Start über `PRAGMA user_version`, keine Migrationsbibliothek. Datei mit `0600`
   und `journal_mode=DELETE` — mit WAL lägen sensible Seiten in `-wal`/`-shm` mit Default-Rechten.

7. **Verschlüsselung at rest mit AES-256-GCM**, Schlüssel aus `SETUP_ENCRYPTION_KEY` über das
   bestehende `SECRET_BACKEND`. Je Wert eine eigene 12-Byte-Nonce als Präfix, **AAD ist der
   `subject_hash`**. Ohne AAD ließe sich eine Zeile übernehmen, indem man das Chiffrat einer fremden
   Zeile hineinkopiert. Fehlender oder zu kurzer Schlüssel ist ein Startup-Fehler.

8. **Kein Klartext-Subject in der Datenbank.** Lookup-Schlüssel ist `SHA-256(subject)`, das Subject
   selbst liegt verschlüsselt daneben und wird nur für die Admin-Ansicht entschlüsselt. ADR-0012
   Punkt 10 verbietet das Klartext-Subject bereits im Log; bei `MCP_OIDC_SUBJECT_CLAIM=email` stünde
   sonst die Adresse in der Datei. Der Konto-Schlüssel wird deterministisch aus den ersten 16
   Hex-Zeichen des Hashes abgeleitet und erfüllt damit ohne Zusatzprüfung die Pfad-Traversal-Regel
   aus ADR-0012 Punkt 8.

9. **Der Client-Pool braucht nur noch Invalidierung.** Der ursprüngliche Entwurf verlangte, den Pool
   von „alle Clients beim Start" auf „bedarfsgesteuert" umzubauen. **Das ist bereits erledigt:**
   `internal/clientpool.Pool.For` erzeugt den Client beim ersten Request und serialisiert den Login
   über einen Lock je Konto-Schlüssel. Offen bleibt allein `Invalidate(key)` — nach Änderung oder
   Löschung der Zugangsdaten Client schließen und Session-Datei entfernen. Ohne das liefe der alte
   Client mit alten Zugangsdaten weiter, bis der Prozess neu startet.

10. **ENV-Konten bleiben möglich und unveränderlich.** Beide Quellen dürfen koexistieren. Ein
    Subject, das in `FILEEE_ACCOUNT_<KEY>_SUBJECTS` **und** in der Datenbank steht, bricht den Start
    ab — dieselbe Regel wie ADR-0012 Punkt 4, kein „first match wins". Das Formular verweigert die
    Anlage für ein bereits per ENV abgedecktes Subject.

11. **Die Identität kommt aus `serve.IdentityFrom(ctx)`**, wie ADR-0012 Punkt 2 es seit ADR-0015
    verlangt — nie aus einem eigenen Zwischenspeicher und nie aus einem Header. Für die Setup-Seite
    gilt das nicht: sie ist Relying Party und liest das Subject aus dem selbst geprüften ID-Token
    ihres eigenen Code-Flows. Beide Wege müssen denselben Claim auswerten
    (`MCP_OIDC_SUBJECT_CLAIM`), sonst zeigt der Datenbankeintrag auf ein Subject, das der
    MCP-Pfad nie nachschlägt. Das ist die subtilste Fehlerquelle des ganzen Entwurfs und gehört in
    einen eigenen Test.

12. **Kein Einmal-Link als Einstieg.** Ein signierter Link im Werkzeug-Fehler wäre bequemer und
    spart den zweiten Redirect-URI, ist aber ein Bearer-Credential, das durch den Modellkontext
    läuft. Nur als dokumentierter Rückfallweg denkbar, wenn ein Identity Provider keinen zweiten
    Redirect-URI zulässt.

## Konsequenzen

**Positiv**

- Ein neues Konto kostet keinen Neustart und keine Admin-Beteiligung. Die Downtime bei jeder
  Onboarding-Runde entfällt.
- Der Container-Admin sieht keine fremden Fileee-Passwörter mehr. Wer die Datenbankdatei ohne
  `SETUP_ENCRYPTION_KEY` erbeutet, hat nichts.
- Die Resource-Server-Eigenschaft aus ADR-0009 bleibt unangetastet; die zweite OAuth-Rolle sitzt auf
  einem eigenen Pfad.
- Punkt 3 ist gegenüber dem ersten Entwurf deutlich kleiner geworden: der Einstiegspunkt existiert
  bereits, er bekommt nur einen Link. Der Umbau am Transport-Pfad entfällt ersatzlos.

**Negativ**

- Der Server speichert fremde Passwörter und TOTP-Seeds. Das ist die eigentliche Kröte. Sie wird
  geschluckt, weil `go-fileee` sich nach einem `403` sonst nicht selbst erholen kann und der
  Benutzer bei jedem Session-Ablauf erneut durch das Formular müsste. `SETUP_ENCRYPTION_KEY` ist
  damit das wertvollste Secret des Deployments.
- Zwei OAuth-Rollen in einem Prozess sind erklärungsbedürftig und laden zur Verwechslung mit der
  Bauweise „eigener Authorization Server" ein. Gegenmaßnahme ist ein eigener Abschnitt in
  `docs/idp/claude-connector.md`.
- Der Identity Provider braucht einen zweiten Redirect-URI. Bei Entra ID geht das in derselben
  App-Registrierung, bei Authentik ebenfalls — aber es ist ein zusätzlicher Handgriff bei der
  Einrichtung, und er muss in alle drei Anleitungen unter `docs/idp/`.
- Eine weitere Abhängigkeit: `modernc.org/sqlite`. Sie ist groß, aber reines Go.
- Die Testmatrix wächst um eine Betriebsart. Abgefedert dadurch, dass `self-service` dieselbe
  Konto-Auflösung nutzt und sich nur die Herkunft der Zugangsdaten unterscheidet.

**Wann diese Entscheidung neu zu bewerten ist**

Wenn Fileee ein offizielles API mit delegierter Autorisierung anbietet. Dann entfällt der Grund,
überhaupt Passwörter zu speichern, und das Formular wird durch einen zweiten OAuth-Flow gegen Fileee
ersetzt. Betroffen wären Punkt 5 bis 8.

Ebenso, wenn Gangway eine Relying-Party-Rolle für browserbasierte Abläufe bekommt. Dann wäre Punkt 2
kein Eigenbau mehr, sondern Konfiguration — mit demselben Argument, mit dem ADR-0015 die
Token-Verifikation an Gangway abgegeben hat.

## Referenzen

- [Issue #8](https://github.com/strausmann/fileee-mcp-server/issues/8) — Spezifikation mit Schema,
  Routen und Akzeptanzkriterien; Stand vor der Überarbeitung gegen ADR-0015/0016/0018
- [MCP-Spec 2025-06-18, Elicitation](https://modelcontextprotocol.io/specification/2025-06-18/client/elicitation)
  — Verbot sensibler Daten
- [RFC 9728](https://www.rfc-editor.org/rfc/rfc9728) — Protected Resource Metadata
- [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite)
- `go-fileee` `fileee/auth.go` — `Credentials`, `EnsureSession`, `ErrTwoFactorInvalid`
- `internal/clientpool/pool.go` — `Pool.For`, Login-Lock je Konto-Schlüssel (Grundlage für Punkt 9)
- `internal/accounts/accounts.go` — `ErrNoAccount` (Grundlage für Punkt 3)
