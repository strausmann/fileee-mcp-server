# Betrieb: Aufbau und Auslieferung

Diese Seite beschreibt, wie aus dem Quellcode ein lauffähiges Container-Abbild
entsteht und wie geprüft wird, dass es tatsächlich läuft.

## Abbild bauen

Das Abbild entsteht mehrstufig aus `deploy/Dockerfile`:

1. **Bau-Stufe** auf einem Go-Abbild. Das Binär wird statisch gebaut
   (`CGO_ENABLED=0`), damit es in der Laufzeit-Stufe ohne libc-Abhängigkeiten
   läuft.
2. **Laufzeit-Stufe mit Shell** (bewusst nicht distroless): Der Server hat —
   anders als der Geschwister-Dienst `fileee-server` — **kein eingebautes
   Infisical-Backend**. Die Geheimnis-Zustellung läuft deshalb über das
   externe Infisical-Werkzeug, das im Abbild mitgeführt und im
   Einstiegspunkt vorgeschaltet wird: `infisical run -- /fileee-mcp-server`.
3. Der Container läuft als **nicht-root**-Benutzer.

### Etikett (Tag)

Der veröffentlichende Ablauf (`.github/workflows/release.yml`, Job `docker`)
berechnet die Tags aus der von `semantic-release` ermittelten Version nach
dem Schema `<major>`, `<major>.<minor>`, `<major>.<minor>.<patch>` und
`latest`, jeweils für `ghcr.io/strausmann/fileee-mcp-server` und
`strausmann/fileee-mcp-server` (Docker Hub). Ein lokaler Bau ohne
`--build-arg VERSION=…` bleibt beim Vorgabewert `dev`.

### Bauen

```bash
docker build -f deploy/Dockerfile -t fileee-mcp-server:pruef .
```

Mit expliziter Version (wie im CI-Ablauf):

```bash
docker build -f deploy/Dockerfile \
  --build-arg VERSION=0.1.0 \
  --build-arg REVISION="$(git rev-parse HEAD)" \
  --build-arg CREATED="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t fileee-mcp-server:0.1.0 .
```

### Prüfen, dass es läuft

Das Abbild bringt einen `healthcheck`-Unterbefehl mit, der eine URL
abruft (Vorgabe `http://127.0.0.1:8080/healthz`, überschreibbar über
`MCP_HEALTHCHECK_URL`) und den Statuscode in einen Exit-Code übersetzt
(0 = erreichbar). Dieser Unterbefehl treibt auch die
`HEALTHCHECK`-Anweisung im Dockerfile — das Abbild hat kein `curl`/`wget`,
die Lebendprüfung übernimmt das Programm selbst. Bewusst **ohne**
`config.LoadConfig`: eine fehlende Pflichtvariable (z. B. `MCP_API_TOKEN`)
darf die Lebendprüfung nicht scheitern lassen, solange der Server bereits
läuft.

**Wichtiger Unterschied zwischen `docker run --rm … --version` und der
Produktivnutzung:** Der Einstiegspunkt des Abbilds ist
`infisical run -- /usr/local/bin/fileee-mcp-server` — Infisical
umschliesst **jeden** Aufruf, auch einen trivialen wie `--version`.
Ohne gültige Infisical-Zugangsdaten (`.infisical.json`, `--token` oder
Machine-Identity-Umgebungsvariablen) verweigert `infisical run` bereits
den eigenen Start und erreicht den Server-Prozess gar nicht — das ist das
erwartete, gewollte Verhalten des Einstiegspunkts (Geheimnisse müssen da
sein, bevor irgendetwas läuft), aber es bedeutet: ein blankes
`docker run --rm fileee-mcp-server:pruef --version` ohne Infisical-Kontext
schlägt **immer** fehl, unabhängig davon, ob das Abbild korrekt gebaut
wurde.

Für einen lokalen Rauchtest ohne Infisical-Zugangsdaten deshalb den
Einstiegspunkt gezielt umgehen:

