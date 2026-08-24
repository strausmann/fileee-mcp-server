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

## Referenzen

- [ADR-0013](0013-prompt-injection-schutz.md) — Punkt 3 (Herkunft der Whitelist), Abschnitt
  „Negativ" (Begründung für den `write`-Ausschluss, die hier für `share` widerlegt wird)
- `internal/issued/issued.go` — Paket-Doc-Kommentar und `Store`-Implementierung
- `internal/config/config.go` — `IssuedIDTTLSeconds`/`IssuedIDMaxPerIdentity`, Defaults und
  Fail-closed-Konvention für Werte `<= 0`
- `docs/superpowers/plans/2026-08-24-fileee-mcp-id-whitelist.md` (homelab-management-Repo)
