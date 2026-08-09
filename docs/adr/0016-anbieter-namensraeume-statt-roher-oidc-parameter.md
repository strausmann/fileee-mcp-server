# ADR-0016: Ein Variablen-Namensraum je Identity Provider statt roher OIDC-Parameter

**Status:** accepted
**Datum:** 2026-08-09
**Ersetzt:** —
**Ersetzt durch:** —
**Überarbeitet:** [ADR-0010](0010-idp-agnostische-konfiguration.md) (Punkt 2 „Kein IdP-spezifischer Code")
**Überarbeitet durch:** —
**Verwandt:** [ADR-0009](0009-resource-server-statt-eigener-authorization-server.md), [ADR-0015](0015-gangway-als-unterbau.md)

## Kontext

ADR-0010 legte fest, dass die IdP-Anbindung ausschließlich aus generischer Discovery über `MCP_OIDC_ISSUER` besteht — Authentik, Entra ID und Keycloak seien Deployment-Entscheidungen, keine Code-Pfade. Das hat für den Code gestimmt und tut es weiter: Die Prüfung eines Tokens ist überall dieselbe.

Für den **Betreiber** stimmte es nicht. Er hat drei Dinge in der Hand, die ihm sein Anbieter zeigt — bei Entra Verzeichnis-ID, Anwendungs-ID und Anwendungs-Geheimnis — und musste daraus selbst Protokollbegriffe bauen: eine Aussteller-URL zusammensetzen und begreifen, dass „Audience" hier die Anwendungs-ID meint. Beides ist Übersetzungsarbeit, die der Server ihm abnehmen kann.

Drei Beobachtungen gaben den Ausschlag:

1. **Der Name verfehlte die Sache.** `MCP_OIDC_AUDIENCE` erwartet die Client-ID. Niemand liest das aus dem Namen heraus.
2. **Die Aussteller-URL ist ableitbar.** Aus der Verzeichnis-ID folgt sie bei Entra vollständig, aus Host und Anwendungs-Kürzel bei Authentik ebenso.
3. **Falsche Eingaben scheiterten erst zur Laufzeit.** Wer bei Entra eine verifizierte Domain oder `common` einträgt, bekommt vom Discovery-Dokument eine Antwort — aber einen Aussteller, der nie zum Token passt (`common` liefert die Vorlage `{tenantid}`, eine Domain liefert die GUID zurück). Das Symptom war eine 401-Schleife, die im Client nur „Authorization failed" heißt.

Dazu kam die Gefahr des Vermischens: Solange alle Anbieter dieselben Variablen benutzen, liest jemand die Entra-Anleitung und sieht Werte, die für Authentik gedacht sind — und umgekehrt.

## Entscheidung

1. **`MCP_OIDC_PROVIDER` wählt den Anbieter**, Werte `entra` | `authentik` | `generic`. Ohne diese Angabe startet der Server im OIDC-Modus nicht.

2. **Jeder Anbieter hat einen eigenen Variablen-Namensraum.** Kein Name wird geteilt:

   | `entra` | `authentik` | `generic` |
   |---|---|---|
   | `MCP_ENTRA_TENANT_ID` | `MCP_AUTHENTIK_BASE_URL` | `MCP_OIDC_ISSUER` |
   | `MCP_ENTRA_CLIENT_ID` | `MCP_AUTHENTIK_APP_SLUG` | `MCP_OIDC_CLIENT_ID` |
   | | `MCP_AUTHENTIK_CLIENT_ID` | |

   Eine Anleitung nennt damit ausschließlich die Variablen ihres eigenen Anbieters. Ein Leser begegnet nie einem Wert, der ihn nichts angeht.

3. **`generic` ist gleichrangig, kein Notausgang.** Es bedient jeden standardkonformen OpenID-Connect-Anbieter ohne eigenen Zweig — GitLab, Keycloak, Auth0, Google. Es ist ausdrücklich **nicht** als Ausweichweg für Sonderfälle von `entra` oder `authentik` gedacht: Wer Entra nutzt, wählt `entra` und bekommt dessen Prüfungen.

4. **Die Aussteller-URL wird abgeleitet, nicht eingetragen** — bei `entra` aus der Verzeichnis-ID, bei `authentik` aus Host und Kürzel. Nur `generic` nimmt sie direkt entgegen, weil es dort nichts abzuleiten gibt.

5. **Anbieterfremde Variablen sind ein Startfehler**, kein stilles Ignorieren. Wer bei `MCP_OIDC_PROVIDER=entra` noch `MCP_AUTHENTIK_APP_SLUG` gesetzt hat, bekommt eine Meldung mit dem Namen der überflüssigen Variablen. Ohne diese Prüfung sucht ein Betreiber den Fehler an einer Einstellung, die gar nicht gelesen wird.

6. **`MCP_ENTRA_TENANT_ID` akzeptiert nur die GUID.** Domain, `common` und `organizations` werden beim Start abgewiesen, mit Begründung im Fehlertext. Belegt am 09.08.2026 gegen die echten Discovery-Dokumente.

**Was von ADR-0010 unberührt bleibt:** die drei orthogonalen Achsen, die konfigurierbaren Claims, eine Anleitung je IdP, der `token`-Modus als vollwertige Betriebsart. Und der Kern von Punkt 2 gilt weiter — die **Token-Prüfung** ist anbieterneutral.

**Was sich ändert:** Anbieterspezifisch ist ab jetzt die **Konfigurations-Oberfläche**, nicht die Prüflogik. Der Unterschied lebt in `resolveEntra` / `resolveAuthentik` / `resolveGeneric`, die alle dasselbe Paar aus Aussteller und Client-ID erzeugen. Danach ist der Anbieter vergessen.

## Konsequenzen

**Positiv**

- Der Betreiber trägt ein, was ihm sein Anbieter zeigt. Keine Übersetzung, kein Zusammenbauen von URLs.
- Falsche Eingaben scheitern beim Start mit Namen und Begründung statt als 401-Schleife im Client.
- Die drei Anleitungen sind wirklich getrennt und für Fremde einzeln lesbar.
- Ein neuer Anbieter mit eigener Ableitung ist ein zusätzlicher Zweig plus Tabelleneintrag — die bestehenden bleiben unberührt.

**Negativ**

- Mehr Variablennamen insgesamt. Der Preis für die Trennung: Es gibt bewusst drei Client-ID-Variablen statt einer.
- Ein Anbieter mit eigenem Zweig braucht Pflege, wenn sich sein Adressschema ändert. Bei Entra und Authentik sind diese Schemata seit Jahren stabil, das Risiko ist gering.
- Bestehende Konfigurationen brechen: `MCP_OIDC_AUDIENCE` existiert nicht mehr, `MCP_OIDC_PROVIDER` ist neu und Pflicht. Bewusst ohne Übergangsfrist — der Server ist noch nirgends produktiv, und eine stille Rückwärtskompatibilität würde genau die Vermischung konservieren, die dieses ADR beendet.

## Referenzen

- `internal/config/config.go` — `resolveProvider` und die drei Anbieter-Zweige
- `internal/config/config_test.go` — `TestLoadConfigProviderEntra`, `…Authentik`, `…Selection`
- `docs/idp/entra-id.md`, `docs/idp/authentik.md`, `docs/idp/generic.md`
- [ADR-0010](0010-idp-agnostische-konfiguration.md) — die überarbeitete Entscheidung
