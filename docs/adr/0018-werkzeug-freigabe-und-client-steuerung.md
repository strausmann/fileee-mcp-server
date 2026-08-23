# ADR-0018: Werkzeug-Freigabe über Client-Steuerung statt serverseitigem Funktionsumfang

**Status:** accepted
**Datum:** 2026-08-23
**Ersetzt:** [ADR-0011](0011-capability-gating.md)
**Ersetzt durch:** —
**Überarbeitet:** [ADR-0010](0010-idp-agnostische-konfiguration.md) — Punkt 3 (`MCP_OIDC_CAPABILITY_CLAIM` entfällt)
**Überarbeitet durch:** —
**Verwandt:** [ADR-0012](0012-multi-account-mapping.md), [ADR-0013](0013-prompt-injection-schutz.md), [ADR-0015](0015-gangway-als-unterbau.md)

## Kontext

MCP-Connectoren sollen alle Werkzeuge mit korrekten `ToolAnnotations` anbieten und die Freigabe je
Werkzeug dem **Client und dessen Benutzer** überlassen — Always allow / Needs approval / Blocked,
gemäß der [MCP-Spezifikation für Tool-Annotationen](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)
(`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`) und dem client-seitigen
Freigabemodell, das Claude und andere MCP-Hosts darauf aufbauen. Anthropics [Connector-Review-Kriterien](https://claude.com/docs/connectors/building/review-criteria)
erwarten von einem Server, dass er beschreibt, was ein Werkzeug tut und wie riskant es ist, damit
Host und Benutzer entscheiden können — nicht, dass er Werkzeuge anhand einer serverseitigen Policy
gar nicht erst im Katalog anbietet, die der Benutzer nie zu sehen bekommt.

[ADR-0011](0011-capability-gating.md)s serverseitiges Gating über vier Capability-Gruppen
(`read`/`write`/`share`/`destructive`), konfigurierbar über `FILEEE_CAPABILITIES` und
`FILEEE_ALLOW_DESTRUCTIVE`, aufgelöst gegen einen optionalen IdP-Claim
(`MCP_OIDC_CAPABILITY_CLAIM`), und durchgesetzt indem ein Werkzeug schlicht nie registriert wird —
widerspricht diesem Modell. Es holt genau die Werkzeug-Freigabe-Entscheidung auf den Server zurück,
die das Connector-Modell beim Client und dessen Benutzer verortet. Es vervielfacht zudem den
Server-Zustand: eine `*mcp.Server`-Instanz je Capability-Set, ausgewählt pro Aufrufer beim
Verbindungsaufbau, allein damit ein nicht registriertes Werkzeug nie in `tools/list` erscheint.

Die Write-/Share-/Destructive-Werkzeuge, die dieses Gating eigentlich schützen sollte, existieren
auf diesem Branch noch gar nicht — nur die Gruppe `read` ist implementiert (32 Werkzeuge). Das
Gating jetzt zu entfernen, bevor diese Werkzeuge existieren, erspart es, eine serverseitige
Policy-Schicht um sie herum erst zu bauen und dann wieder abzubauen.

Konto-Isolation ([ADR-0012](0012-multi-account-mapping.md)) und die Einrahmung fremdbestimmter
Dokumentinhalte ([ADR-0013](0013-prompt-injection-schutz.md)) sind orthogonale Belange — welchem
Fileee-Konto ein Aufrufer zugeordnet ist, und wie Dokumentinhalte präsentiert werden, damit das
Modell sie nicht als Anweisungen behandelt — und bleiben von dieser Entscheidung unberührt.

## Entscheidung

Der Server registriert **jedes** Werkzeug, immer, für jeden Aufrufer, der Gangways
Authentifizierung und die geforderte Scope-Prüfung besteht. Es gibt keinen serverseitigen Begriff
mehr für „dieser Aufrufer darf Werkzeug X nicht sehen".

1. **Jedes Werkzeug trägt einen `Title` und die zutreffenden Hinweise** — `ReadOnlyHint: true` für
   die heute existierenden lesenden Werkzeuge; `DestructiveHint`/`IdempotentHint` für künftige
   Write-/Destructive-Werkzeuge, wahrheitsgemäß je Operation gesetzt statt vorbelegt. `Title` ist
   eine kurze, für Menschen lesbare Bezeichnung, die eine Freigabe-Oberfläche eines Clients neben
   dem rohen Werkzeugnamen anzeigen kann — bereits umgesetzt für alle 36 Werkzeuge, die
   `tools.RegisterAll` anmeldet.
2. **Gangway autorisiert jeden Aufruf mit `access.AllowAll()`.** Es gibt genau eine
   `*mcp.Server`-Instanz, einmal angemeldet, nicht pro Aufrufer ausgewählt. `AttachMCPSelector`
   bleibt weiterhin nötig — Gangway routet `/mcp` nur über eine angehängte Selector-Funktion oder
   Server-Instanz (siehe [ADR-0015](0015-gangway-als-unterbau.md)) —, aber die Selector-Funktion
   liefert nach erfolgreicher Authentifizierung und Scope-Prüfung immer dieselbe Instanz zurück.
3. **`FILEEE_CAPABILITIES`, `FILEEE_ALLOW_DESTRUCTIVE` und `MCP_OIDC_CAPABILITY_CLAIM` entfallen**
   vollständig aus der Konfiguration — nicht mit einem Default versehen, nicht als deprecated
   weiterhin akzeptiert. Ein Deployment, das sie dennoch setzt, bekommt dieselbe Ablehnung als
   unbekannte Variable wie jede andere veraltete Einstellung, kein stilles Ignorieren.
4. **Konto-Isolation und Inhalts-Einrahmung bleiben unverändert.** [ADR-0012](0012-multi-account-mapping.md)
   entscheidet weiterhin, gegen welches Fileee-Konto ein Aufruf läuft;
   [ADR-0013](0013-prompt-injection-schutz.md) rahmt Dokumentinhalte weiterhin als fremdbestimmte
   Daten ein. Keines von beiden ist eine Form von Werkzeug-Gating, und keines wird von dieser
   Entscheidung angefasst.

## Konsequenzen

**Positiv**

- Client und Benutzer treffen die eigentliche Freigabe-Entscheidung je Werkzeug, mit vollständiger
  Information (`Title`, `ReadOnlyHint`, `DestructiveHint`, `IdempotentHint`) statt einer groben,
  vom Server gewählten Vierteilung, deren Begründung der Benutzer nie zu sehen bekommt.
- Eine Server-Instanz, ein Code-Pfad. Kein Instanz-Katalog je Capability-Set, der mit dem
  wachsenden Werkzeug-Bestand synchron gehalten werden muss — künftige Write-/Share-/
  Destructive-Werkzeuge werden einfach mit wahrheitsgemäßen Hinweisen registriert, ohne dass dafür
  neue Gating-Logik entsteht.
- Kein Werkzeug kann trotz „nicht freigeschaltet" durch einen Handler-Fehler erreichbar werden —
  es gibt von vornherein keine Freischaltung, die man umgehen könnte.
- Entspricht der Form, die MCP-Hosts unter aktiver client-seitiger Prüfung von einem Connector
  bereits erwarten: Claude und andere Hosts bauen ihre Freigabe-Oberfläche auf `ToolAnnotations`
  auf; ein Server, der Werkzeuge versteckt statt sie zu annotieren, arbeitet gegen diese
  Oberfläche, nicht mit ihr.

**Negativ / Kosten**

- Ein Deployment-Betreiber verliert die Möglichkeit, einen Connector-Link herauszugeben, der per
  serverseitiger Policy nur eine Teilmenge der Werkzeuge zeigt. Ein Deployment, das wirklich
  „read-only, egal was der Client tut" will, braucht dafür einen anderen Mechanismus — etwa einen
  eigenen, bewusst read-only gebauten Build, oder eine außerhalb dieses Servers durchgesetzte
  Betreiber-Policy. Das bleibt legitim, ist hier aber nicht Gegenstand und ausdrücklich nicht mehr
  der Standardfall.
- Jedes künftige Write-/Share-/Destructive-Werkzeug muss seine Hinweise gleich beim ersten Mal
  korrekt und ehrlich setzen — es gibt kein zweites Gate dahinter, das ein unzureichend
  annotiertes destruktives Werkzeug auffängt. Das verschiebt die Sorgfaltspflicht von „wurde diese
  Capability-Gruppe je freigeschaltet" (eine Konfigurationsfrage, beim Start leicht prüfbar) zu
  „beschreiben `DestructiveHint`/`IdempotentHint` dieses Werkzeugs wahrheitsgemäß, was es tut"
  (eine Frage des Code-Reviews je Werkzeug).
- Betreiber, die sich auf `FILEEE_CAPABILITIES`, `FILEEE_ALLOW_DESTRUCTIVE` oder
  `MCP_OIDC_CAPABILITY_CLAIM` verlassen, müssen sie aus ihrer Deployment-Konfiguration entfernen;
  der Server lehnt sie als unbekannt ab, statt sie still zu akzeptieren und zu ignorieren.

## Referenzen

- MCP-Spezifikation, [Tool annotations](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)
- Anthropic, [Connector review criteria](https://claude.com/docs/connectors/building/review-criteria)
- [ADR-0011](0011-capability-gating.md) — abgelöstes serverseitiges Capability-Gating
- [ADR-0010](0010-idp-agnostische-konfiguration.md) — Punkt 3, teilweise überarbeitet (Capability-Claim entfällt)
- [ADR-0012](0012-multi-account-mapping.md) — Konto-Isolation, unberührt
- [ADR-0013](0013-prompt-injection-schutz.md) — Einrahmung fremdbestimmter Inhalte, unberührt
- [ADR-0015](0015-gangway-als-unterbau.md) — `access.Decider`/`AttachMCPSelector`-Mechanik, auf der diese Entscheidung aufbaut
- `internal/tools/read.go`, `internal/server/server.go` — `RegisterAll`, `access.AllowAll()`