```bash
# Infisical-Werkzeug liegt tatsächlich im Abbild und ist lauffähig
# (bewusst NICHT über den Standard-Einstiegspunkt — der würde ohne
# Zugangsdaten sofort verweigern):
docker run --rm --entrypoint /usr/local/bin/infisical fileee-mcp-server:pruef --version

# Server-Binary antwortet direkt (Unterbefehl ist "version", ohne
# Bindestriche — "--version" wird NICHT erkannt und fällt auf die
# normale Konfigurationsprüfung durch):
docker run --rm --entrypoint /usr/local/bin/fileee-mcp-server fileee-mcp-server:pruef version

# healthcheck-Unterbefehl gegen einen beliebigen HTTP-Endpunkt, auch ohne
# laufenden fileee-mcp-server (belegt: der Unterbefehl funktioniert im
# gebauten Abbild, nicht nur im go-test):
docker run --rm --network host --entrypoint /usr/local/bin/fileee-mcp-server \
  -e MCP_HEALTHCHECK_URL=http://127.0.0.1:<port-eines-erreichbaren-dienstes>/ \
  fileee-mcp-server:pruef healthcheck
```

Ein vollständiger Produktivstart braucht zusätzlich `MCP_AUTH_MODE=oidc`
mit einem erreichbaren Identity Provider — der Modus `token` ist von
diesem Server heute (noch) nicht unterstützt (Gangway v0.2.0 baut intern
immer einen OIDC-Verifier auf, siehe ADR-0015-Nachtrag). Ein Rauchtest mit
`MCP_AUTH_MODE=token` startet deshalb **nicht** — das ist keine
Eigenschaft des Abbilds, sondern der aktuelle Funktionsstand des Servers.

Mit vollständiger, gültiger Konfiguration (Infisical-Zugangsdaten +
funktionierender OIDC-Issuer) meldet `docker inspect` nach Ablauf der
Anlaufzeit `healthy`:

```bash
docker inspect --format '{{.State.Health.Status}}' <container-name>
```

## Ausrollen

Der veröffentlichende Ablauf pusht das Abbild automatisch nach GHCR und
Docker Hub, sobald `semantic-release` eine neue Version erzeugt hat (täglich,
kein `on: push`). Das Ausrollen auf dem Ziel-Knoten selbst ist nicht
Gegenstand dieser Seite — siehe das GitOps-Repo
`infrastructure/docker/fileee-mcp-server` auf `git.strausmann.de`.

## Geheimnis-Zustellung

Der Server holt seine Geheimnisse nicht selbst aus Infisical (kein
eingebautes Backend, siehe oben) — das übernimmt der Einstiegspunkt
`infisical run -- /usr/local/bin/fileee-mcp-server`. Das Werkzeug braucht
dafür eine eigene Maschinen-Identität mit **möglichst wenig** Rechten.

### Zwei Identitäten, nicht eine

Es gibt zwei Container-Instanzen dieses Dienstes — eine hinter Authentik
(`fileee-mcp.strausmann.cloud`), eine hinter Entra ID
(`fileee-mcp-entra.strausmann.cloud`). Jede hat eine **eigene**
Maschinen-Identität in Infisical, angelegt im Projekt `fileee-mcp-server`
(`e626aa2a-c8e5-4cd5-8b81-b90c979edf30`):

| Identität | Infisical-Identity-ID | Ordner | Umgebung | Zusatzprivileg |
|---|---|---|---|---|
| `fileee-mcp-server-authentik` | `6ad0a0ea-de78-4e03-abcb-0c21bbf19bbd` | `/authentik` | `dev` | `authentik-dev-readonly` |
| `fileee-mcp-server-entra-id` | `7a1ad0ad-1bd1-4204-8df5-5b5921dbed05` | `/entra-id` | `dev` | `entra-id-dev-readonly` |

Eine gemeinsame Identität für beide Ordner würde die Trennung der Ordner
zur reinen Zierde machen — ein kompromittierter Authentik-Container könnte
dann auch die Entra-ID-Geheimnisse mitlesen. Deshalb zwei Identitäten
statt einer.

Jede Identität hat:

- **keine** Rechte auf Organisationsebene (Org-Rolle `no-access`),
- **keine** eigenen Rechte über die Projekt-Mitgliedschaft
  (Projekt-Rolle `no-access`),
