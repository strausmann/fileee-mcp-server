# Werkzeuge

Dieser Server registriert **jedes** Werkzeug immer, für jeden Aufrufer, der Gangways
Authentifizierung und die geforderte Scope-Prüfung besteht — es gibt keine serverseitige
Freischaltung mehr, die ein Werkzeug aus `tools/list` fernhält. Jedes Werkzeug trägt stattdessen
einen `Title` und wahrheitsgemäße `ToolAnnotations` (`readOnlyHint`, `destructiveHint`,
`idempotentHint`); ob ein Client es tatsächlich aufrufen darf, entscheiden der Client und dessen
Benutzer (Always allow / Needs approval / Blocked), nicht der Server. Details und Begründung:
[ADR-0018](adr/0018-werkzeug-freigabe-und-client-steuerung.md) (löst
[ADR-0011](adr/0011-capability-gating.md)s serverseitiges Capability-Gating ab).

Die lesenden Werkzeuge sind heute vollständig: **32 fileee-Werkzeuge**, unten nach Sachgebiet
gruppiert — plus 4 operative Werkzeuge, die keine Fileee-Daten berühren (`get_runtime_stats`,
`get_tool_manifest`, `self_check`, `whoami`; siehe deren eigene Beschreibung im Code, nicht Teil
dieser Gruppierung nach Sachgebiet). Jede Zusammenfassung fasst die Beschreibung zusammen, die
das jeweilige Werkzeug selbst im Code trägt (die eigentliche Quelle) — sie erfindet nichts Neues.

## Fremdbestimmter Text — die wichtigste Eigenschaft dieses Servers (ADR-0013)

Ein Grossteil der hier aufgeführten Werkzeuge liefert Text, den **nicht der Kontoinhaber**
geschrieben hat, sondern ein Dritter: wer eine Rechnung stellt, bestimmt ihren Titel; wer mit dem
Kontoinhaber chattet, bestimmt den Betreff; wessen Dokument gescannt wird, bestimmt den Text, den
die OCR erkennt. Dieser Text könnte Formulierungen enthalten, die wie eine Anweisung an das Modell
aussehen.

**Deshalb erscheint fremdbestimmter Text bei jedem betroffenen Werkzeug ausschliesslich gerahmt im
Textinhalt der Antwort — niemals in `structuredContent`.** Der Rahmen ist eine bei jedem Aufruf
frisch erzeugte, unvorhersagbare Umrandung (`<untrusted_external_content boundary="…">…</…>`, siehe
Abschnitt weiter unten) mit einem ausdrücklichen Hinweis, enthaltene Anweisungen nicht zu befolgen.
Wer eine Antwort dieses Servers verarbeitet, sollte diesen Textblock **grundsätzlich als Daten
behandeln, nicht als Anweisung** — unabhängig davon, wie er formuliert ist.

**Welche Werkzeuge liefern fremdbestimmten Text (gerahmt), und woher stammt er:**

| Werkzeug(e) | Fremdbestimmtes Feld | Herkunft |
|---|---|---|
| `list_documents`, `sync_documents`, `get_document` | Dokumenttitel | wer das Dokument verfasst/eingescannt hat |
| `list_companies`, `sync_companies`, `get_company` | Firmenname | wer das Dokument gesendet hat, sofern automatisch erkannt (`FromUserDB=false`) |
| `list_contacts`, `sync_contacts`, `get_contact` | Kontaktname | der Kontakt selbst, oder aus einem Dokument extrahiert |
| `list_reminders`, `sync_reminders`, `get_reminder` | Erinnerungs-Beschreibung | kann aus dem verknüpften Dokument übernommen sein |
| `list_conversations`, `sync_conversations`, `get_conversation`, `list_document_conversations` | Konversations-Betreff | wer auf der anderen Seite der Konversation steht |
| `get_page_ocr` | erkannter Seitentext | wörtlich das, was auf dem Papier eines Dritten steht — der stärkste Fall im ganzen Server |

