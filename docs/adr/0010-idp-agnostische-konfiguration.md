# ADR-0010: IdP-agnostische Konfiguration über drei orthogonale Achsen

**Status:** accepted
**Datum:** 2026-08-06
**Ersetzt:** —
**Ersetzt durch:** —
**Überarbeitet:** —
**Überarbeitet durch:** [ADR-0016](0016-anbieter-namensraeume-statt-roher-oidc-parameter.md) — überarbeitet Punkt 2: anbieterspezifisch ist ab dort die Konfigurations-Oberfläche (`MCP_OIDC_PROVIDER` mit eigenem Variablen-Namensraum je Anbieter), die Token-Prüfung bleibt anbieterneutral. Alle übrigen Punkte dieses ADR gelten unverändert. [ADR-0018](0018-werkzeug-freigabe-und-client-steuerung.md) — überarbeitet Punkt 3: `MCP_OIDC_CAPABILITY_CLAIM` entfällt (Funktionsumfang wird nicht mehr über Claims/Capability-Gruppen gesteuert).
**Verwandt:** [ADR-0009](0009-resource-server-statt-eigener-authorization-server.md), [ADR-0012](0012-multi-account-mapping.md), [ADR-0015](0015-gangway-als-unterbau.md), [ADR-0014](0014-self-service-onboarding.md)

## Kontext

Der unmittelbare Anlass für diesen Server ist ein konkretes Setup: mehrere Benutzer, jeder mit eigenem Fileee-Konto, Anmeldung über eine selbst betriebene Authentik-Instanz, Secrets aus Infisical, öffentlich erreichbar über einen Reverse Proxy.

Das Repo ist aber öffentlich, und der überwiegende Teil möglicher Nutzer hat davon nichts: ein Fileee-Konto, keinen Identity Provider, kein Secret-Management. Würde der Server diese Umgebung voraussetzen, wäre er für alle anderen unbrauchbar — und die Schwester-Repos [`go-fileee`](https://github.com/strausmann/go-fileee) und [`fileee-server`](https://github.com/strausmann/fileee-server) sind bewusst allgemein gehalten.

Gleichzeitig darf daraus kein Wildwuchs an Sonderfällen werden. Jede zusätzliche Betriebsart, die sich nicht sauber von den anderen trennen lässt, vervielfacht die Testmatrix.

## Entscheidung

1. **Drei orthogonale Achsen**, unabhängig voneinander schaltbar:

   | Achse | Variable | Werte |
   |---|---|---|
   | Authentifizierung | `MCP_AUTH_MODE` | `oidc` \| `token` \| `both` |
   | Konto-Auflösung | `FILEEE_MODE` | `single` \| `multi` |
   | Secret-Herkunft | `SECRET_BACKEND` | `env` \| `infisical` |

   Die Defaults ergeben den einfachsten Fall: `token` + `single` + `env` — drei Pflichtvariablen, kein IdP, kein Reverse Proxy, keine Fremdsysteme.

2. **Kein IdP-spezifischer Code.** Die Anbindung besteht ausschließlich aus generischer OIDC-Discovery über `MCP_OIDC_ISSUER`, JWKS-Prüfung und Claim-Auswertung. Authentik, Entra ID, Keycloak, Auth0 und Google sind Deployment-Entscheidungen, keine Code-Pfade.

3. **Claims sind konfigurierbar, nicht verdrahtet.** `MCP_OIDC_SUBJECT_CLAIM` (Default `sub`) und `MCP_OIDC_CAPABILITY_CLAIM` bestimmen, welche Claims ausgewertet werden. Grund: die stabilen Identifier unterscheiden sich je IdP — bei Entra ist `sub` anwendungsbezogen pseudonymisiert, tenant-weit stabil ist `oid`.

4. **Betriebliches Wissen gehört in die Dokumentation, nicht in den Code.** Je IdP eine eigene Anleitung unter `docs/idp/`, vollständig mit Platzhaltern, damit sie im öffentlichen Repo liegen kann und in beliebigen Umgebungen taugt.

5. **`token` bleibt als vollwertiger Modus erhalten**, nicht als Notbehelf. Er bedient den lokalen Einsatz in Claude Code, Automatisierung im eigenen Netz und jeden, der keinen IdP betreiben will.

## Konsequenzen

**Positiv**

- Dasselbe Image bedient „eine Person, ein Fileee-Konto, kein IdP" und das Mehrbenutzer-Setup hinter Authentik. Kein Fork, kein Build-Flag.
- Der Wechsel des Identity Providers ist eine Konfigurationsänderung. Dass das trägt, ist belegt: die Entra-Anleitung entstand vollständig, ohne eine Zeile Code anzufassen.
- Der `token`-Modus macht den Server testbar, bevor irgendein IdP existiert — genau das hat in der Umsetzung die Reihenfolge der Schritte bestimmt.

**Negativ**

- Die Testmatrix ist größer. Abgefedert wird das dadurch, dass die Achsen wirklich unabhängig sind: die Konto-Auflösung interessiert sich nicht dafür, woher die Credentials kommen.
- Eine Kombination ist verboten und muss beim Start abbrechen: `MCP_AUTH_MODE=token` zusammen mit `FILEEE_MODE=multi`. Ein statisches Token trägt kein Subject, es gibt also nichts aufzulösen. Bei `both` gilt: JWT-Pfad zuerst, Token-Pfad nur im `single`-Modus.
- Mehr Konfigurationsoberfläche heißt mehr Möglichkeiten, sie falsch zu setzen. Gegenmaßnahme ist eine ausnahmslose Fail-fast-Validierung beim Start, mit einem eigenen Test pro Regel.

## Referenzen

- `README.md` — die drei Betriebsarten mit vollständigen Beispielkonfigurationen
- `docs/idp/authentik.md`, `docs/idp/entra-id.md`, `docs/idp/claude-connector.md`
- [fileee-server](https://github.com/strausmann/fileee-server) — der Infisical-Dual-Mode wird von dort unverändert übernommen

## Nachtrag (2026-08-23): Punkt 3 — `MCP_OIDC_CAPABILITY_CLAIM` entfällt

Punkt 3 oben nennt `MCP_OIDC_CAPABILITY_CLAIM` neben `MCP_OIDC_SUBJECT_CLAIM` als konfigurierbaren
Claim. Diese Angabe ist überholt: [ADR-0018](0018-werkzeug-freigabe-und-client-steuerung.md) hebt die
serverseitige Capability-Gating-Entscheidung aus [ADR-0011](0011-capability-gating.md) vollständig
auf — der Server registriert seither alle Werkzeuge für jeden authentifizierten Aufrufer, die
Freigabe je Werkzeug trifft der Client. Damit gibt es keinen Claim mehr, der einen Funktionsumfang
steuern könnte; `MCP_OIDC_CAPABILITY_CLAIM` wurde aus der Konfiguration entfernt.

`MCP_OIDC_SUBJECT_CLAIM` bleibt von diesem Nachtrag unberührt — Punkt 3 gilt für den
Subject-Claim unverändert, nur der zweite dort genannte Claim ist entfallen.

Die ursprüngliche Fassung von Punkt 3 bleibt oben unverändert stehen, als Protokoll dessen, wie die
Konfiguration ursprünglich beschrieben war.
