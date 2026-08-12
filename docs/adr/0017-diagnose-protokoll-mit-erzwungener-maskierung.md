# ADR-0017: Diagnose-Protokoll mit erzwungener Maskierung statt vertrauensbasierter Feldwahl

**Status:** accepted
**Datum:** 2026-08-12
**Ersetzt:** —
**Ersetzt durch:** —
**Überarbeitet:** —
**Überarbeitet durch:** —
**Verwandt:** [ADR-0013](0013-prompt-injection-schutz.md), [ADR-0015](0015-gangway-als-unterbau.md)

## Kontext

Der Server protokollierte bislang nichts über einzelne Werkzeugaufrufe: weder welches Werkzeug lief, noch mit welchem Ergebnis, noch wie eine Anfrage an Fileee weitergereicht wurde. `FILEEE_LOG_LEVEL` wurde zwar aus der Umgebung gelesen (`internal/config`), hatte aber keinen Konsumenten — ein Level ohne Protokollier-Schicht wäre Scheinarbeit gewesen.

Konkreter Anlass: Ein Betreiber verband den claude.ai-Konnektor erfolgreich, bekam aber keine Werkzeuge angeboten, und es gab kein Mittel herauszufinden, warum — weder ob der OIDC-Selector eine leere Fähigkeitsmenge auflöste, noch ob und mit welchem Ergebnis überhaupt ein Werkzeug aufgerufen wurde.

Ein Diagnose-Protokoll für genau diesen Server bringt ein eigenes Risiko mit, das eine gewöhnliche REST-API nicht in derselben Schärfe hat: Werkzeugaufrufe tragen Suchbegriffe, und Fileee-Antworten tragen Dokumenttitel und Fehlertexte — beides fremdbestimmter Inhalt im Sinne von [ADR-0013](0013-prompt-injection-schutz.md), zusätzlich zu den gewöhnlichen Zugangsdaten (Passwort, TOTP-Seed, statisches API-Token), die dieser Server ohnehin verwaltet. Ein Logging-Aufruf, der versehentlich das falsche Feld protokolliert, ist ein Leck — und die Erfahrung aus dem Betrieb dieser Repo-Familie (siehe `internal/diag`s eigener Doc-Kommentar zu vergangenen Vorfällen in Schwester-Projekten) zeigt, dass sich darauf, dass jede künftige Logging-Stelle die richtigen Feldnamen kennt, nicht verlassen werden kann.

## Entscheidung

1. **Ein eigenes Paket (`internal/diag`) baut den Logger, keine Stelle loggt an ihm vorbei.** `internal/server.New` baut ihn genau einmal und reicht ihn unverändert an `internal/tools.RegisterRead` (Werkzeugaufrufe) und an `fileee.WithLogger` (go-fileees eigenes Transport-Protokoll, `clientpool.WithClientOptions`) weiter — derselbe Wert, nicht zwei parallele Logging-Wege.

2. **Zwei Stufen, `info` (Default) und `debug`, nicht mehr.** `info` protokolliert Betriebszustand: Werkzeugname, Dauer, eine feste Ergebnisart (nie eine freie Fehlermeldung — siehe Punkt 4), den erreichten Fileee-Endpunkt, die Trefferzahl bei Erfolg, und je aufgelöster Anfrage die vom Selector ermittelte Fähigkeitsmenge samt Werkzeuganzahl. `debug` ergänzt ausschließlich die vom Aufrufer übergebenen Werkzeug-Argumente — bewusst eine eigene, höhere Stufe: ein Suchbegriff ist bereits eine inhaltliche Angabe über die Dokumente des Kontos, kein reiner Betriebszustand.

3. **Maskierung ist eine erzwungene Eigenschaft des `slog.Handler`, keine Konvention am Aufrufer.** `internal/diag`s `redactingHandler` prüft **jedes** Attribut, das durch ihn läuft — ob über `Logger.Info`/`Debug`, `Logger.With` oder `Logger.WithGroup`, verschachtelt in einer Gruppe oder nicht, unabhängig von der Stufe — gegen eine Liste verdächtiger Namensbestandteile (`password`, `secret`, `token`, `totp`, `seed`, `authorization`, `apikey`, `credential`, `cookie`, Teilstring, ohne Berücksichtigung der Groß-/Kleinschreibung) und ersetzt einen Treffer durch einen festen Platzhalter. Wer diesen Logger benutzt, kann die Maskierung nicht versehentlich umgehen — sie sitzt zwischen jedem Aufrufer und der tatsächlichen Ausgabe, nicht davor als optionale Disziplin.

