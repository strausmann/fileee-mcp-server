# ADR-0020: Instanz-Beschreibung über das protokollvorgesehene `instructions`-Feld

**Status:** accepted
**Datum:** 2026-08-25
**Ersetzt:** —
**Ersetzt durch:** —
**Überarbeitet:** —
**Überarbeitet durch:** —
**Verwandt:** [ADR-0018](0018-werkzeug-freigabe-und-client-steuerung.md), [ADR-0019](0019-id-whitelist-gilt-auch-fuer-share.md)

## Kontext

Mehrere Instanzen dieses Servers können gegen verschiedene Fileee-Konten laufen und gleichzeitig
in einem Client eingebunden sein — etwa eine Testumgebung mit Wegwerfdaten neben dem produktiven
Archiv. Das Modell, das mit beiden Verbindungen arbeitet, braucht dann einen Anhaltspunkt, welche
Instanz es gerade benutzt und wie mit ihr umzugehen ist.

### Verworfen: `serverInfo.name` als Unterscheider

Naheliegend wäre, den Namen zu nutzen, mit dem sich der Server dem Client meldet
(`mcp.Implementation.Name`, Teil der `initialize`-Antwort). Das trägt nicht: Dieser Server meldet
sich immer als `fileee-mcp-server` — ein fester, im Code stehender Wert. Was der Client-Oberfläche
tatsächlich als Name eines Connectors angezeigt wird, ist ein **davon unabhängiger, vom Betreiber
in der Client-Konfiguration vergebener** Name; er wird dem Server nie mitgeteilt und der Server
kann ihn nicht setzen. Ein Versuch, diesen angezeigten Namen über eine Server-seitige
Umgebungsvariable zu steuern, ginge damit nachweislich ins Leere — es gibt auf dieser Seite der
Verbindung keinen Wert, der beim Client ankäme. Das wird hier festgehalten, damit dieser Weg nicht
in einer späteren Session erneut versucht wird.

### Verworfen: die Angabe in jede Werkzeug-Beschreibung schreiben

Ebenfalls denkbar wäre, den unterscheidenden Text in die `Description` jedes einzelnen Werkzeugs
aufzunehmen, damit er unabhängig vom Gesprächsverlauf immer sichtbar ist. Das skaliert nicht: Bei
den heute registrierten 44 Werkzeugen bedeutete das eine 44-fache Wiederholung desselben Textes im
Werkzeugkatalog, den jede Sitzung beim Verbinden vollständig überträgt — Kontext-Aufwand ohne
zusätzlichen Nutzen gegenüber einer einzigen zentralen Stelle.

## Entscheidung

`MCP_INSTANCE_DESCRIPTION` (optional, höchstens 2000 Zeichen, siehe `internal/config/config.go`)
wird auf **zwei** Wegen ausgespielt, nicht nur einem:

1. **`instructions`-Feld der `initialize`-Antwort** (`mcp.ServerOptions.Instructions`,
   `go-sdk@v1.7.0`) — das vom MCP-Protokoll dafür vorgesehene Feld, mit dem sich ein Server beim
   Verbinden gegenüber dem Client äußert. Trägt `omitempty`: ein leerer Wert erzeugt kein Feld in
   der Antwort, der Server verhält sich dann exakt wie vor Einführung dieser Variablen.
2. **`whoami`** (`instanceDescription`-Feld, `internal/tools/whoami.go`), zusätzlich zum ersten
   Weg. Der Grund für die Redundanz: `instructions` kommt genau **einmal**, beim Verbinden. In
   langen Sitzungen wird der frühe Gesprächskontext zusammengefasst — der einzige verlässliche Weg,
   die Instanz-Beschreibung danach noch zu erfragen, ist ein erneuter Werkzeugaufruf. Ohne
   `whoami` als zweiten Weg wäre die Angabe nach einer Zusammenfassung faktisch verloren.