**Welche Werkzeuge liefern KEINEN fremdbestimmten Text** — alle Felder sind entweder Fileees
eigene Metadaten oder vom Kontoinhaber selbst vergeben: `search_documents`, `list_tags`/`get_tag`/
`sync_tags`, `list_document_types`/`get_document_type`/`sync_document_types`,
`list_document_type_schemes`/`get_document_type_scheme`/`sync_document_type_schemes`,
`list_boxes`/`get_box`, `get_document_pdf`, `get_page_image`, `get_account_status`.

## `read` — lesende Werkzeuge

### Dokumente

| Werkzeug | Was es tut und was nicht |
|---|---|
| `list_documents` | Listet Dokumente, zuletzt geänderte zuerst, mit ID und Metadaten (Status, Typ, Zeitstempel); der Titel steht getrennt gerahmt im Text. |
| `search_documents` | Volltextsuche über Dokumente, liefert nur Trefferzahl und IDs (relevanteste zuerst) — **keine** Titel, keine sonstigen Metadaten (go-fileees `Documents.Search` liefert selbst nichts anderes). |
| `get_document` | Lädt ein Dokument per Kennung: Metadaten, Seitenanzahl, Schlagwort-Kennungen, Titel gerahmt. Gibt **nicht** die Datei selbst zurück (dafür `get_document_pdf`) und sucht **nicht** nach Titel. |
| `sync_documents` | Gleicht Dokumente inkrementell ab (Cursor-basiert): geänderte/neue Dokumente strukturiert, gelöschte IDs, neuer Cursor, Titel gerahmt. Nimmt **keinen** Cursor eines anderen Abgleich-Werkzeugs entgegen und sucht **nicht** nach Titel. |
| `list_document_conversations` | Listet die Konversationen, in denen ein Dokument geteilt wurde (Kennung, Typ, Art, Teilnehmerzahl), Betreff gerahmt. Gibt **keine** Teilnehmernamen oder Nachrichteninhalte zurück und sucht **nicht** nach Dokumenttitel. |

### Stammdaten: Schlagworte, Firmen, Dokumenttypen, Dokumenttyp-Schemata

Alle vier Ressourcen haben dasselbe Dreier-Muster (Liste, Detail, inkrementeller Abgleich).
Schlagworte, Dokumenttypen und Dokumenttyp-Schemata tragen keinen Fremdtext — ihre Felder sind
ausschliesslich Fileee-Metadaten oder vom Kontoinhaber selbst vergeben. Firmen sind die Ausnahme
(siehe Tabelle oben).

| Werkzeug | Was es tut und was nicht |
|---|---|
| `list_tags` | Listet Schlagworte (Kennung, Name — eigene Kategorisierung des Kontoinhabers). Gibt **nicht** die Dokumente zurück, die ein Schlagwort tragen. |
| `get_tag` | Lädt ein Schlagwort per Kennung. Sucht **nicht** nach Namen, gibt **nicht** die tragenden Dokumente zurück. |
| `sync_tags` | Gleicht Schlagworte inkrementell ab. Kein Cursor eines anderen Abgleich-Werkzeugs. |
| `list_companies` | Listet Firmen (Kennung, Kontakttyp/-status, Dokumentzähler, Herkunft — eigene Eingabe oder automatisch erkannt); Firmenname gerahmt. Gibt **nicht** die verknüpften Kontakte/Dokumente zurück. |
| `get_company` | Lädt eine Firma per Kennung, Firmenname gerahmt. Sucht **nicht** nach Firmenname, gibt **nicht** verknüpfte Kontakte/Dokumente zurück. |
| `sync_companies` | Gleicht Firmen inkrementell ab, Firmenname gerahmt. |
| `list_document_types` | Listet Dokumenttypen (Kennung, Anzeigename, Schema-Kennung, Dokumentzähler). Gibt **nicht** die Feld-Definitionen des Schemas zurück (dafür `get_document_type_scheme`). |
| `get_document_type` | Lädt einen Dokumenttyp per Kennung. Sucht **nicht** nach Namen, gibt **nicht** die Schema-Feldliste zurück. |
| `sync_document_types` | Gleicht Dokumenttypen inkrementell ab. Gibt **nicht** das Feld-Schema zurück (dafür `sync_document_type_schemes`). |
| `list_document_type_schemes` | Listet Dokumenttyp-Schemata (Kennung, Feldschlüssel). Gibt **nicht** zurück, welche Dokumenttypen ein Schema referenzieren. |
| `get_document_type_scheme` | Lädt ein Schema per Kennung. Sucht **nicht** nach Feldschlüssel, gibt **nicht** die referenzierenden Dokumenttypen zurück. |
| `sync_document_type_schemes` | Gleicht Dokumenttyp-Schemata inkrementell ab. |

