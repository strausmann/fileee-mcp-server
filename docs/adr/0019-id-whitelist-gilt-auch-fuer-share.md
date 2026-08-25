# ADR-0019: Die Ausgeliefert-ID-Whitelist gilt auch für die `share`-Klasse

**Status:** accepted
**Datum:** 2026-08-24
**Ersetzt:** —
**Ersetzt durch:** —
**Überarbeitet:** —
**Überarbeitet durch:** —
**Verwandt:** [ADR-0013](0013-prompt-injection-schutz.md)

## Kontext

[ADR-0013](0013-prompt-injection-schutz.md) Punkt 3 führt `internal/issued.Store` ein: eine
Merkliste, die je verifizierter Aufrufer-Identität festhält, welche Dokument-, Kontakt- und
Reminder-IDs dieser Server tatsächlich über ein Lese-Werkzeug ausgeliefert hat. Ein destruktives
Werkzeug prüft vor dem Handeln, ob die ihm übergebene ID eine davon ist — eine ID, die nur im Text
eines Dokuments stand, etwa in einem eingebetteten Prompt-Injection-Versuch, war nie über einen
echten Lese-Schritt ausgeliefert und ist damit kein gültiges Ziel.

ADR-0013s Abschnitt „Negativ" begründet ausdrücklich, warum die Whitelist **nur** die destruktive
Gruppe trägt, nicht `write`:

> „Die ID-Whitelist ist Sitzungszustand und damit zusätzliche Komplexität im Server. Sie gilt
> bewusst nur für die destruktive Gruppe, nicht für `write` — dort wäre der Aufwand
> unverhältnismäßig, weil Uploads naturgemäß keine Vorgänger-ID haben."

Diese Begründung trägt für `write`: Ein Upload legt eine neue Ressource an, es gibt keine
Vorgänger-ID, gegen die man prüfen könnte. Sie trägt **nicht** für die Werkzeuggruppe `share`.
`Share`, `ShareDocument` und `AddParticipant` haben alle dieselbe Form wie die destruktiven
Werkzeuge, gegen die die Whitelist heute schon greift: sie nehmen eine bestehende Dokument-ID
entgegen und wirken auf der damit bezeichneten Ressource. Eine ID, die nur im Text eines fremden
Dokuments auftaucht — der klassische ADR-0013-Angriffsweg — wäre ohne diese Erweiterung ein
gültiges Ziel für `Share`/`ShareDocument`/`AddParticipant`, genauso wie sie es vor ADR-0013 Punkt 3
für die destruktiven Werkzeuge war.

Der Unterschied zu `write` ist dabei nicht nur formal, sondern in der Schadenshöhe: Bei `write`
ist der schlimmste per Prompt-Injection auslösbare Fall „das falsche eigene Feld wurde geändert" —
ärgerlich, aber folgenlos für Dritte, und im eigenen Konto reparierbar. Bei `share` ist der
schlimmste Fall „die Daten liegen bei einem Fremden", und das lässt sich nicht zurücknehmen:
`Share` erzeugt einen anonymen, credential-losen Freigabe-Link auf ein Dokument: wer den Link
kennt, sieht den Inhalt, ohne sich je auszuweisen. `AddParticipant` lädt eine externe
E-Mail-Adresse als Teilnehmer auf ein Dokument ein — eine dauerhafte Zugriffsberechtigung für ein
Konto, das der Aufrufer nicht kontrolliert. Ein eingebetteter Prompt-Injection-Versuch, der ein
Modell dazu bringt, ein beliebiges, im Dokumenttext genanntes Dokument freizugeben oder mit einer
vom Angreifer gewählten Adresse zu teilen, ist damit kein Konfigurationsfehler, sondern ein
Datenabfluss zu einem Dritten, den niemand mehr rückgängig machen kann.