Beide Felder tragen `omitempty`/eine äquivalente Leer-Behandlung: Ist die Variable nicht gesetzt,
fehlen beide Felder vollständig, statt als leerer String zu erscheinen.

## Abgrenzung

**Das ist ein Wegweiser, keine Schranke.** Der Text hilft dem Modell bei der Auswahl der richtigen
Instanz und verhindert nichts, wenn es trotzdem danebengreift — es gibt keine technische Prüfung,
die einen Aufruf ablehnt, weil er nicht zur beschriebenen Instanz passt.

Durchgesetzt wird die eigentliche Trennung an anderer Stelle, durch bereits bestehende Mechanik:

- **Getrennte Fileee-Zugangsdaten je Instanz** — jede Instanz spricht ohnehin nur mit ihrem
  eigenen, konfigurierten Fileee-Konto; ein Aufruf kann grundsätzlich nicht auf ein anderes Konto
  ausweichen.
- **Die client-seitige Werkzeug-Freigabe** ([ADR-0018](0018-werkzeug-freigabe-und-client-steuerung.md)) —
  Always allow / Needs approval / Blocked entscheidet der Client und dessen Nutzer je Werkzeug,
  nicht der Server.
- **Die ID-Whitelist** ([ADR-0019](0019-id-whitelist-gilt-auch-fuer-share.md)) — ein destruktiver
  oder teilender Aufruf muss sich auf eine ID beziehen, die zuvor über einen echten Lese-Schritt
  **derselben** verifizierten Identität ausgeliefert wurde.

`MCP_INSTANCE_DESCRIPTION` ergänzt diese drei Schutzschichten um eine Orientierungshilfe für das
Modell — sie ersetzt keine davon und wird von keiner vorausgesetzt.

## Konsequenzen

**Positiv**

- Ein Betreiber mit mehreren gleichzeitig eingebundenen Instanzen kann dem Modell in Prosa sagen,
  welche Instanz was ist und wie damit umzugehen ist — ohne auf den (server-seitig ohnehin nicht
  beeinflussbaren) Connector-Namen angewiesen zu sein.
- Die Angabe bleibt auch nach einer Kontext-Zusammenfassung abrufbar, weil `whoami` sie erneut
  liefert, statt sich auf das einmalige `instructions`-Feld zu verlassen.
- Ist nichts konfiguriert, ändert sich am Verhalten des Servers nichts: kein zusätzliches Feld in
  keiner der beiden Antworten.

**Negativ**

- Der Text ist reine Orientierung ohne Durchsetzungskraft — ein Betreiber, der sich auf ihn als
  Schutzmechanismus verlässt, verwechselt einen Wegweiser mit einer Schranke. Die tatsächliche
  Trennung hängt weiterhin von den drei oben genannten Mechanismen ab.
- Zwei Ausspielwege statt einem sind zwei Stellen im Code, die synchron gehalten werden müssen
  (`internal/server/server.go` und `internal/tools/whoami.go` lesen beide unabhängig
  `cfg.InstanceDescription`) — ein bewusst in Kauf genommener, geringer Mehraufwand gegenüber
  einer Angabe, die nach der ersten Zusammenfassung des Gesprächs verschwindet.

## Referenzen

- `internal/config/config.go` — `InstanceDescription`, `maxInstanceDescriptionRunes`
- `internal/server/server.go` — Weitergabe über `mcp.ServerOptions.Instructions`
- `internal/tools/whoami.go` — `ServerInfo.InstanceDescription`, `whoamiOutput.InstanceDescription`
- `docs/betrieb/aufbau.md`, Abschnitt „Mehrere Instanzen unterscheiden" — bewährte Formulierungen
- `README.md`, Abschnitt „Mehrere Instanzen unterscheiden"
- [ADR-0018](0018-werkzeug-freigabe-und-client-steuerung.md) — client-seitige Werkzeug-Freigabe
- [ADR-0019](0019-id-whitelist-gilt-auch-fuer-share.md) — ID-Whitelist