- ihre einzigen tatsächlichen Rechte über genau **ein** Zusatzprivileg,
  beschränkt auf `environment=dev` und den jeweils eigenen Ordner, mit
  den Aktionen `describeSecret` und `readValue` — lesend, ohne Schreib-
  oder Löschrecht. (Die ältere Aktion `read` wurde bewusst weggelassen:
  Infisical lässt sie nicht zusammen mit den beiden granularen
  Nachfolge-Aktionen zu, siehe Fehlermeldung *„The Read permission is a
  legacy action which has been replaced by Describe Secret and Read
  Value"*.)

Geprüft (Gegenprobe über die Rechte-Konfiguration, siehe unten) statt
über einen echten Anmeldeversuch, weil das erste Zugangsmerkmal beim
Schreiben dieses Abschnitts noch fehlte (nächster Absatz): Jede Identität
hat **genau ein** Zusatzprivileg — für den eigenen Ordner. Es existiert
**kein** Privileg, das den jeweils anderen Ordner nennt, und die
Basisrolle (Projekt wie Organisation) ist `no-access`, also strukturell
ohne jede Berechtigung. Da Infisical Rechte ausschließlich additiv über
explizit vergebene Privilegien gewährt, kann `fileee-mcp-server-authentik`
dadurch **nicht** auf `/entra-id` lesen, und umgekehrt.

### Startgeheimnisse

Jede Identität authentifiziert sich über Universal-Auth
(Client-ID + Client-Secret). Diese beiden Werte sind die
„Startgeheimnisse", die der Container braucht, um überhaupt bei Infisical
anzuklopfen.

Der Universal-Auth-Mechanismus ist bei beiden Identitäten bereits
angehängt, die Client-ID existiert also schon (unkritisch, kein
Geheimnis):

| Identität | Client-ID |
|---|---|
| `fileee-mcp-server-authentik` | `a81e7af5-bc07-4589-858e-93fb9905411e` |
| `fileee-mcp-server-entra-id` | `7777c8e1-2f23-48d2-ad76-7c979803b82d` |

**Abweichung vom ursprünglichen Plan:** Das eigentliche Client-Secret
konnte nicht automatisiert erzeugt werden. Die dafür genutzte
Automations-Identität hat auf Organisationsebene bewusst nur die Rolle
`member` (Least-Privilege-Entscheidung aus einer früheren Aufgabe) — und
`member` fehlt laut Infisical-Rechte-Schema ausgerechnet die Aktion
`identity:create-token`, die zum Erzeugen eines Client-Secrets für eine
andere Identität nötig ist. Eine kurzzeitige Rechte-Anhebung dieser
geteilten Identität wurde bewusst **nicht** automatisiert durchgeführt,
weil sie für die Dauer des Vorgangs alle Prozesse betrifft, die dieselbe
Identität nutzen — das ist keine Ausführungsdetail-Entscheidung. Details
und Hintergrund: `.claude/skills/infisical/references/troubleshooting.md`
im `homelab-management`-Repo, Abschnitt „Org-Rolle `member` kann
Machine-Identities anlegen …".

Bis zur Klärung liegt je Identität ein vorbereiteter Vaultwarden-Eintrag
(Organisation `homelab-automation`, Sammlung `Automation/Claude-Team`)
mit Client-ID und Host bereits ausgefüllt und dem Passwort-Feld auf dem
Platzhalter `CHANGE_ME`:

- `Infisical Machine Identity - fileee-mcp-server-authentik`
- `Infisical Machine Identity - fileee-mcp-server-entra-id`

Sobald ein Org-Admin-Zugang das jeweilige Client-Secret im
Infisical-Web-UI erzeugt (Identities → Name → Universal Auth → Create
Client Secret), ersetzt dieser Wert den Platzhalter im zugehörigen
Vaultwarden-Eintrag. Von dort werden beide Werte anschließend als
**Dockhand-Stack-Geheimnisse** beim jeweiligen Ziel-Stack hinterlegt
(`INFISICAL_UNIVERSAL_AUTH_CLIENT_ID`,
`INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET`) — nicht im GitOps-Repo.

## Manueller Nachbau (Backfill)

Für den Fall, dass ein bereits getaggter Stand nachträglich gebaut werden
muss (z. B. weil der tägliche Ablauf zwischenzeitlich übersprungen hat),
existiert `.github/workflows/publish-image.yml` als manuell auslösbarer
Ablauf (`workflow_dispatch`) mit dem Git-Tag als Eingabe.