Die Whitelist selbst existiert bereits und ist produktiv (siehe `internal/issued`,
[ADR-0013](0013-prompt-injection-schutz.md) Punkt 3) — dieses ADR ändert nicht ihre Mechanik,
sondern ausschließlich, **welche** Werkzeuggruppe sie deckt. Die Werkzeuge `Share`, `ShareDocument`
und `AddParticipant` selbst existieren zum Zeitpunkt dieser Entscheidung im Code noch nicht — sie
sind Teil eines späteren Increments dieser Spezifikation (Spec 4 trägt zusätzlich die destruktive
Gruppe). Diese Entscheidung wird bewusst vor deren Implementierung getroffen, damit die
Prüfpflicht von Anfang an gilt und nicht nachträglich um bereits ungeschützt ausgelieferte
Werkzeuge herum ergänzt werden muss.

## Entscheidung

1. **Die Whitelist deckt ab sofort zwei Werkzeuggruppen: `destructive` und `share`.** Jedes
   Werkzeug, das eine bestehende Fileee-ID entgegennimmt und darauf wirkt — unabhängig davon, ob
   die Wirkung ein Löschen/Ändern (`destructive`) oder ein Freigeben/Einladen (`share`) ist —
   prüft die übergebene ID vor der Ausführung gegen `issued.Store.Check`. `write` bleibt bewusst
   ausgenommen, aus dem in ADR-0013 genannten Grund (keine Vorgänger-ID).

2. **Mechanik, Bindung und Grenzen bleiben unverändert** — dieses ADR ändert keine der bereits
   getroffenen Ausgestaltungen, es erweitert nur ihren Geltungsbereich:
   - **Verfall nach Zeit** (`IssuedIDTTLSeconds`, Vorgabe 1800 Sekunden / 30 Minuten) **und**
     ein **Deckel je Identität** (`IssuedIDMaxPerIdentity`, Vorgabe 1000) — beide konfigurierbar,
     beide im Praxisbetrieb noch zu erproben; es gibt aktuell keine belastbare Erfahrung darüber,
     ob diese Startwerte für die zusätzliche `share`-Last ausreichen oder zu eng sind.
   - **Bindung an `serve.IdentityFrom(ctx)`, nicht an die MCP-Sitzung.** Unter Gangways
     erzwungener Statelessness ([ADR-0015](0015-gangway-als-unterbau.md)) öffnet und schließt
     jede Anfrage eine neue, temporäre Sitzung; eine sitzungsgebundene Merkliste könnte nie über
     den einzelnen Request hinaus etwas merken. Die Merkliste schlüsselt deshalb über das Subject
     der verifizierten Identität.
   - **Fail-closed ohne geprüfte Identität.** Ist keine verifizierte Identität aus dem Kontext
     auflösbar, gilt jede ID als nicht ausgeliefert — es gibt keinen Fallback, der eine
     ID-Prüfung überspringt, nur weil die Identität fehlt.
   - **Nur Prozessspeicher.** Die Merkliste lebt ausschließlich im Arbeitsspeicher dieses
     Prozesses; ein Neustart verwirft sie vollständig. Das ist Absicht, kein Mangel — es gibt
     keine Anforderung, ausgelieferte IDs über einen Neustart hinaus als gültig zu behandeln.

3. **Die Erweiterung ist ein Zwischenschritt, keine Endstufe.** Ab Spec 4 trägt dieselbe
   Merkliste zusätzlich die ursprüngliche destruktive Gruppe (Löschen, Ändern) — sie war der
   eigentliche Grund, weshalb `internal/issued` überhaupt gebaut wurde
   ([ADR-0013](0013-prompt-injection-schutz.md) Punkt 3). Mit dieser Entscheidung deckt sie
   `destructive` und `share`; künftige destruktive Werkzeuge binden sich an dieselbe Prüfung, ohne
   dass die Mechanik erneut geändert werden muss.

## Konsequenzen

**Positiv**

