# ADR-0021: Fremdtext steht gerahmt in beiden Antwortkanälen

**Status:** accepted
**Datum:** 2026-10-24
**Ersetzt:** —
**Ersetzt durch:** —
**Überarbeitet:** [ADR-0013](0013-prompt-injection-schutz.md) — Punkt 1 (Ort der Rahmung)
**Überarbeitet durch:** —
**Verwandt:** Issue #82, Issue #77

## Kontext

[ADR-0013](0013-prompt-injection-schutz.md) Punkt 1 verlangt, dass Fremdtext (Dokumenttitel,
Firmen- und Kontaktnamen, Betreffzeilen, OCR-Text) nur **gerahmt** beim Modell ankommt. Die
Umsetzung legte den Rahmen in `CallToolResult.Content` und hielt `StructuredContent` bewusst
vollständig frei von Fremdtext: Das go-sdk füllt `StructuredContent` aus dem typisierten
Ausgabe-Struct, und ein Titel dort wäre ungerahmt beim Modell gelandet.

Diese Aufteilung setzte voraus, dass ein Client **beide** Kanäle auswertet. Das tut nicht jeder.
Gemessen an claude.ai (Issue #82):

| Datum | Was beim Modell ankam |
|---|---|
| 25.08.2026 | nur `content` — Kennungen und Metadaten aus `structuredContent` fehlten |
| 24.10.2026 | nur `structuredContent` — `list_documents`, `list_companies`, `list_contacts` lieferten Kennungen und Zähler, aber keinen einzigen Titel oder Namen |

Der Client hat sein Verhalten innerhalb von zwei Monaten umgekehrt. Weil `Content` nicht leer war,
legte das go-sdk außerdem keinen JSON-Text-Ersatz an (`if res.Content == nil`); wer nur `content`
las, bekam die Metadaten nicht, wer nur `structuredContent` las, den Fremdtext nicht.

## Entscheidung

Die Lösung muss unabhängig davon funktionieren, **welchen** der beiden Kanäle ein Client
auswertet. Jedes Werkzeug, das Fremdtext liefert, gibt deshalb **dasselbe in beiden Kanälen** aus:

1. **`structuredContent` bekommt ein Feld `untrustedText`** mit dem gerahmten Block (gleiche
   Boundary wie in `content`). Das Feld steht im **Ausgabeschema**, mit englischer Beschreibung,
   die den Inhalt als Text Dritter ausweist, der nur als Daten zu behandeln ist. Es wird nichts
   außerhalb des Schemas eingeschleust, weil manche Clients gegen `outputSchema` validieren.
2. **`content` bekommt zwei Blöcke:** das **ungerahmte** JSON der strukturierten Ausgabe **ohne**
   `untrustedText`, danach der gerahmte Block, byte-identisch zum Feld.
3. **Der Rahmen bleibt die Sicherheitsgrenze.** Fremdtext steht nie ungerahmt in einem Kanal:
   weder im JSON-Block (er wird mit geleertem Feld erzeugt) noch als eigenes Strukturfeld ohne
   Rahmen. Das Feld bekommt Text erst ab dem ersten Öffnungs-Tag des Rahmens.
4. **Werkzeuge ohne Fremdtext bleiben unverändert** (SDK-Ersatz greift weiter, kein Feld).

Umgesetzt an **einer** Stelle: das eingebettete `untrustedTextField` (Feldname, `omitempty`,
Schemabeschreibung genau einmal) und `attachUntrustedStructured` in
`internal/tools/read_generic.go`. Jede Aufrufstelle von `wrapUntrustedLines*` ruft sie auf; die
Schreibwerkzeuge **nach** der Mutation, deshalb ohne Fehlerrückgabe (ein Fehler dort würde eine
bereits gespeicherte Änderung als gescheitert melden und einen doppelten Retry auslösen). Jeder
Sonderfall — kein Rahmen im Text, nicht serialisierbare Ausgabe — lässt Ergebnis und Feld
unangetastet, also im Zustand vor diesem ADR, und veröffentlicht nie ungerahmten Text.

## Konsequenzen

**Positiv**

- Ein Client, der nur einen der beiden Kanäle liest, sieht Metadaten **und** gerahmten Fremdtext.
  Ein weiterer Verhaltenswechsel des Clients bricht die Werkzeuge nicht erneut.
- Die Sicherheitsgrenze aus ADR-0013 verschiebt sich nicht: Der Rahmen mit unvorhersagbarer
  Boundary ist weiterhin das Einzige, was Fremdtext vom Rest trennt.

**Negativ**

- **Der Block wird doppelt übertragen** (Feld und `content`). Bei großen Listen und OCR-Text
  verdoppelt das den Fremdtext-Anteil der Antwort. Wertet ein Client beide Kanäle aus, sieht das
  Modell ihn zweimal.
- **Die allgemeinen Ausgabetypen deklarieren das Feld auch für Werkzeuge ohne Fremdtext**
  (Schlagworte, Dokumenttypen, Schemata). Es ist optional und bleibt dort leer; eine eigene
  Typfamilie ohne das Feld wäre mehr Code für keinen Sicherheitsgewinn.
- **Die Allowlist-Tests der Ausgabe-Structs** (`…FeldlisteIstAbgeschlossen`) führen das Feld
  `untrustedTextField` jetzt als erlaubte Ausnahme. Eine Ausnahme in der Liste ist eine bewusste,
  sichtbare Entscheidung, aber auch ein Ort, an dem ein künftiges Fremdtext-Feld nicht mehr
  auffällt, wenn jemand die Liste gedankenlos erweitert. Der Test `assertDualChannel`
  (`internal/tools/untrusted_structured_test.go`) prüft deshalb zusätzlich, dass Fremdtext
  außerhalb von `untrustedText` in keinem Kanal vorkommt.

**Nicht Teil dieses ADR**

- Die Kennungen im gerahmten Block von `list_documents` (`- id=… title=…`) bleiben dort. Sie aus
  dem Block herauszunehmen ist der Nebenpunkt von Issue #82 und eine eigene Änderung.

## Referenzen

- [ADR-0013](0013-prompt-injection-schutz.md) — Dokumentinhalte sind fremdbestimmte Daten
- `docs/tools.md`, Abschnitt „Fremdbestimmter Text“ — Antwortform für Betreiber und Clients
- Issue #82, Issue #77