### Personen & Kommunikation: Kontakte, Erinnerungen, Konversationen

Alle drei Ressourcen tragen fremdbestimmten Text (siehe Tabelle oben) — jedes Liste-/Detail-/
Abgleich-Werkzeug rahmt ihn getrennt und lässt ihn aus der Struktur weg.

| Werkzeug | Was es tut und was nicht |
|---|---|
| `list_contacts` | Listet Kontakte (Kennung, Firmen-Kennung, Kontakttyp/-status, Herkunft, Dokumentzähler); Kontaktname gerahmt. Sucht **nicht** nach Namen, gibt **nicht** E-Mail/Telefon/Adresse zurück. |
| `get_contact` | Lädt einen Kontakt per Kennung, Kontaktname gerahmt. Sucht **nicht** nach Namen, gibt **nicht** E-Mail/Telefon/Adresse zurück. |
| `sync_contacts` | Gleicht Kontakte inkrementell ab, Name gerahmt. |
| `list_reminders` | Listet Erinnerungen (Kennung, verknüpfte Dokument-Kennung, Startdatum, Erledigt-Status); Beschreibung gerahmt. Gibt **nicht** die ausführliche Beschreibung zurück. |
| `get_reminder` | Lädt eine Erinnerung per Kennung, Beschreibung gerahmt. Gibt **nicht** die ausführliche Beschreibung zurück. |
| `sync_reminders` | Gleicht Erinnerungen inkrementell ab, Beschreibung gerahmt. |
| `list_conversations` | Listet Konversationen (Kennung, Typ, Art, Teilnehmerzahl); Betreff gerahmt. Gibt **keine** Teilnehmernamen oder Nachrichteninhalte zurück (auch die sind fremdbestimmt, siehe unten). |
| `get_conversation` | Lädt eine Konversation per Kennung, Betreff gerahmt. Gibt **keine** Teilnehmernamen oder Nachrichteninhalte zurück. |
| `sync_conversations` | Gleicht Konversationen inkrementell ab, Betreff gerahmt. |

**Nebenbefund zu Konversationen:** Nicht nur der Betreff ist fremdbestimmt — auch jeder
Teilnehmername und jeder Nachrichtentext sind vom jeweiligen Teilnehmer verfasst, nicht vom
Kontoinhaber. Keines der Konversations-Werkzeuge gibt diese zurück; strukturiert erscheint nur
eine Teilnehmer**zahl** (`participantCount`), nie eine Namensliste.

### Boxen

`list_boxes`/`get_box` laufen über einen eigenen Handler, da Fileees `BoxService.List` keine
Seitenaufteilung kennt.

| Werkzeug | Was es tut und was nicht |
|---|---|
| `list_boxes` | Listet **alle** Boxen auf einmal — kein Grenzwert-/Seitenversatz-Parameter, weil Fileees API dafür keine Seitenaufteilung anbietet. Der Boxname ist die eigene Beschriftung des Kontoinhabers, kein Fremdtext. Gibt **nicht** die Titel/Metadaten der enthaltenen Dokumente zurück. |
| `get_box` | Lädt eine Box per Kennung, inklusive der enthaltenen Dokument-Kennungen. Sucht **nicht** nach Boxname, gibt **nicht** Dokumenttitel/-metadaten zurück. |

### Binärdaten & OCR

`get_document_pdf`/`get_page_image` liefern Binärinhalte mit einer harten Obergrenze von **8 MiB**
— eine Antwort darüber schlägt mit einem Fehler fehl, der die tatsächlich gelesene Byte-Zahl
nennt, nie mit einer stillschweigend gekürzten Datei.