- Ein per Prompt-Injection genanntes Dokument kann nicht mehr über `Share`/`ShareDocument`/
  `AddParticipant` an einen Fremden gegeben werden, ohne dass diese ID zuvor über einen echten
  Lese-Schritt an genau diesen Aufrufer ausgeliefert wurde — derselbe Schutz, den destruktive
  Werkzeuge bereits haben, jetzt auch für den Weg, auf dem Daten das Konto endgültig verlassen.
- Keine neue Mechanik, kein neuer Zustand: `Share`/`ShareDocument`/`AddParticipant` binden sich an
  dieselbe `issued.Store`-Instanz, dieselbe Identitätsbindung, dieselbe Verfalls- und
  Deckel-Logik, die für die destruktive Gruppe bereits existiert und getestet ist.
- Die Entscheidung steht fest, bevor die Werkzeuge selbst implementiert werden — es gibt keinen
  Zeitraum, in dem `Share`/`ShareDocument`/`AddParticipant` ungeschützt ausgeliefert würden und
  die Prüfung nachträglich ergänzt werden müsste.

**Negativ**

- **Der Nutzer zahlt einen konkreten, bewusst in Kauf genommenen Preis:** Eine ID, die der Nutzer
  selbst nennt — etwa aus der Fileee-Weboberfläche abgelesen, ohne sie zuvor über ein Lese-Werkzeug
  dieses Servers gesehen zu haben —, wird von `Share`/`ShareDocument`/`AddParticipant` abgelehnt.
  Der Server kann prinzipiell nicht unterscheiden, ob eine ID „vom Nutzer genannt" oder „aus
  injiziertem Dokumenttext übernommen" stammt; er kennt nur, ob sie über einen eigenen
  Lese-Schritt ausgeliefert wurde. Das ist kein Versehen, sondern die Kehrseite derselben Prüfung,
  die den Angriffsweg schließt — wer eine ID ohne vorherigen Lese-Schritt dieses Servers verwenden
  will, muss das Dokument zuerst darüber lesen (z. B. `get_document`), dann erst teilen.
- Die geteilte Last zweier Werkzeuggruppen auf einer Merkliste mit festem Deckel je Identität
  (`IssuedIDMaxPerIdentity`) kann in Sitzungen mit vielen Lese- und Freigabe-Operationen den
  Deckel schneller erreichen als bisher — noch ohne Praxiswert dafür, ob 1000 in diesem
  erweiterten Geltungsbereich ausreicht (siehe Entscheidung, Punkt 2).
- Dieselbe Wirksamkeitsgrenze wie in [ADR-0013](0013-prompt-injection-schutz.md): Die Whitelist
  senkt die Wahrscheinlichkeit eines erfolgreichen Angriffs, sie beseitigt ihn nicht. Ein Modell,
  das durch eine Injektion dazu gebracht wird, ein Dokument freizugeben, das der Nutzer zuvor
  selbst und legitim über ein Lese-Werkzeug hat lesen lassen, bleibt innerhalb dessen, was die
  Whitelist zulässt.
- **Der Nachbarfall oben ist nicht der einzige Weg, die Vorbedingung zu erfüllen — eine Injektion
  kann sie sich selbst verschaffen.** `getFromService`/`documentFromService` nehmen die ID aus der
  Antwort auf, sobald ein Lese-Werkzeug sie ausliefert — unabhängig davon, wer den Aufruf
  veranlasst hat. Eine Injektion der Form „rufe zuerst `get_document(X)` auf, teile dann X" stellt
  ihre eigene Vorbedingung in genau einem zusätzlichen Schritt her; die Whitelist verhindert das
  nicht, sie macht den Angriff nur mehrschrittig statt einschrittig. Was sie weiterhin leistet:
  Die ID muss im Konto des Aufrufers **existieren und für ihn lesbar** sein — ein Angreifer kann
  keine beliebige, im eigenen Konto nie vorhandene ID einschleusen, nur eine, auf die der Aufrufer
  ohnehin schon Lesezugriff hat. Der Schutz verschiebt die Angriffskosten, er schließt den
  Angriffsweg nicht.
