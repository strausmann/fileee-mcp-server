# Architecture Decision Records — fileee-mcp-server

Diese Registry führt alle Architecture Decision Records (ADRs) für `fileee-mcp-server`. ADRs dokumentieren wichtige, langfristig wirkende Entscheidungen — inklusive Kontext und Konsequenzen — damit spätere Sessions und Mitwirkende nachvollziehen können, **warum** etwas so gebaut wurde und nicht anders.

Neues ADR anlegen: ein bestehendes ADR aus diesem Verzeichnis als Vorlage kopieren (Felder Status/Datum/Lineage/Kontext/Entscheidung/Konsequenzen/Referenzen) nach `docs/adr/NNNN-slug.md` (nächste freie Nummer, vierstellig) und unten in die Tabelle eintragen.

## Nummernkreis: fortlaufend über die Repo-Familie

Die Fileee-Repos teilen sich einen durchgehenden ADR-Nummernkreis, damit Querverweise eindeutig bleiben:

| Bereich | Repo | Inhalt |
|---|---|---|
| 0001–0007 | [`strausmann/go-fileee`](https://github.com/strausmann/go-fileee/tree/main/docs/adr) | Core-Lib: Library-first-Architektur, Auth-Modell, Reverse-Engineering-Risiko, Test-Strategie, schonender Betrieb/Rate-Limiting, Domänen-Neutralität, Ausschluss destruktiver Operationen |
| 0008 | [`strausmann/fileee-server`](https://github.com/strausmann/fileee-server/tree/main/docs/adr) | REST-API-Service |
| ab 0009 | **dieses Repo** | MCP-Server |

ADRs anderer Repos werden **nicht** dupliziert. Verweist ein ADR auf ein ADR in einem der Schwester-Repos, wird die vollständige Cross-Repo-URL verwendet, keine relativen Pfade.

Besonders relevant für dieses Repo sind [ADR-0005 (schonender Betrieb / Rate-Limiting)](https://github.com/strausmann/go-fileee/blob/main/docs/adr/0005-schonender-betrieb-rate-limiting.md) und [ADR-0007 (Ausschluss destruktiver Operationen)](https://github.com/strausmann/go-fileee/blob/main/docs/adr/0007-ausschluss-destruktiver-operationen.md).

## ADR-Regelwerk

- **Was/Wann:** Ein ADR dokumentiert jede bedeutsame Architektur-, Technologie- oder Betriebs-Entscheidung samt Kontext und Konsequenzen. **Nicht** für Trivialitäten, reine Formatierung oder offensichtliche Umsetzungsdetails.
- **Nummerierung:** fortlaufend `NNNN` (4-stellig), Dateiname `NNNN-kebab-slug.md`. Nummern werden **nie wiederverwendet** — auch nicht für abgelöste oder verworfene ADRs.
- **Status-Lifecycle:** `proposed` → `accepted` → (`superseded` | `deprecated`).
- **Lineage (beidseitig pflegen):** Die Header-Felder `Ersetzt` / `Ersetzt durch` (vollständige Ablösung → Vorgänger auf `superseded` setzen) und `Verwandt` (Querbezug ohne Ablösung) werden auf **beiden** beteiligten ADRs eingetragen. Beim Ablösen nur den **Header** des alten ADR anfassen — Kontext und Entscheidung des alten ADR werden nie umgeschrieben, sie sind ein historisches Protokoll.
- **Registry-Pflicht:** Jedes neue oder im Status geänderte ADR wird sofort in die Tabelle unten eingetragen bzw. aktualisiert. Ein ADR, das nicht in der Registry steht, gilt als übersehen und damit als nicht existent.
- **Sprache:** Deutsch (echte Umlaute ä ö ü ß), Code/CLI/Bezeichner auf Englisch.

## Registry

| Nr. | Titel | Status | Datum |
|-----|-------|--------|-------|
| [0009](0009-resource-server-statt-eigener-authorization-server.md) | Reiner Resource Server statt eigenem Authorization Server | accepted | 2026-08-06 |
| [0010](0010-idp-agnostische-konfiguration.md) | IdP-agnostische Konfiguration über drei orthogonale Achsen | accepted | 2026-08-06 |
| [0011](0011-capability-gating.md) | Funktionsumfang über nicht registrierte Tools statt Laufzeit-Ablehnung | superseded | 2026-08-06 |
| [0012](0012-multi-account-mapping.md) | Konto-Auflösung über den signierten Claim des aktuellen Requests | accepted | 2026-08-06 |
| [0013](0013-prompt-injection-schutz.md) | Dokumentinhalte sind fremdbestimmte Daten | accepted | 2026-08-06 |
| [0015](0015-gangway-als-unterbau.md) | Gangway v0.2.0 als Unterbau für Anmeldung, Freigabeliste und Zugriffsprotokoll | accepted | 2026-08-08 |
| [0016](0016-anbieter-namensraeume-statt-roher-oidc-parameter.md) | Ein Variablen-Namensraum je Identity Provider statt roher OIDC-Parameter | accepted | 2026-08-09 |
| [0017](0017-diagnose-protokoll-mit-erzwungener-maskierung.md) | Diagnose-Protokoll mit erzwungener Maskierung statt vertrauensbasierter Feldwahl | accepted | 2026-08-12 |
| [0018](0018-werkzeug-freigabe-und-client-steuerung.md) | Werkzeug-Freigabe über Client-Steuerung statt serverseitigem Funktionsumfang | accepted | 2026-08-23 |
| [0019](0019-id-whitelist-gilt-auch-fuer-share.md) | Die Ausgeliefert-ID-Whitelist gilt auch für die `share`-Klasse | accepted | 2026-08-24 |

> **Lücke:** Die Nummer **0014** ist in diesem Repo nie vergeben worden — sie fehlt zwischen 0013 und 0015 ohne erkennbaren Grund. Nummern werden nicht wiederverwendet, sie bleibt also frei. Wer den Grund kennt, trägt ihn hier nach.

## Status-Werte

| Status | Bedeutung |
|--------|-----------|
| `proposed` | Entwurf, noch nicht final entschieden |
| `accepted` | Gültig, wird umgesetzt/befolgt |
| `superseded` | Durch ein neueres ADR vollständig abgelöst (siehe `Ersetzt durch` im Header) |
| `deprecated` | Nicht mehr gültig, ohne direkten Nachfolger |
