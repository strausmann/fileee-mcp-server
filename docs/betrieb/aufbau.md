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
dafür eine eigene Maschinen-Identität.

### Einstiegspunkt: Environment und Ordnerpfad

Der gebackene Einstiegspunkt kennt weder `--env` noch `--path` — er läuft
mit den Vorgaben `env=dev`/`path=/`. Das GitOps-Repo
(`infrastructure/docker/fileee-mcp-server`) überschreibt den Einstiegspunkt
deshalb per Compose auf `infisical run --env=dev --path=/authentik --
/usr/local/bin/fileee-mcp-server` (für die Entra-ID-Instanz entsprechend
`--path=/entra-id`). Die Machine-Identity-Umgebungsvariablen
(`INFISICAL_UNIVERSAL_AUTH_CLIENT_ID`/`_SECRET`) reichen dafür aus — sobald
sie gesetzt sind, authentifiziert sich `infisical run` selbstständig gegen
die Instanz und injiziert die Geheimnisse aus dem angegebenen Ordner.

### Eine Identität für beide Instanzen — bewusst ohne Trennung

Es gibt zwei Container-Instanzen dieses Dienstes — eine hinter Authentik
(`fileee-mcp.strausmann.cloud`), eine hinter Entra ID
(`fileee-mcp-entra.strausmann.cloud`). Beide teilen sich **eine einzige**
Maschinen-Identität im Infisical-Projekt `fileee-mcp-server`
(`e626aa2a-c8e5-4cd5-8b81-b90c979edf30`):

| Identität | Infisical-Identity-ID | Projekt-Rolle | Zuschnitt |
|---|---|---|---|
| `fileee-mcp-server` | `990ecb65-e971-429e-a829-55569603b9c2` | `viewer` (eingebaute Rolle) | **ganzes Projekt** — alle Environments, alle Ordner |

**Das heißt konkret: beide Instanzen lesen dieselben Geheimnisse.** Es
gibt **keine** Trennung auf Ordner- oder Environment-Ebene — ein
kompromittierter Authentik-Container käme technisch auch an die
Entra-ID-Geheimnisse (und umgekehrt), obwohl er sie nicht braucht. Das
war zuerst anders geplant: Ein früherer Anlauf hatte zwei Identitäten mit
Org-Rolle `no-access` und je einem auf Ordner (`/authentik` bzw.
`/entra-id`) und Environment (`dev`) beschränkten Zusatzprivileg angelegt
— strenger, aber **abweichend vom Rest des Hauses**.

**Warum die Trennung wieder aufgegeben wurde:** Der tatsächliche Bestand
an Consumer-Identitäten (`run-stalwart` im Stalwart-Projekt,
`fileee-server` im gleichnamigen Projekt, `GitHub - Fileee Server
Pipeline` im fileee-server-ci-Projekt) vergibt durchgängig **eine
Identität je Projekt** mit Lesezugriff aufs **ganze** Projekt — über eine
eingebaute Rolle wie `viewer`, nicht über ein auf Ordner/Environment
gescoptes Zusatzprivileg. Ein solches gescoptes Privileg existiert im
gesamten geprüften Bestand **nirgends** tatsächlich im Einsatz, auch nicht
dort, wo ein früherer Entwurf es als Ziel vorsah (Ordner-Scoping wird bei
weiterem Feinschliff-Bedarf separat entschieden — nicht implizit über
diesen Dienst eingeführt). Der Betreiber hat entschieden, diesem
gelebten Muster zu folgen: „viewer pro Projekt reicht mir aktuell aus."
Mit nur einer Identität war die vormalige Ordner-Trennung ohnehin nur
noch ein operativer Vorteil (eine Instanz sperren, ohne die andere zu
treffen), kein tatsächlicher Datenschutz — beide kamen nach dem ersten
Umbau (Rollenwechsel von `no-access`+Zusatzprivileg auf `viewer`)
bereits an dieselben Daten. Details: `.claude/skills/infisical/references/infisical-struktur.md`
im `homelab-management`-Repo, Abschnitt „Gelebte Praxis der
Consumer-Identitäten".

### Startgeheimnis

Die Identität authentifiziert sich über Universal-Auth
(Client-ID + Client-Secret). Diese beiden Werte sind das
„Startgeheimnis", das der Container braucht, um überhaupt bei Infisical
anzuklopfen — **dasselbe** Startgeheimnis für beide Instanzen.

Universal-Auth ist bereits angehängt, die Client-ID existiert also schon
(unkritisch, kein Geheimnis): `19af5f6f-3c18-4b9c-94d6-0d9b443f0181`.

**Das Client-Secret selbst fehlt noch.** Es konnte nicht automatisiert
erzeugt werden: Die für die Anlage genutzte Automations-Identität hat auf
Organisationsebene bewusst nur die Rolle `member` (Least-Privilege-
Entscheidung aus einer früheren Aufgabe) — und `member` fehlt laut
Infisical-Rechte-Schema ausgerechnet die Aktion `identity:create-token`,
die zum Erzeugen eines Client-Secrets für eine andere Identität nötig
ist. Eine kurzzeitige Rechte-Anhebung dieser geteilten Identität wurde
bewusst **nicht** automatisiert durchgeführt, weil sie für die Dauer des
Vorgangs alle Prozesse betrifft, die dieselbe Identität nutzen — das ist
keine Ausführungsdetail-Entscheidung. Details:
`.claude/skills/infisical/references/troubleshooting.md` im
`homelab-management`-Repo, Abschnitt „Org-Rolle `member` kann
Machine-Identities anlegen …".

Bis dahin liegt ein vorbereiteter Vaultwarden-Eintrag (Organisation
`homelab-automation`, Sammlung `Automation/Claude-Team`) mit Client-ID
und Host bereits ausgefüllt und dem Passwort-Feld auf dem Platzhalter
`CHANGE_ME`: **`Infisical Machine Identity - fileee-mcp-server`**.

Sobald ein Org-Admin-Zugang das Client-Secret im Infisical-Web-UI erzeugt
(Identities → `fileee-mcp-server` → Universal Auth → Create Client
Secret), ersetzt dieser Wert den Platzhalter im Vaultwarden-Eintrag. Von
dort wird derselbe Wert anschließend als **Dockhand-Stack-Geheimnis** bei
**beiden** Ziel-Stacks (Authentik- und Entra-ID-Instanz) hinterlegt
(`INFISICAL_UNIVERSAL_AUTH_CLIENT_ID`,
`INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET`) — nicht im GitOps-Repo.

## Manueller Nachbau (Backfill)

Für den Fall, dass ein bereits getaggter Stand nachträglich gebaut werden
muss (z. B. weil der tägliche Ablauf zwischenzeitlich übersprungen hat),
existiert `.github/workflows/publish-image.yml` als manuell auslösbarer
Ablauf (`workflow_dispatch`) mit dem Git-Tag als Eingabe.
