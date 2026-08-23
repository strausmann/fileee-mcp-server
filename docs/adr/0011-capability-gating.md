# ADR-0011: Funktionsumfang über nicht registrierte Tools statt Laufzeit-Ablehnung

**Status:** superseded
**Datum:** 2026-08-06
**Ersetzt:** —
**Ersetzt durch:** [ADR-0018](0018-werkzeug-freigabe-und-client-steuerung.md)
**Überarbeitet:** —
**Überarbeitet durch:** —
**Verwandt:** [ADR-0013](0013-prompt-injection-schutz.md), [ADR-0015](0015-gangway-als-unterbau.md), [go-fileee ADR-0007](https://github.com/strausmann/go-fileee/blob/main/docs/adr/0007-ausschluss-destruktiver-operationen.md), [fileee-server ADR-0008](https://github.com/strausmann/fileee-server/blob/main/docs/adr/0008-fileee-server.md)

## Kontext

Der Server vermittelt Zugriff auf private Finanzdokumente. Welche Operationen ein AI-Client darauf ausführen darf, ist keine Frage, die sich allgemein beantworten lässt — der eine will nur suchen und lesen, der nächste auch hochladen, ein dritter zusätzlich Freigaben erzeugen.

`go-fileee` ADR-0007 schließt destruktive Operationen für die Library kategorisch aus; `fileee-server` ADR-0008 hat das zu einem Gate aufgeweicht (`FILEEE_ALLOW_DESTRUCTIVE`, Routen existieren sonst gar nicht). Für einen MCP-Server kommt eine Eigenheit hinzu, die es bei einer REST-API nicht gibt: **das Modell entscheidet selbst, welches Tool es aufruft.** Ein Tool, das im Katalog steht, wird früher oder später aufgerufen — auch versehentlich, auch auf Zuruf aus einem Dokumentinhalt.

Fileees Hard-DELETE ist zudem serverseitig endgültig, ohne Papierkorb und ohne Bestätigungsschritt.

## Entscheidung

1. **Vier Capability-Gruppen**, über `FILEEE_CAPABILITIES` kommasepariert konfigurierbar: `read` (Default), `write`, `share`, `destructive`.

2. **Nicht freigeschaltete Tools werden gar nicht erst registriert.** Sie erscheinen nicht in `tools/list`, das Modell sieht sie nicht und kann sie nicht halluzinieren. Das ist der wesentliche Unterschied zu einer Prüfung im Handler: bei einem Logikfehler im Handler wäre die Operation erreichbar, bei einem nicht registrierten Tool nicht.

   Das ist auch der Grund, warum die Vorbild-Lösung aus `bookstack-mcp` — getrennte Backend-Tokens mit unterschiedlichen Rechten — hier nicht nachgebaut werden kann: eine Fileee-Session hat immer Vollzugriff, es gibt keine gescopten Tokens. Das Gate liegt vollständig im eigenen Code, also muss es so früh wie möglich greifen.

3. **Eine `*mcp.Server`-Instanz je Capability-Set**, ausgewählt in der Handler-Factory anhand der Token-Information. Ein gemeinsamer Server für alle Benutzer wäre mit benutzerabhängigem Funktionsumfang logisch unvereinbar, weil `tools/list` dann für alle identisch ausfiele. Die Zahl der Instanzen ist beim Start bekannt und begrenzt: das kartesische Produkt der konfigurierten Gruppen, nicht eine Instanz pro Benutzer.

4. **Rangfolge, verbindlich, als Kette mit Vorrang — nicht als Schnittmenge aus allen Quellen:**

   1. `FILEEE_CAPABILITIES` ist die **Obergrenze**. Keine andere Quelle schaltet darüber hinaus etwas frei.
   2. Ist `MCP_OIDC_CAPABILITY_CLAIM` gesetzt und trägt das Token mindestens einen bekannten Wert, gilt dieser, geschnitten mit der Obergrenze. Der IdP gewinnt, weil dort die Benutzerverwaltung stattfindet.
   3. Sonst gilt `FILEEE_ACCOUNT_<KEY>_CAPABILITIES`, geschnitten mit der Obergrenze.
   4. Sonst die Obergrenze.

5. **Fail-closed an Stufe 2.** Ist der Claim konfiguriert, das Token trägt aber keinen bekannten Wert, ergibt die Auswertung `read` — und fällt **nicht** auf Stufe 3 oder 4 zurück. Andernfalls hieße „keine Rolle zugewiesen" Vollzugriff, und ein vergessener Klick im IdP wäre eine stille Rechteausweitung.

6. **`destructive` ist über keinen Claim erreichbar.** Der Server ignoriert einen solchen Wert im Token. Diese Gruppe entsteht ausschließlich aus `FILEEE_CAPABILITIES` **und** `FILEEE_ALLOW_DESTRUCTIVE=true` — zwei bewusste Eingriffe am Server. Zusätzlich gilt: Audit-Log auf `warn` vor der Ausführung, und die zu löschende ID muss aus einer vorangegangenen Leseantwort derselben Sitzung stammen.

## Konsequenzen

**Positiv**

- Der Betreiber bestimmt den Funktionsumfang, nicht die Software. Kein Streit darüber, was der „richtige" Default ist.
- Was nicht registriert ist, kann auch bei einem Fehler im Handler nicht aufgerufen werden.
- Berechtigungen lassen sich dort pflegen, wo Benutzer ohnehin verwaltet werden — als Entra-App-Rolle oder Authentik-Gruppe.
- Der Default `read` ist die sichere Startlinie und entspricht der Vorgabe aus dem `go-fileee`-README.

**Negativ**

- Mehrere Server-Instanzen bedeuten mehr Zustand beim Start und eine zusätzliche Fehlerquelle bei der Auswahl. Abgesichert über einen Test, der `tools/list` für zwei Konten mit unterschiedlichem Umfang vergleicht.
- Die Rangfolge ist erklärungsbedürftig. Sie steht deshalb in `README.md`, in beiden IdP-Anleitungen und hier — bewusst mehrfach.
- Ändert ein Betreiber `FILEEE_CAPABILITIES`, ändert sich der Tool-Katalog bestehender Verbindungen erst nach einem Neustart. Vertretbar, weil es sich um eine Betriebsentscheidung handelt, nicht um Laufzeitverhalten.

**Testpflicht**

Je ein Fall pro Rangfolgestufe, plus: Claim gesetzt aber leer (muss `read` ergeben), Claim enthält `destructive` (muss ignoriert werden), Claim überschreitet die Obergrenze (muss gekappt werden), Konto-Override enthält eine global nicht gesetzte Gruppe (muss den Start abbrechen).

## Referenzen

- `README.md`, Abschnitt „Funktionsumfang festlegen"
- `docs/idp/authentik.md` Abschnitt 4a, `docs/idp/entra-id.md` Abschnitt 3a
- [go-fileee ADR-0007](https://github.com/strausmann/go-fileee/blob/main/docs/adr/0007-ausschluss-destruktiver-operationen.md) — Ausgangslage: kategorischer Ausschluss in der Library

## Nachtrag (2026-08-13): Punkt 6 — „derselben Sitzung" ist überholt

Punkt 6 oben verlangt, dass die zu löschende ID „aus einer vorangegangenen Leseantwort derselben
Sitzung" stammen muss. Diese Formulierung stammt aus der Zeit vor
[ADR-0015](0015-gangway-als-unterbau.md) und ist überholt: Gangways erzwungene Statelessness öffnet
und schließt pro Anfrage eine neue, temporäre Sitzung — es gibt keinen Sitzungsbegriff, der über den
einzelnen Request hinaus etwas merken könnte. Die tatsächliche Bindung ist die geprüfte Identität aus
`serve.IdentityFrom(ctx)`, nicht die MCP-Sitzung — bereits korrekt beschrieben in
[ADR-0013](0013-prompt-injection-schutz.md) Punkt 3 und in `CONTRIBUTING.md`, Abschnitt „Destruktive
Whitelist".

Die ursprüngliche Fassung von Punkt 6 bleibt oben unverändert stehen, als Protokoll dessen, wie die
Regel ursprünglich formuliert war.
