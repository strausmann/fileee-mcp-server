# ADR-0015: Gangway v0.2.0 als Unterbau für Anmeldung, Freigabeliste und Zugriffsprotokoll

**Status:** accepted
**Datum:** 2026-08-08
**Ersetzt:** —
**Ersetzt durch:** —
**Überarbeitet:** [ADR-0012](0012-multi-account-mapping.md), [ADR-0013](0013-prompt-injection-schutz.md)
**Überarbeitet durch:** —
**Verwandt:** [ADR-0009](0009-resource-server-statt-eigener-authorization-server.md), [ADR-0010](0010-idp-agnostische-konfiguration.md), [ADR-0011](0011-capability-gating.md), [ADR-0016](0016-anbieter-namensraeume-statt-roher-oidc-parameter.md), [ADR-0017](0017-diagnose-protokoll-mit-erzwungener-maskierung.md), [Gangway](https://github.com/strausmann/gangway)

## Kontext

Dieser Server tritt nach [ADR-0009](0009-resource-server-statt-eigener-authorization-server.md) als
OAuth-2.1-Resource-Server auf und muss dafür — unabhängig von den fileee-spezifischen Tools — eine
ganze Reihe wiederkehrender Bausteine selbst bauen: Bearer-Token-Verifikation, Protected-Resource-
Metadata, eine Adress-Freigabeliste (`MCP_ALLOWED_SUBJECTS`), Freigabe je Werkzeug
([ADR-0011](0011-capability-gating.md)) und ein Zugriffsprotokoll für sicherheitsrelevante
Entscheidungen. Keiner dieser Bausteine ist fileee-spezifisch — sie beträfen jeden MCP-Server mit
demselben Anspruch.

Das separat entstandene Projekt [Gangway](https://github.com/strausmann/gangway) — eine
wiederverwendbare Auth-/Autorisierungs-Schicht für MCP-Server auf demselben Go-SDK
(`github.com/modelcontextprotocol/go-sdk v1.7.0`) — deckt genau diese Bausteine ab: Anmeldung
(`identity.Verifier`, u. a. `identity.NewOIDC`), eine Adress-Freigabeliste, Freigabe je Werkzeug
(`access.Decider`) und ein Zugriffsprotokoll (`accesslog`). Version v0.2.0 ist die aktuelle Fassung
(https://gangway.strausmann.cloud).

**Der unmittelbare Anlass ist kein neuer:** [ADR-0012](0012-multi-account-mapping.md) dokumentiert
unter „Die SDK-Semantik" einen konkreten Fehler im Go-SDK v1.7.0 — `auth.TokenInfoFromContext(ctx)`
liefert innerhalb eines Tool-Handlers bei zustandsbehafteten (stateful) Sessions nicht das Token des
aktuellen `tools/call`, sondern das des `initialize`-Requests, weil `connectStreamable` den
Connection-Kontext einmalig bindet und alle Handler-Kontexte sich davon ableiten. In einer von
Benutzer A eröffneten Session würde damit jeder spätere Request auf A's Fileee-Konto auflösen —
ein Fehler, der ohne Prüfung des SDK-Quelltexts nicht aufgefallen wäre. Gangway hat denselben Fehler
unabhängig gefunden (Doc-Kommentar zu `AttachMCP` in `serve/serve.go`, Stand Tag `v0.1.0`) und ihn
strukturell anders gelöst als ADR-0012: nicht durch einen Lese-Pfad, der den betroffenen Kontext
umgeht (`req.GetExtra().TokenInfo`), sondern indem die SDK-eigene `auth`-Middleware für die Identität
gar nicht erst verwendet wird — eine eigene HTTP-Middleware verifiziert das Bearer-Token vor dem
MCP-Handler, und `AttachMCP` erzwingt `mcp.StreamableHTTPOptions.Stateless = true`. Dadurch entsteht
gar keine sitzungsübergreifende Kontextbindung mehr, an der ein `context.Value`-Read hängenbleiben
könnte — der Fehler wird nicht umgangen, sondern strukturell unmöglich gemacht.

Der Nachtrag vom 2026-08-08 in ADR-0012 hat diesen Fund bereits als Beobachtung festgehalten, ohne
eine Übernahme-Entscheidung zu treffen. Dieses ADR trifft sie.

**Was nicht das Ziel ist:** Dieses ADR entscheidet nicht über die vier `backend.TokenSource`-Bausteine
Gangways (`StaticToken`, `PerUser`, `PassThrough`, `Exchange`) — keiner davon passt Fileees
Credential-Modell (ein zustandsbehafteter, gepoolter `*fileee.Client` mit Cookie-Jar statt eines
weiterreichbaren Strings). Das Paket `backend` ist an keiner Stelle in `serve`/`access`/`identity`
referenziert und bleibt für diesen Server schlicht ungenutzt — das ändert nichts an Gangways
Funktion für Anmeldung, Freigabeliste und Zugriffsprotokoll.

## Entscheidung

Der Server benutzt **Gangway v0.2.0** für Anmeldung, Adress-Freigabeliste, Freigabe je Werkzeug und
Zugriffsprotokoll, statt diese Bausteine selbst zu bauen.

Konkret: `serve.Server.AttachMCP` übernimmt die Bearer-Token-Verifikation
(`identity.NewOIDC`) und die Protected-Resource-Metadata-Auslieferung; die Identität eines Requests
wird ausschließlich über `serve.IdentityFrom(ctx)` gelesen, niemals selbst zwischengespeichert
(Gangways eigene Warnung dazu: `serve.go`, „Never … cache or persist it beyond the request it was
read from"). Die vier Capability-Gruppen aus [ADR-0011](0011-capability-gating.md) werden als eigener
`access.Decider` über `WithDecider` registriert — Gangways `access.Decider`-Schnittstelle ist absichtlich
nicht auf zwei Werte beschränkt (`ToolKind` ist kein geschlossener Enum), das deckt die
Laufzeit-Ablehnung der vier Gruppen vollständig ab.

Gangway löst denselben SDK-Fehler, den ADR-0012 beschreibt, strukturell — durch erzwungenen
zustandslosen Betrieb, nicht durch einen Lese-Pfad, der den betroffenen Kontext umgeht. Es bringt
zusätzlich die Adress-Freigabeliste und das Zugriffsprotokoll mit, die dieser Server sonst selbst
bauen müsste.

## Alternativen

| Option | Pro | Contra | Warum (nicht) gewählt |
|---|---|---|---|
| Selbst bauen (bisheriger Stand, ADR-0012/0013) | Volle Kontrolle, keine Fremdabhängigkeit | Vier wiederkehrende Bausteine (Anmeldung, Freigabeliste, Tool-Freigabe, Zugriffsprotokoll) sind kein fileee-spezifisches Problem, aber fileee-spezifischer Code — zwei unabhängige Entwürfe (dieser Server und Gangway) sind auf **denselben** SDK-Fehler gestoßen, was zeigt, dass diese Schicht eigenständig entsteht, ob man sie plant oder nicht | Verworfen — eine gemeinsame Schicht löst das SDK-Problem ein für alle Mal, statt es in jedem MCP-Server neu zu entdecken |
| Gangway v0.2.0 als Unterbau | Löst den SDK-Fehler strukturell (erzwungene Statelessness), bringt Freigabeliste + Zugriffsprotokoll mit, unabhängig gewartet | Zusätzliche Abhängigkeit; Gangways `AttachMCP` bindet genau eine `*mcp.Server`-Instanz — für Setups mit capability-abhängig unterschiedlichem Tool-Katalog (mehrere IdP-Claims/Konten mit unterschiedlichen Rechten) ist das heute eine Lücke, kein registrierter Weg für „Tool erscheint gar nicht in `tools/list`" je nach Identität; im einfachsten Fall (statische Capability-Menge über die Prozesslaufzeit) reicht das heutige `AttachMCP` aber vollständig aus | **Gewählt** |

## Konsequenzen

**Positiv**

- Anmeldung, Adress-Freigabeliste, Freigabe je Werkzeug und Zugriffsprotokoll sind nicht mehr
  fileee-spezifischer Eigencode, sondern eine unabhängig gewartete, wiederverwendbare Schicht.
- Der SDK-Kontext-Fehler aus ADR-0012 ist strukturell ausgeschlossen (erzwungene Statelessness),
  nicht nur durch eine Konvention verhindert, die man beim Schreiben eines neuen Tools verletzen kann.
- Gangways eigene Statelessness-Garantie sichert nebenbei jedes weitere context-gebundene
  Per-Request-Muster ab, nicht nur die Identität — ein Bonus, der beim reinen `GetExtra`-Ansatz nicht
  automatisch mitkäme.

**Negativ / Kosten**

1. **ADR-0012 Punkte 2 und 3 werden gegenstandslos:** Die Identität kommt aus
   `serve.IdentityFrom(ctx)`, nicht mehr aus `req.GetExtra().TokenInfo`. Die Beobachtung zum
   SDK-Verhalten («Die SDK-Semantik») bleibt gültig und bleibt wörtlich stehen — sie gilt weiterhin
   für jeden, der diesen Server ohne Gangway baut.
2. **ADR-0013 Punkt 3 muss umgebaut werden:** Eine an die MCP-Sitzung gebundene Merkliste
   ausgelieferter IDs kann unter Gangway nicht funktionieren, weil der erzwungene zustandslose
   Betrieb je Anfrage eine neue, temporäre Sitzung öffnet und wieder schließt — es gibt keine
   Sitzung, die über den einzelnen Request hinaus etwas merken könnte. Die Merkliste muss stattdessen
   an die über `serve.IdentityFrom(ctx)` geprüfte Identität gebunden werden, mit einer eigenen
   Verfallsregelung (Details: Umsetzungsschritt, in dem `tools_destructive.go` entsteht).
3. **`CONTRIBUTING.md`** wiederholt unter „Besondere Sorgfalt" die Regel aus ADR-0012 Punkt 2/3 und
   wird entsprechend nachgezogen.
4. **Folge-To-do:** `AttachMCP` bindet heute genau eine `*mcp.Server`-Instanz. Sobald
   [ADR-0011](0011-capability-gating.md)s Vorgabe „eine `*mcp.Server`-Instanz je Capability-Set"
   tatsächlich mehrere Capability-Sets zur Laufzeit braucht (IdP-Claim oder Konto-Override weichen
   von der globalen Obergrenze ab), fehlt in Gangway ein Weg, den `*mcp.Server` pro Request anhand
   der bereits verifizierten Identität auszuwählen. Das ist keine Fileee-spezifische Einschränkung,
   sondern eine offene Erweiterung von Gangway selbst — nachzuverfolgen im Gangway-Repo, nicht hier.

## Nachtrag (2026-08-08): `MCP_AUTH_MODE=token` ist über Gangway derzeit nicht erreichbar

**Anlass.** Beim Umsetzungsschritt, der Gangway tatsächlich verdrahtet (`internal`/`cmd/fileee-mcp-server`,
`New`/`Server.Run`), zeigte `go doc github.com/strausmann/gangway/serve` und der Quelltext von
`serve.New`: Gangway baut **unbedingt** einen `identity.NewOIDC`-Verifier auf
(`s.verifier, err = identity.NewOIDC(ctx, identity.OIDCConfig{...})`, `serve.go`, ohne Verzweigung).
`identity.Verifier` ist zwar ein Interface, aber `Server` hält seinen `verifier` unexportiert und
bietet keine Option (`serve.Option`), einen anderen `identity.Verifier` einzuhängen — etwa einen für
ein statisches Bearer-Token, wie ihn `MCP_AUTH_MODE=token` aus [ADR-0009](0009-resource-server-statt-eigener-authorization-server.md)
und der Konfiguration (`AuthMode`, `APIToken`) vorsieht.

**Was das bedeutet.** Solange Gangway keinen Weg bietet, den Verifier auszutauschen, kann dieser
Server `MCP_AUTH_MODE=token` (und `both`, dessen Token-Zweig genauso betroffen ist) **nicht** über
Gangway bedienen — unabhängig davon, wie gut `LoadConfig` diesen Modus validiert. `New` in
`internal/server/server.go` lehnt deshalb jeden Config mit `AuthMode != AuthOIDC` explizit und
mit benannter Ursache ab, statt eine verwirrende Fehlermeldung tief aus `identity.NewOIDC` (leere
`IssuerURL`) durchsickern zu lassen oder — schlimmer — Gangway zu umgehen und selbst eine
Auth-Schicht für den Token-Fall zu bauen. Letzteres widerspräche der Entscheidung dieses ADRs direkt:
der Sinn von Gangway ist, dass Anmeldung, Freigabeliste, Werkzeug-Freigabe und Zugriffsprotokoll
**nicht** wieder fileee-spezifischer Eigencode werden.

**Was das nicht bedeutet.** Kein Rückzieher von der Entscheidung oben — Gangway bleibt für die
`oidc`-Betriebsart (die im README als das primäre Szenario beschriebene „Remote-Connector mit
OAuth-Anmeldung") vollständig tragfähig, und genau das ist das Szenario, für das dieser Server als
OAuth-2.1-Resource-Server überhaupt entworfen wurde ([ADR-0009](0009-resource-server-statt-eigener-authorization-server.md)).
Betroffen ist ausschließlich die lokale/Token-Betriebsart aus dem README-Abschnitt „Eine Person, ein
Fileee-Konto, kein Identity Provider".

**Wie es weitergehen kann.** Zwei nicht sich ausschließende Wege, beide außerhalb dieses ADRs zu
entscheiden:

1. Gangway bekommt eine Möglichkeit, den `identity.Verifier` zu injizieren (z. B. ein
   `serve.WithVerifier(v identity.Verifier) Option`, analog zu `WithDecider`) — dann bräuchte dieser
   Server nur noch einen kleinen `identity.Verifier` für ein statisches Token zu schreiben und
   weiterhin Gangways Adress-Freigabeliste, Werkzeug-Freigabe und Zugriffsprotokoll zu nutzen. Das ist
   der naheliegendere Weg, weil er die Struktur dieses ADRs (Gangway als vollständiger Unterbau)
   unangetastet lässt.
2. Der Token-Modus bleibt dauerhaft ohne Gangway — ein zweiter, deutlich schlankerer Codepfad in
   diesem Server, der Gangway für `oidc` nutzt und für `token` selbst eine einfache
   Bearer-Token-Prüfung vornimmt. Das widerspräche der „ein Weg für alles"-Absicht dieses ADRs und
   ist deshalb die unattraktivere Option.

Dieser Nachtrag ist ein Fund, kein Fix — die tatsächliche Entscheidung (und ein etwaiges
Feature-Issue im Gangway-Repo) steht noch aus.

## Referenzen

- [Gangway](https://github.com/strausmann/gangway), Dokumentation https://gangway.strausmann.cloud
- [ADR-0009](0009-resource-server-statt-eigener-authorization-server.md) — OAuth-2.1-Resource-Server-Rolle, die Gangway übernimmt
- [ADR-0011](0011-capability-gating.md) — die vier Capability-Gruppen, die als `access.Decider` registriert werden
- [ADR-0012](0012-multi-account-mapping.md) — SDK-Kontext-Fehler, Punkte 2/3 hierdurch überarbeitet
- [ADR-0013](0013-prompt-injection-schutz.md) — Session-Whitelist, Punkt 3 hierdurch überarbeitet
- Go MCP SDK v1.7.0, `mcp/streamable.go` und `mcp/shared.go` — Grundlage des SDK-Kontext-Fehlers
- Gangway `serve/serve.go`, Doc-Kommentar zu `AttachMCP` (Stand Tag `v0.1.0`) — unabhängiger zweiter Fund desselben Fehlers
