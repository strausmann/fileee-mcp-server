# ADR-0012: Konto-Auflösung über den signierten Claim des aktuellen Requests

**Status:** accepted
**Datum:** 2026-08-06
**Ersetzt:** —
**Ersetzt durch:** —
**Überarbeitet:** —
**Überarbeitet durch:** [ADR-0015](0015-gangway-als-unterbau.md)
**Verwandt:** [ADR-0009](0009-resource-server-statt-eigener-authorization-server.md), [ADR-0010](0010-idp-agnostische-konfiguration.md), [go-fileee ADR-0005](https://github.com/strausmann/go-fileee/blob/main/docs/adr/0005-schonender-betrieb-rate-limiting.md)

## Kontext

Mehrere Personen sollen denselben Server nutzen, jede mit ihrem eigenen Fileee-Konto. Der Server muss also je Anfrage entscheiden, mit welchen Credentials er gegen `my.fileee.com` geht.

Drei Aspekte machen das heikler, als es klingt:

**Die Identitätsquelle.** Ein vorgeschalteter Auth-Proxy könnte die Identität als HTTP-Header liefern — das wäre einfach, aber ein Header ist nicht signiert und die Vertrauenskette hängt an der Proxy-Konfiguration.

**Die SDK-Semantik.** Im Go-SDK v1.7.0 liefert `auth.TokenInfoFromContext(ctx)` innerhalb eines Tool-Handlers **nicht** das Token des aktuellen `tools/call`, sondern das des `initialize`-Requests: `connectStreamable` bindet den Connection-Kontext, und alle Handler-Kontexte leiten sich davon ab. In einer von Benutzer A eröffneten Session würde damit jeder spätere Request auf A's Fileee-Konto auflösen. Zusätzlich greift der eingebaute Session-Hijacking-Schutz des SDK nur, wenn `TokenInfo.UserID` gesetzt ist.

**Die Last gegen Fileee.** `go-fileee` ADR-0005 begründet den eingebauten Rate-Limiter damit, dass er im Transport sitzt und automatisch für alle Konsumenten gilt. Ein Pool aus N Clients mit je eigenem Limiter würde diese Zusicherung stillschweigend ver-N-fachen.

## Entscheidung

1. **Die Identität kommt aus dem signierten Token, nicht aus einem Header.** Ausgewertet wird der über `MCP_OIDC_SUBJECT_CLAIM` konfigurierte Claim (Default `sub`).

2. **Gelesen wird ausschließlich über `serve.IdentityFrom(ctx)`** — mit [ADR-0015](0015-gangway-als-unterbau.md) der von Gangway bereitgestellte Weg, niemals selbst zwischengespeichert. Der ursprünglich hier verlangte Zugriffspfad `req.GetExtra().TokenInfo` (direkt auf der SDK-eigenen `auth`-Middleware) entfällt, weil dieser Server nicht mehr selbst gegen `auth.RequireBearerToken` verifiziert — Gangways `AttachMCP` übernimmt die Verifikation vor dem MCP-Handler und erzwingt `Stateless = true`, wodurch die oben unter „Die SDK-Semantik" beschriebene Kontextbindung gar nicht erst entsteht. **Wer diesen Server ohne Gangway baut**, gilt die ursprüngliche Regel unverändert: Zugriff auf `auth.TokenInfoFromContext` innerhalb eines Tool-Handlers ist verboten, stattdessen `req.GetExtra().TokenInfo`, das pro POST neu gesetzt wird.

3. **Die Pflicht, `TokenInfo.UserID` zu setzen, betraf den SDK-eigenen Session-Hijacking-Schutz und entfällt mit Gangway ebenso.** Die Identität wird pro Request neu über `serve.IdentityFrom(ctx)` gelesen; durch die erzwungene Statelessness gibt es keine fortbestehende Sitzung, an der eine fremde `Mcp-Session-Id` weiterverwendet werden könnte. **Wer diesen Server ohne Gangway baut**, gilt weiterhin: Der `TokenVerifier` muss `TokenInfo.UserID` auf den Subject-Claim setzen. Bleibt das Feld leer, ist der Session-Hijacking-Schutz des SDK wirkungslos und jeder Inhaber irgendeines gültigen Tokens könnte eine fremde `Mcp-Session-Id` weiterverwenden. Ein leeres `UserID` ist dort ein Startup-Fehler.

4. **Ein Subject zeigt auf genau ein Konto.** Mehrere Identitäten dürfen dasselbe Konto nutzen; ein Subject in zwei Konten bricht den Start ab. Kein „first match wins" — bei zwei plausiblen Zuordnungen gibt es keine richtige Wahl, also darf der Server nicht raten.

5. **Kein Fallback.** Ein unbekanntes Subject bekommt `403`, nicht das Default-Konto.

6. **Ein Client je Konto, Login serialisiert.** Der Pool erzeugt pro Konto-Key genau einen `*fileee.Client` für die Prozesslebensdauer; parallele Erst-Requests werden über `singleflight` zusammengefasst. Zwei gleichzeitige Logins gegen dasselbe Konto sind genau das Muster, das serverseitige Konto-Sperren (`secondsBlocked`) auslöst.

7. **Zusätzlich ein globaler Rate-Limiter** über einen gemeinsamen `http.RoundTripper`, der allen Konto-Clients übergeben wird. Das Per-Konto-Limit bleibt bestehen, der globale Deckel hält die Gesamtlast unabhängig von der Kontenzahl. Damit bleibt die Zusicherung aus `go-fileee` ADR-0005 gewahrt.

8. **Session-Dateien getrennt je Konto**, `<key>.json` mit Rechten `0600`. Der Konto-Key wird gegen `^[a-z0-9_-]{1,32}$` geprüft — ohne diese Prüfung wäre ein Key wie `../../etc/x` ein Schreibzugriff außerhalb des Verzeichnisses.

9. **Flache, präfigierte Schlüssel im Secret-Backend** (`FILEEE_ACCOUNT_<KEY>_USERNAME` …), bewusst kein Ordner pro Konto. `infisical export` ist pfad-gescoped und kennt kein `--recursive`; gleichnamige Schlüssel aus mehreren Ordnern kollidierten beim Flatten in eine Dotenv-Datei. So bleibt der Dual-Mode-Bootstrap aus `fileee-server` unverändert übernehmbar.

10. **Geloggt werden nur Konto-Keys.** Subjects erscheinen ausschließlich gekürzt (erste 8 Zeichen eines SHA-256), sonst stünde bei `MCP_OIDC_SUBJECT_CLAIM=email` die Klartext-Adresse im 403-Pfad.

## Konsequenzen

**Positiv**

- Die Zuordnung ist kryptographisch an das Token gebunden und hängt nicht an einer Proxy-Konfiguration.
- Punkte 2 und 3 schließen zwei konkrete Wege, über die Anfragen an ein fremdes Fileee-Konto hätten gelangen können. Beide wären ohne Prüfung des SDK-Quelltexts nicht aufgefallen.
- Der `single`-Modus ist derselbe Codepfad mit genau einem Eintrag — keine Sonderbehandlung.

**Negativ**

- Punkt 2 ist eine Regel, die man beim Schreiben eines neuen Tools leicht verletzt, weil der bequeme Weg über den Kontext führt. Deshalb steht sie in `CONTRIBUTING.md`, in der PR-Checkliste und wird per Test abgesichert.
- Ein neues Konto erfordert vier Secrets, einen Eintrag in `FILEEE_ACCOUNTS` und einen Neustart. Für eine Handvoll Konten vertretbar.
- Der Pool hält je Konto eine Session offen und hält sie per Keepalive am Leben. Bei vielen selten genutzten Konten ist das unnötige Grundlast — bei Bedarf später über einen Leerlauf-Timeout lösbar.

**Wann diese Entscheidung neu zu bewerten ist**

Wenn fremde Benutzer ihre Fileee-Credentials selbst pflegen sollen. Dann greift Punkt 9 zu kurz und es braucht Least-Privilege je Konto — etwa einen Ordner pro Konto mit eigener Machine Identity, geladen über die Infisical-API statt über einen Dotenv-Export. Betroffen wäre nur die Secret-Ladeschicht.

## Referenzen

- [go-fileee ADR-0005](https://github.com/strausmann/go-fileee/blob/main/docs/adr/0005-schonender-betrieb-rate-limiting.md) — schonender Betrieb, Rate-Limiting im Transport
- [go-fileee#20](https://github.com/strausmann/go-fileee/issues/20) — `Upload` puffert den Multipart-Body vollständig im RAM; mitbestimmend für die Upload-Grenze
- [go-fileee#22](https://github.com/strausmann/go-fileee/issues/22) — Cookie-Pfad beim Restore; Anlass für einen Test, der nach dem Laden einer Session-Datei einen XSRF-pflichtigen Request fährt
- Go MCP SDK v1.7.0, `mcp/streamable.go` und `mcp/shared.go` — Grundlage der Punkte 2 und 3
- [Gangway](https://github.com/strausmann/gangway), Dokumentation https://gangway.strausmann.cloud — unabhängiger zweiter Fund desselben SDK-Fehlers, strukturell andere Lösung; siehe Nachtrag unten

## Nachtrag (2026-08-08)

**Anlass.** Das separat entstandene Projekt [Gangway](https://github.com/strausmann/gangway) — eine wiederverwendbare Auth-/Autorisierungs-Schicht für MCP-Server auf demselben Go-SDK — hat unabhängig denselben Fehler gefunden, den dieses ADR unter „Die SDK-Semantik" beschreibt: den Doc-Kommentar zu `AttachMCP` in `serve/serve.go` (Stand Tag `v0.1.0`), Punkt 2.

**Was dadurch bestätigt wird.** Kontext und Punkt 1 bleiben unverändert richtig — zwei getrennte Implementierungen sind unabhängig voneinander auf dasselbe SDK-Verhalten gestoßen. Ebenso unverändert: Punkte 4–10 (ein Subject pro Konto, kein Fallback, Client-Pool mit `singleflight`, globaler Rate-Limiter, getrennte Session-Dateien, präfigierte Secret-Keys, gekürztes Logging). Keiner dieser Punkte hängt an einem bestimmten Auth-Transportweg.

**Was fraglich wird.** Gangway löst denselben Fehler strukturell anders als Punkt 2/3 hier: nicht durch einen Lese-Pfad, der den betroffenen Kontext umgeht (`req.GetExtra().TokenInfo`), sondern indem die SDK-eigene `auth`-Middleware für die Identität gar nicht erst verwendet wird. Eine eigene HTTP-Middleware verifiziert das Bearer-Token vor dem MCP-Handler und `AttachMCP` erzwingt `mcp.StreamableHTTPOptions.Stateless = true` — dadurch entsteht keine sitzungsübergreifende Kontextbindung mehr, an der ein `context.Value`-Read hängenbleiben könnte. Übernähme dieser Server Gangway als Unterbau für die OAuth-2.1-Rolle aus [ADR-0009](0009-resource-server-statt-eigener-authorization-server.md), entfiele der Code-Pfad, auf den sich Punkt 2 (GetExtra-Gebot) und Punkt 3 (TokenInfo.UserID-Pflicht) beziehen, vollständig — `auth.RequireBearerToken`, `TokenInfo` und `GetExtra()` würden dann nicht mehr benutzt. Die Identität käme stattdessen aus `serve.IdentityFrom(ctx)`, sicher aus demselben Grund, aus dem Gangway `AttachMCP` überhaupt so gebaut hat.

**Wie es weiterging.** Zum Zeitpunkt dieses Nachtrags war die Übernahme noch keine getroffene Entscheidung. Sie ist es inzwischen: [ADR-0015](0015-gangway-als-unterbau.md) legt fest, dass dieser Server Gangway v0.2.0 als Unterbau für Anmeldung, Adress-Freigabeliste, Freigabe je Werkzeug und Zugriffsprotokoll benutzt. Punkt 2 und 3 oben sind entsprechend umformuliert — auf „Identität ausschließlich über `serve.IdentityFrom(ctx)`, niemals selbst zwischenspeichern" —, jeweils mit der ursprünglichen, weiterhin gültigen Regel für den Fall, dass dieser Server einmal ohne Gangway gebaut würde. Die ursprüngliche Fassung dieses Nachtrags bleibt oberhalb unverändert stehen, als Protokoll des Fundes.
