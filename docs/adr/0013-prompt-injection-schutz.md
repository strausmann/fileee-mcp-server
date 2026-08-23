# ADR-0013: Dokumentinhalte sind fremdbestimmte Daten

**Status:** accepted
**Datum:** 2026-08-06
**Ersetzt:** —
**Ersetzt durch:** —
**Überarbeitet:** —
**Überarbeitet durch:** [ADR-0015](0015-gangway-als-unterbau.md), [ADR-0018](0018-werkzeug-freigabe-und-client-steuerung.md) — Punkt 4
**Verwandt:** [ADR-0011](0011-capability-gating.md), [ADR-0017](0017-diagnose-protokoll-mit-erzwungener-maskierung.md), [go-fileee ADR-0007](https://github.com/strausmann/go-fileee/blob/main/docs/adr/0007-ausschluss-destruktiver-operationen.md)

## Kontext

Der Server liefert Dokumentinhalte und OCR-Text an ein Sprachmodell. Diese Inhalte stammen von Dritten: Rechnungen, Behördenpost, Verträge, eingescannte Werbung. Wer eine Rechnung stellt, bestimmt ihren Inhalt — und damit auch, was das Modell zu lesen bekommt.

Gleichzeitig können, je nach Konfiguration, Tools registriert sein, die Dokumente ändern, hochladen, teilen oder löschen. Das Modell entscheidet selbst, welches Tool es aufruft.

Damit besteht ein Angriffsweg, der bei einer REST-API nicht existiert und bei den Schwester-Repos deshalb kein Thema war: ein präpariertes Dokument enthält Text, der wie eine Anweisung an das Modell aussieht. Das Modell liest ihn als Teil eines Tool-Ergebnisses und führt ihn möglicherweise aus — gegen Dokumente, die mit dem präparierten nichts zu tun haben.

Ein Angreifer braucht dafür keinen Zugang zum System. Es reicht, dem Opfer ein Dokument zu schicken, das dieses einscannt oder importiert.

Die Vorbild-Lösung aus vergleichbaren Projekten — ein Backend-Token mit Leserechten und ein zweiter mit Schreibrechten — ist hier nicht verfügbar: eine Fileee-Session hat immer Vollzugriff, es gibt keine gescopten Tokens. Jede Absicherung muss im eigenen Code liegen.

## Entscheidung

1. **Tool-Ausgaben, die Dokumentinhalte oder OCR-Text enthalten, werden als nicht vertrauenswürdig gekennzeichnet.** Der Inhalt wird von einem Hinweis eingerahmt, der ihn als Daten aus einer fremden Quelle ausweist und klarstellt, dass darin enthaltene Anweisungen nicht zu befolgen sind. Das ist keine Garantie, aber es verschiebt die Grundannahme des Modells.

2. **Alle Tools führen `mcp.ToolAnnotations`** mit `ReadOnlyHint` beziehungsweise `DestructiveHint`. Clients können damit selbst entscheiden, ob sie vor einem Aufruf rückfragen.

3. **Destruktive Operationen brauchen eine ID aus einer vorangegangenen Leseantwort — gebunden an die geprüfte Identität, nicht an die MCP-Sitzung.** Mit [ADR-0015](0015-gangway-als-unterbau.md) öffnet und schließt Gangways erzwungene Statelessness pro Anfrage eine neue, temporäre Sitzung; eine sitzungsgebundene Merkliste könnte damit nie über den einzelnen Request hinaus etwas merken. Der Server führt die Liste der ausgelieferten Dokument-, Kontakt- und Reminder-IDs deshalb je über `serve.IdentityFrom(ctx)` geprüfter Identität, mit eigener Verfallsregelung. Eine ID, die nur im Text eines Dokuments stand, bleibt damit kein gültiges Ziel — genau der Weg, über den eine eingebettete Anweisung sonst wirken könnte. **Wer diesen Server ohne Gangway baut**, bleibt bei der ursprünglichen, session-gebundenen Fassung: eine Liste der in derselben MCP-Sitzung ausgelieferten IDs.

4. ~~Der Funktionsumfang bleibt die primäre Absicherung. Nicht registrierte Tools sind auch durch die überzeugendste eingebettete Anweisung nicht erreichbar. Deshalb Default `read` und `destructive` hinter zwei bewussten Schaltern — siehe ADR-0011.~~ **Überholt seit [ADR-0018](0018-werkzeug-freigabe-und-client-steuerung.md) (2026-08-23) — siehe Nachtrag unten.** Der Server registriert heute jedes Werkzeug für jeden authentifizierten Aufrufer; die Freigabe je Werkzeug liegt beim Client (`ToolAnnotations`, Punkt 2 oben), nicht mehr am nicht registrierten Tool.

5. **Keine Auto-Ausführung von Verweisen aus Dokumentinhalten.** Der Server folgt keiner URL und löst keinen Freigabe-Link auf, nur weil dieser in einem Dokument auftaucht. `resolve_share_link` verarbeitet ausschließlich das, was der Benutzer als Parameter übergibt.

## Konsequenzen

**Positiv**

- Die naheliegendsten Angriffswege sind geschlossen, ohne den Funktionsumfang zu beschneiden.
- Die ID-Whitelist macht destruktive Operationen an einen vorherigen, vom Modell bewusst ausgeführten Leseschritt gebunden.
- Die Annotationen kosten fast nichts und geben Clients die Möglichkeit, selbst vorsichtig zu sein.

**Negativ**

- Kein vollständiger Schutz. Ein hinreichend geschicktes Dokument kann ein Modell weiterhin zu einem Aufruf verleiten, der innerhalb des freigeschalteten Umfangs liegt. Wer `write` freischaltet, akzeptiert dieses Restrisiko — es ist der eigentliche Grund für den `read`-Default.
- Die ID-Whitelist ist Sitzungszustand und damit zusätzliche Komplexität im Server. Sie gilt bewusst nur für die destruktive Gruppe, nicht für `write` — dort wäre der Aufwand unverhältnismäßig, weil Uploads naturgemäß keine Vorgänger-ID haben.
- Der Untrusted-Hinweis verbraucht Kontext-Tokens bei jedem Dokumentinhalt.

**Wirksamkeitsgrenze, die man kennen muss**

Diese Entscheidung senkt die Wahrscheinlichkeit, sie beseitigt das Problem nicht. Die belastbare Aussage war die aus Punkt 4: Ein Tool, das nicht registriert ist, kann nicht aufgerufen werden — das gilt seit [ADR-0018](0018-werkzeug-freigabe-und-client-steuerung.md) nicht mehr (siehe Nachtrag unten). Die verbleibende Absicherung ist die Client-/Benutzer-Freigabe je Werkzeug (`ToolAnnotations`), nicht mehr der serverseitige Funktionsumfang.

## Referenzen

- [MCP Security Best Practices](https://modelcontextprotocol.io/specification/draft/basic/security_best_practices)
- [go-fileee ADR-0007](https://github.com/strausmann/go-fileee/blob/main/docs/adr/0007-ausschluss-destruktiver-operationen.md) — Hard-DELETE ist serverseitig endgültig, ohne Papierkorb
- `CONTRIBUTING.md`, Abschnitt „Besondere Sorgfalt"

## Nachtrag (2026-08-13): „Negativ" — „Sitzungszustand" ist überholt

Der zweite Punkt im Abschnitt „Negativ" oben sagt „Die ID-Whitelist ist Sitzungszustand". Das
war korrekt, bevor [ADR-0015](0015-gangway-als-unterbau.md) diesen Server auf zustandslosen
Betrieb umgestellt hat, und ist seitdem überholt — Punkt 3 weiter oben wurde im Zuge dessen
bereits korrigiert und sagt zutreffend: „gebunden an die geprüfte Identität, nicht an die
MCP-Sitzung". Der Konsequenzen-Absatz wurde bei dieser Überarbeitung nicht mitgezogen und blieb
stehen wie ursprünglich formuliert.

Die tatsächliche Kosten-Aussage bleibt in der Sache unverändert richtig: Die ID-Whitelist ist
zusätzlicher, dauerhafter Zustand im Server — sie hängt heute nur an der geprüften Identität
(`serve.IdentityFrom(ctx)`, mit eigener Verfallsregelung) statt an einer MCP-Sitzung, die es
unter Gangways erzwungener Statelessness ohnehin nicht über den einzelnen Request hinaus gibt.

Die ursprüngliche Fassung des Punkts bleibt oben unverändert stehen, als Protokoll dessen, wie
die Regel ursprünglich formuliert war.

## Nachtrag (2026-08-23): Punkt 4 und die Wirksamkeitsgrenze sind überholt

[ADR-0018](0018-werkzeug-freigabe-und-client-steuerung.md) löst
[ADR-0011](0011-capability-gating.md)s serverseitiges Capability-Gating ab, auf das Punkt 4 sich
stützte („Deshalb Default `read` und `destructive` hinter zwei bewussten Schaltern"). Der Server
registriert seither **jedes** Werkzeug, immer, für jeden Aufrufer, der Gangways Authentifizierung
und die geforderte Scope-Prüfung besteht — es gibt keinen serverseitigen Begriff mehr für „dieses
Werkzeug ist für diesen Aufrufer nicht registriert". Punkt 4s Kernaussage („Nicht registrierte
Tools sind auch durch die überzeugendste eingebettete Anweisung nicht erreichbar") und der daraus
gezogene Schluss im Abschnitt „Wirksamkeitsgrenze, die man kennen muss" sind damit falsch, wenn
man sie auf den heutigen Stand liest.

Was die Absicherung stattdessen trägt: **Punkt 2** — jedes Werkzeug führt wahrheitsgemäße
`mcp.ToolAnnotations` (`ReadOnlyHint`/`DestructiveHint`/`IdempotentHint`), und Client und Benutzer
entscheiden je Werkzeug über Always allow / Needs approval / Blocked (siehe
[ADR-0018](0018-werkzeug-freigabe-und-client-steuerung.md)s eigene Konsequenzen-Liste). Das ist
eine schwächere Garantie als „nicht registriert, also nicht aufrufbar" — sie hängt jetzt von der
Sorgfalt des Clients und des Benutzers ab, nicht mehr allein vom Server. Das Restrisiko, das
Punkt 4 vorher dem `write`-Schalter zuschrieb, gilt jetzt für jedes destruktive Werkzeug, das
künftig hinzukommt, unabhängig von einer Konfiguration.

Punkt 4 und die Wirksamkeitsgrenze-Aussage bleiben oben unverändert stehen, als Protokoll dessen,
wie die Entscheidung ursprünglich begründet war — [ADR-0018](0018-werkzeug-freigabe-und-client-steuerung.md)
ist die verbindliche, aktuelle Quelle für das Freigabe-Modell dieses Servers.