4. **Fehler werden auf eine feste, kleine Ergebnisart reduziert — nie auf ihren eigenen Text.** `classifyErr` (`internal/tools/read.go`) unterscheidet `ok`, `invalid_input`, `access_denied`, `fileee_error` (mit dem numerischen HTTP-Status, nie mit `fileee.APIError`s `Message`/`Localized`) und `error`. Ein Fehlertext kann Fileees Antwortkörper wörtlich enthalten — genau das, was laut ADR-0013 als fremdbestimmter Inhalt gilt und nie geloggt werden darf.

5. **Der Fileee-Endpunkt ist eine feste, pro Werkzeug bekannte Zeichenkette, nicht der tatsächliche `*http.Request`.** `go-fileee`s `Documents`-Dienst reicht diesen Server keinen Weg, den intern gebauten Request zu beobachten; eine Erweiterung des Transports (z. B. über `fileee.WithHTTPClient`) hätte `go-fileee`s eigene Verteidigung (`defaultTransport`s `ResponseHeaderTimeout`, siehe dessen Doc-Kommentar) unbeabsichtigt außer Kraft gesetzt und lag außerhalb des Auftrags „nur protokollieren". `go-fileee`s eigenes, transportnahes Protokoll (Methode, Pfad, Status, Versuchszähler) bleibt zusätzlich über `fileee.WithLogger` erreichbar — bei `debug`, weil es intern immer auf `Debug` loggt.

6. **Gangways `accesslog.MarkToolOutcome` bleibt unverändert und unberührt.** Es protokolliert bereits automatisch, in Gangways eigenem NGINX-Zugriffsprotokoll, ob ein Werkzeugaufruf die Autorisierungsprüfung bestanden hat (`gangway/serve`, `toolMiddleware`) — vor jedem Aufruf dieses Servers eigener Werkzeug-Handler. Das ist eine andere Frage (Autorisierung) als die, die dieses ADR beantwortet (Ausführungsdiagnose: Dauer, Ergebnis, Argumente) und ein anderer Ausgabestrom; keine der beiden Mechaniken ersetzt die andere.

## Konsequenzen

**Positiv**

- Der auslösende Befund — ein leerer Werkzeugkatalog ohne erkennbaren Grund — ist ab sofort diagnostizierbar: die aufgelöste Fähigkeitsmenge und Werkzeuganzahl stehen im Protokoll, ebenso eine Ablehnung durch `MCP_OIDC_REQUIRED_SCOPES` samt Namen des fehlenden Scopes.
- Die Maskierung schützt auch künftigen Code, der diesen Logger benutzt, ohne dass jede neue Logging-Stelle die Liste sicherer Feldnamen kennen muss.
- `debug` bleibt bewusst schmal (nur Argumente) — kein Kanal, über den versehentlich mehr als beabsichtigt ausgeweitet wird, wenn jemand die Stufe künftig erweitert.

**Negativ**

- Zwei Stufen sind gröber als ein klassisches fünfstufiges Logging-Schema (trace/debug/info/warn/error). Für dieses Werkzeug reicht das: es gibt keinen Bedarf, zwischen mehreren Rauschgraden zu unterscheiden, solange nur zwei Fragen beantwortet werden („was geschah" und „womit").
- Die Ergebnisart-Reduktion (`classifyErr`) verliert Detail gegenüber der vollen Fehlermeldung — bewusst, siehe Punkt 4, aber es bedeutet, dass eine Fehlersuche jenseits der fünf Arten (`ok`/`invalid_input`/`access_denied`/`fileee_error`/`error`) auf andere Quellen angewiesen bleibt (z. B. `debug`s Argumentzeile plus manuelle Nachstellung).
- Der Fileee-Endpunkt ist statisch je Werkzeug hinterlegt, nicht am tatsächlichen Request abgelesen — bricht still, falls `go-fileee` künftig einen anderen Pfad für dieselbe Operation verwendet, ohne dass dieser Server es bemerkt.

## Referenzen

- `internal/diag` — Paket-Dokumentation, insbesondere der Doc-Kommentar zu `redactingHandler`
- `internal/tools/read.go` — Abschnitt „diagnostic logging", `classifyErr`
- README.md, Abschnitt „Diagnose"
- [ADR-0013](0013-prompt-injection-schutz.md) — Dokumentinhalte sind fremdbestimmte Daten, dieselbe Grundannahme angewendet auf Log-Ausgaben