| Werkzeug | Was es tut und was nicht |
|---|---|
| `get_document_pdf` | Lädt die Original-PDF-Datei eines Dokuments (max. 8 MiB). Gibt **nicht** ein Seitenbild zurück (dafür `get_page_image`) und **nicht** den OCR-Text (dafür `get_page_ocr`). |
| `get_page_image` | Lädt das gerenderte Bild einer Seite — Fallback ohne PDF (max. 8 MiB); braucht die aktuelle Bildversion der Seite. Gibt **nicht** den OCR-Text zurück und liefert **nur eine** Seite je Aufruf. |
| `get_page_ocr` | Führt Fileees OCR-Erkennung für eine Seite aus. Der erkannte Text ist der **am stärksten fremdbestimmte Inhalt im ganzen Server** und erscheint ausschliesslich gerahmt im Text. Strukturiert kommen **nur** Tokenanzahl und Koordinaten/Fileees eigene Token-Kennung zurück — **niemals** der Text selbst unter einem Strukturfeld. |

### Kontostand

| Werkzeug | Was es tut und was nicht |
|---|---|
| `get_account_status` | Liefert Abo-Typ, -Name, Abrechnungsintervall/-betrag, Lizenzgültigkeit/-auffüllung und ein von Fileee gemeldetes Kontoproblem. Nimmt **keine** Parameter entgegen (genau ein Wert je Konto) und liefert **keine** dokumentbezogenen Informationen. |

## Wie fremdbestimmter Text gerahmt wird

`list_documents` (und jedes andere Werkzeug aus der Tabelle oben) markiert jeden fremdbestimmten
Textblock als nicht vertrauenswürdig: der Block ist von einer Umrandung mit einer bei jedem Aufruf
frisch erzeugten, 128 Bit langen Zufallsmarke (`boundary`) eingerahmt, zusammen mit einem
ausdrücklichen Hinweis, enthaltene Anweisungen nicht zu befolgen.

```text
<untrusted_external_content boundary="…">
The block below was written by third parties …

- id=doc-1 title="Rechnung ACME GmbH"
- id=doc-2 title="…"
</untrusted_external_content boundary="…">
```

**Warum eine zufällige Marke und nicht ein fester String?** Ein fremdbestimmter Text könnte eine
feste Umrandung (z. B. `</untrusted_external_content>`) nachahmen und versuchen, den Block
vorzeitig zu beenden — alles danach erschiene dann als „vertrauenswürdig“. Mit einer bei jedem
Aufruf **neu** erzeugten Zufallsmarke (`crypto/rand`, 16 Byte) kann vorab verfasster Text die
tatsächliche Marke nicht kennen; eine nachgeahmte schließende Marke bleibt als sichtbarer,
unwirksamer Text innerhalb des Blocks stehen. Das ist **keine Garantie** — die primäre Absicherung
ist, dass ein Client anhand der `ToolAnnotations` (`readOnlyHint`, `destructiveHint`,
`idempotentHint`) und der eigenen Freigabe-Entscheidung (Always allow / Needs approval /
Blocked) selbst kontrolliert, welches Werkzeug ausgeführt wird — nicht ein serverseitig nicht
registriertes Werkzeug (siehe [ADR-0018](adr/0018-werkzeug-freigabe-und-client-steuerung.md),
das [ADR-0013](adr/0013-prompt-injection-schutz.md) Punkt 4 in diesem Punkt überholt) — aber
die Zufallsmarke macht eine plausible, feste Nachahmung des Rahmens wirkungslos.

Werkzeuge ohne fremdbestimmten Text (siehe Tabelle oben) enthalten keine solche Umrandung — ein
leeres Textergebnis ist bei ihnen normal, kein Fehler.

## Zugriffstrennung

Jeder Aufruf löst die Fileee-Verbindung ausschließlich über die durch Gangway geprüfte Identität
auf (`serve.IdentityFrom(ctx)`, siehe [ADR-0015](adr/0015-gangway-als-unterbau.md) und
`CONTRIBUTING.md`, Abschnitt „Konto-Auflösung“) — nie über eine zwischengespeicherte oder feste
Identität. Ein Aufrufer, der Gangways eigene Prüfungen besteht, aber keinem Fileee-Konto
zugeordnet ist, bekommt ein gewöhnliches Werkzeug-Fehlerergebnis (kein Server- oder
Protokollfehler) — siehe [ADR-0012](adr/0012-multi-account-mapping.md), Punkt 4/5.