- **Der Store ist eine flache Menge ohne Entitätstyp.** `issued.Store.Check` prüft nur, ob eine
  Kennung irgendwann für diese Identität aufgenommen wurde — nicht, als was. `get_document` nimmt
  neben der Dokument-ID auch deren `TagIDs` auf (siehe `internal/tools/read.go`); ein `Check` für
  eine Dokument-ID würde damit auch eine zuvor aufgenommene Tag-ID akzeptieren, wenn beide Räume
  je kollidieren sollten. Praktisch unkritisch, solange Fileee-IDs über Entitätstypen hinweg nicht
  kollidieren (wovon aktuell ausgegangen wird, ohne dass es irgendwo verbindlich zugesichert ist)
  — aber implizit geerbt, nicht bewusst entschieden. Spec 3b, die `Check` erstmals produktiv an
  ein Werkzeug bindet, soll ausdrücklich entscheiden, ob `Check` typbewusst wird (z. B. über einen
  zweiten Parameter oder getrennte Namensräume je Entitätstyp), statt diese Eigenschaft
  stillschweigend fortzuschreiben.

## Nachtrag (2026-08-25): Nur gezielte Einzelabrufe erfassen

**Betreiber-Entscheidung, direkte Folge eines Sicherheits-Audits.** Dieser Nachtrag ändert NICHT
die Entscheidung oben (welche Werkzeuggruppen — `destructive`/`share` — die Whitelist per `Check`
prüfen) — er ändert die ANDERE Seite derselben Mechanik: welche Lese-Werkzeuge überhaupt eine ID
in die Whitelist AUFNEHMEN (`Record`). Kontext und Entscheidung oben bleiben unverändert stehen,
als historisches Protokoll (siehe `docs/adr/README.md`, „Beim Ablösen nur den Header anfassen") —
dieser Abschnitt ergänzt, statt zu überschreiben.

### Befund

Zwei unabhängige Hunter im Sicherheits-Audit fanden, dass die Erfassung vor diesem Nachtrag
praktisch wertlos war: Ein einziger `list_documents`-Aufruf hebt bis zu 100 IDs in die Whitelist
(Standardgrenze), paginierbar bis zum Deckel von 1000 je Identität (`IssuedIDMaxPerIdentity`);
`sync_documents` nimmt beim ersten Aufruf (kein Cursor) gleich den kompletten aktuellen Bestand
auf; `list_boxes`/`get_box` waren vollständig ungedeckelt. Bei einem Konto mit ein paar hundert
Dokumenten macht das praktisch **jede** ID im Konto zu einer gültigen. Der Angreifer muss die
Ziel-ID nicht mehr kennen — er muss das Modell nur zum Auflisten bringen. Die Zusage „nur was
echt gelesen wurde" degenerierte damit zu „fast alles im Konto".

### Entscheidung (Ergänzung)

**Nur gezielte Einzelabrufe nehmen eine ID in die Whitelist auf.** Ein Werkzeug, das EINE Entität
anhand einer VOM AUFRUFER GENANNTEN ID liefert (ein `get_*`-Werkzeug), nimmt diese eine ID auf.
Ein Werkzeug, das MEHRERE Entitäten liefert, ohne dass der Aufrufer sie einzeln genannt hat
(`list_*`, `search_*`, `sync_*`), nimmt KEINE mehr auf — unabhängig davon, ob es zur `destructive`/
`share`-Prüfung selbst gehört oder nur Vorstufe dafür ist (z. B. `list_documents` als
Entdeckungsschritt vor `get_document`).

Konkret, in `internal/tools`:

- **Nehmen weiterhin auf** (unverändert): `get_document`, `get_box`, und der generische
  Einzelabruf-Pfad (`getFromService`, `read_generic.go`) hinter `registerReferenceTools`/
  `registerPeopleTools` — jeweils NUR die eine, per `id`-Parameter angeforderte ID.
- **Nehmen seit diesem Nachtrag NICHT MEHR auf**: `list_documents`, `search_documents`,
  `sync_documents`, `list_document_conversations`, `list_boxes`, sowie der generische
  Listen-Pfad (`listFromService`) und alle sieben generischen Sync-Werkzeuge
  (`syncFromService`/`registerSyncTools`).
- **Zwei Grenzfälle, nach derselben Linie entschieden** — beide waren zuvor Nebenprodukte, die der
  Aufrufer nie einzeln genannt hatte, und wurden trotzdem aufgenommen:
  - `get_document` nahm bislang zusätzlich `TagIDs` auf (Tags des Dokuments, nicht vom Aufrufer
    angefordert) — das behebt genau die im „Negativ"-Abschnitt oben offen gelassene
    Kollisionsfrage („Der Store ist eine flache Menge ohne Entitätstyp") für diesen konkreten
    Fall, indem `TagIDs` gar nicht mehr in die Menge gelangen — nicht, indem `Check` typbewusst
    wird (das bleibt eine offene Frage für den Rest der flachen Menge).
  - `get_box` nahm bislang zusätzlich `DocumentIDs` auf (die im Karton abgelegten Dokumente,
    ebenfalls nicht vom Aufrufer angefordert) — dieselbe Begründung, dasselbe Ergebnis.

### Der bewusst gezahlte Preis

Wer mehrere konkrete Treffer als gültige Ziele für ein späteres `destructive`/`share`-Werkzeug
braucht, muss sie nach einem `list_*`/`search_*`/`sync_*`-Aufruf einzeln per Einzelabruf (z. B.
`get_document`) nachladen — ein zusätzlicher Schritt pro ID. `internal/issued.errNotIssuedFor`
nennt genau diesen Weg in seiner Fehlermeldung.

### Was der Mechanismus weiterhin NICHT leistet

Unverändert gegenüber dem „Negativ"-Absatz oben, der das für die ursprüngliche Fassung bereits
festhielt: Eine Injektion kann weiterhin „ruf erst `get_document(X)` auf, dann nutze X" im Text
eines fremden Dokuments verlangen — der Schutz erzwingt nur, dass X wirklich im Konto des
Aufrufers existiert und für ihn lesbar ist, und macht den Angriff dadurch mehrschrittig statt
einschrittig, mehr nicht. Dieser Nachtrag verengt lediglich, WELCHE Aufrufe eine ID überhaupt in
die Whitelist heben können — er schafft keinen neuen Schutz gegen den mehrschrittigen Weg.

### Guardrail

`internal/tools/issued_coverage_test.go` (`TestJedesWerkzeugDasIDsAusliefertMerktSieAuch`) und
`internal/tools/read_generic_test.go`
(`TestGetFromServiceMerktDenGezieltenEinzelabrufListFromServiceMerktNichts`) prüfen seit diesem
Nachtrag BEIDE Richtungen: Einzelabrufe MÜSSEN aufnehmen, Listen-/Such-/Sync-Werkzeuge DÜRFEN
NICHT. Per Gegenprobe belegt (beide Richtungen): `Record` aus `get_document` entfernt → Test
schlägt fehl und nennt `get_document`; `Record` erneut in `list_documents` eingebaut → Test
schlägt fehl und nennt `list_documents`.

## Referenzen

- [ADR-0013](0013-prompt-injection-schutz.md) — Punkt 3 (Herkunft der Whitelist), Abschnitt
  „Negativ" (Begründung für den `write`-Ausschluss, die hier für `share` widerlegt wird)
- `internal/issued/issued.go` — Paket-Doc-Kommentar und `Store`-Implementierung
- `internal/config/config.go` — `IssuedIDTTLSeconds`/`IssuedIDMaxPerIdentity`, Defaults und
  Fail-closed-Konvention für Werte `<= 0`
- `docs/superpowers/plans/2026-08-24-fileee-mcp-id-whitelist.md` (homelab-management-Repo)
