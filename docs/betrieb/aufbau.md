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
deshalb per Compose auf einen eigenen `--env=dev --path=/authentik`
(für die Entra-ID-Instanz entsprechend `--path=/entra-id`).

**Korrektur nach Prüfung — `infisical run` authentifiziert sich NICHT von
selbst über die Machine-Identity-Umgebungsvariablen.** Die ursprüngliche
Annahme oben war falsch. Live gegen das gebaute Abbild geprüft (mit
absichtlich ungültigen Zugangsdaten, um nur den *Mechanismus* zu sehen,
nicht ein Geheimnis zu erraten):

```bash
docker run --rm \
  -e INFISICAL_UNIVERSAL_AUTH_CLIENT_ID=… -e INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET=… \
  -e INFISICAL_API_URL=https://secretsmanager.strausmann.cloud/api \
  --entrypoint /usr/local/bin/infisical fileee-mcp-server:pruef \
  run --env=dev --path=/authentik --projectId=<projekt-id> -- /bin/true
```

Ergebnis: `No valid login session found, triggering login flow` — die CLI
versucht einen **interaktiven Browser-/E-Mail-Login**, ignoriert die
gesetzten Umgebungsvariablen vollständig und scheitert danach zusätzlich
am fehlenden Schreibzugriff auf `$HOME/.infisical` (der Container hat kein
Zuhause-Verzeichnis für den `nonroot`-Benutzer). Die offizielle Dokumentation
(<https://infisical.com/docs/cli/commands/run>, Abschnitt „Environment
variables“) bestätigt das: `infisical run` kennt nur **`INFISICAL_TOKEN`**
(oder `--token`) als Zugangsdaten — keine direkte
Universal-Auth-Unterstützung. Der dort dokumentierte Weg:

```bash
export INFISICAL_TOKEN=$(infisical login --method=universal-auth \
  --client-id=<identity-client-id> --client-secret=<identity-client-secret> \
  --silent --plain)
```

`--plain` gibt bei Erfolg **ausschließlich** den Token aus (leer bei
Fehlschlag, geprüft mit denselben ungültigen Zugangsdaten — Antwort blieb
auf stdout leer, die Fehlermeldung lief über stderr). Damit lässt sich der
Token in eine Shell-Variable einlesen, ohne ihn je auszugeben (siehe
`.claude/rules/secret-safe-config-inspection.md` im
`homelab-management`-Repo, Abschnitt „CLI: `infisical login` druckt …“).

Der tatsächliche Einstiegspunkt im GitOps-Repo ist deshalb ein kleiner
Shell-Wrapper (`entrypoint: [sh, -c, …]`), der zuerst den Login-Schritt
ausführt und den Token nur intern weiterreicht:

```sh
TOKEN="$(/usr/local/bin/infisical login \
  --method=universal-auth \
  --client-id="$INFISICAL_UNIVERSAL_AUTH_CLIENT_ID" \
  --client-secret="$INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET" \
  --domain="$INFISICAL_API_URL" \
  --silent --plain)"
export INFISICAL_TOKEN="$TOKEN"
exec /usr/local/bin/infisical run \
  --domain="$INFISICAL_API_URL" --projectId="$INFISICAL_PROJECT_ID" \
  --env="$INFISICAL_ENV" --path="$INFISICAL_SECRET_PATH" \
  -- /usr/local/bin/fileee-mcp-server
```

Die vollständige, kommentierte Fassung steht im GitOps-Repo
`infrastructure/docker/fileee-mcp-server/compose.yaml` (Anker
`x-fileee-mcp-entrypoint`).

**Offen, weil ohne echtes Client-Secret nicht abschließend prüfbar
(Aufgabe 4 übernimmt das):** Ob `infisical login --silent --plain` auch
bei **erfolgreicher** Anmeldung ohne Schreibzugriff auf ein
`$HOME`-Verzeichnis auskommt, ist mit den zwangsläufig ungültigen
Testzugangsdaten dieser Aufgabe nicht geprüft worden — der Login schlug
in jedem Testlauf schon am `401` der API fehl, bevor ein etwaiger
Session-Schreibversuch überhaupt drankäme. Aus dem Skill-Troubleshooting
(`.claude/skills/infisical/references/troubleshooting.md` im
`homelab-management`-Repo, „Ohne OS-Keyring … gibt den JWT auf stdout
aus“) ist bekannt, dass die CLI in einer keyring-losen Umgebung wie
diesem Debian-Abbild auf den reinen Stdout-Druck zurückfällt, statt zu
scheitern — das spricht dafür, dass kein extra `$HOME` nötig ist, ist
aber keine Live-Bestätigung für *dieses* Abbild. Der
Sitzungsspeicher-Bind-Mount (`/docker/stacks/fileee-mcp-server/authentik`
→ `/home/nonroot/sessions`, siehe GitOps-README) deckt den `nonroot`-
Benutzer für den Fall ab, dass doch ein Schreibzugriff nötig wird — sollte
Aufgabe 4 trotzdem einen Fehler in diesem Schritt sehen, ist das der
erste Verdacht.

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

## Mehrere Instanzen unterscheiden

Laufen mehrere Instanzen dieses Servers gegen verschiedene Fileee-Konten und sind beide
gleichzeitig in einem Client eingebunden, braucht das Modell einen Anhaltspunkt, welche es gerade
benutzt.

Der **Name des Connectors** ist der stärkste Unterscheider — er landet im Präfix jedes
Werkzeugaufrufs. Er wird aber im Client vergeben und ist vom Server aus nicht beeinflussbar: Der
Server meldet sich als `fileee-mcp-server`, die Oberfläche zeigt den Namen, den der Betreiber dort
eingetragen hat. Ein Versuch, den Namen über eine Umgebungsvariable zu steuern, ginge ins Leere.

Was der Server beitragen kann, ist **`MCP_INSTANCE_DESCRIPTION`** (optional, höchstens 2000
Zeichen). Der Wert erscheint im `instructions`-Feld der `initialize`-Antwort und als
`instanceDescription` in `whoami`.

Zwei Sätze haben sich bewährt — **wer**, und **wie zu behandeln**:

    Testumgebung. Wegwerfdaten, hier darf experimentiert werden.
    Nicht das produktive Archiv.

    Produktives Fileee-Archiv. Echte Rechnungen und Verträge. Schreibende
    und teilende Aufrufe nur auf ausdrückliche Anweisung.

Der zweite Satz ist der wertvolle. „Wer" allein sagt schon der Connector-Name.

**Das ist ein Wegweiser, keine Schranke.** Der Text hilft bei der Auswahl und verhindert nichts,
wenn das Modell danebengreift. Durchgesetzt wird die Trennung an anderer Stelle: durch getrennte
Fileee-Zugangsdaten je Instanz, durch die client-seitige Werkzeug-Freischaltung (Always allow /
Needs approval / Blocked, siehe [ADR-0018](../adr/0018-werkzeug-freigabe-und-client-steuerung.md))
und durch die ID-Whitelist ([ADR-0019](../adr/0019-id-whitelist-gilt-auch-fuer-share.md)).

Ist die Variable nicht gesetzt, fehlen beide Felder — der Server verhält sich wie zuvor.
