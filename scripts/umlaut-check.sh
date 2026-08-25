#!/usr/bin/env bash
# Prüft deutsche Prosa in .go-Dateien (Kommentare + deutsche Test-Meldungsstrings)
# auf bekannte ASCII-Ersatzschreibungen (ae/oe/ue/ss statt ä/ö/ü/ß) — siehe
# .claude/rules/deutsche-umlaute.md (homelab-management) und Issue #71
# (strausmann/fileee-mcp-server, "German prose in Go test files uses ae/oe/ue
# instead of real umlauts").
#
# Anlass: Aufgabe 1 dieses Plans hatte denselben Verstoß, wurde in einem eigenen
# Commit behoben — Aufgabe 3 führte ihn erneut ein. Ohne ein Gate verfällt die
# Disziplin unter Zeitdruck, egal wie oft man es sagt (dieselbe Lehre wie
# test-coverage-pflicht.md).
#
# BEWUSST KEIN generisches "ae|oe|ue"-Muster: das träfe "true", "value", "queue",
# "Quelle", "Modulquellcode" und ähnliche englische/deutsche Wörter ohne jede
# Substitution. Stattdessen eine Liste KONKRETER, bekannter Ersatzschreibungen —
# lieber ein paar Wörter zu wenig als ein Fehlalarm: ein Gate, das grundlos rot
# wird, schaltet irgendwann jemand ab (test-coverage-pflicht.md, Abschnitt
# "Hart JA — aber niemals rot aus einem Nicht-Fehler").
#
# Scope: Zeilen, die entweder (a) eine vollständige "//"-Kommentarzeile sind,
# oder (b) ein doppeltes Anführungszeichen enthalten — Letzteres deckt
# t.Fatalf/t.Errorf-Meldungen samt mehrzeiliger String-Verkettung ab, ohne
# einen echten Go-Parser zu brauchen. Go-Bezeichner (camelCase, keine
# Wortgrenzen zwischen den Teilen) werden von der Wortgrenzen-Prüfung (\b)
# der einzelnen Wörter unten NICHT getroffen — "issuedStore" enthält z. B.
# kein von \b begrenztes "fuer"/"ueber"/etc.
#
# Ausnahmeliste (unten, `exceptions`): Bestandsdateien, die Issue #71 bereits
# als Altlast erfasst hat, plus .github/workflows/test.yml (dort gilt bewusst
# das bestehende ASCII im Kommentarblock — von Aufgabe 3 selbst so gehandhabt,
# um dessen Konsistenz nicht zu brechen). Die Liste SCHRUMPFT über die Zeit
# (Issue #71 arbeitet sie ab) — sie WÄCHST NICHT: jede neue .go-Datei muss
# von Anfang an sauber sein, keine Datei wird nachträglich hinzugefügt, um
# ein rotes Gate stillzulegen.
set -euo pipefail
cd "$(dirname "$0")/.."

# Bekannte Ersatzschreibungen — bewusst unvollständig, wird bei Bedarf um
# weitere bekannte Wörter ergänzt. NIE durch ein generisches ae|oe|ue-Muster
# ersetzen (siehe Kopfkommentar).
wrong_words=(
  waere waeren
  koennen koennte koennten koennt
  muessen muesste muesst
  fuer dafuer wofuer worueber
  ueber ueberholt ueberschreitet ueberschreiben ueberlaeuft ueberlaufen Ueberlauf
  ueberstehen ueberprueft ueberpruefen ueberpruefung
  gruen
  geprueft ungeprueft ungepruefte ungepruefter ungeprueften pruefen prueft Pruefung Pruefungen
  # ungeprueft/wuerde sind in Task 4 durchgerutscht, weil nur die Grundform gelistet war.
  # Ableitungen mit Vorsilbe oder Endung stehen deshalb ausdrücklich mit in der Liste.
  wuerde wuerden wuerdest
  kuenstlich
  Luecke Luecken
  schliesst schliessen schliesslich ausschliesslich ausschliesst
  zurueck zurueckliefert zurueckgreifen
  naechste naechsten naechster naechstes
  Groesse groesser groesseren groessere groesste
  unabhaengig unabhaengige unabhaengigen unabhaengiger
  tatsaechlich tatsaechliche tatsaechlicher tatsaechlichen
  unveraendert unveraenderten unveraenderte veraendert aendert aendern
  Aenderung Aenderungen
  gueltig gueltige gueltiger gueltiges gueltigen
  verdraengt verdraengten verdraengte Verdraengung
  haette haetten
  zusaetzlich zusaetzliche zusaetzlichen zusaetzlicher
  gewaehlt gewaehlte gewaehlten
  aelteste aeltesten aeltester
  Gegenstueck Gegenstuecke
  Identitaet Identitaeten
  ausfuehrlich ausfuehrliche ausfuehrlichen
  noetig benoetigt benoetigen benoetigte
  gleichgueltig
  vollstaendig vollstaendige vollstaendiger vollstaendigen
  unverhaeltnismaessig verhaeltnismaessig
  faerbt faerben gefaerbt
  schlaegt
  Begruendung Begruendungen begruendet begruenden
  haelt haeltst
  verfuegbar verfuegbare verfuegbaren
  zuverlaessig zuverlaessige zuverlaessigen
  waehrend
  moeglich moegliche moeglichen moeglicherweise moeglichkeit kleinstmoegliche kleinstmoeglich
  loest geloest Loesung Loesungen
  oeffentlich oeffentliche oeffentlichen
  Praefix Praefixe Praefixen Praefixlisten
  haengen haengt abhaengig abhaengige abhaengigen Abhaengigkeit Abhaengigkeiten
  zaehlt
  woertlich
  fruehere fruehe fruehen
  kuenftig kuenftige kuenftigen kuenftiger
  Faehigkeitsmenge
  verdaechtigen verdaechtig
  Erweiterungsmoeglichkeit
  Grenzfaelle
  Sonderfaelle
)

# Dateien, die Issue #71 (strausmann/fileee-mcp-server) als Altlast erfasst
# hat — ermittelt, indem dieses Skript einmal OHNE Ausnahmeliste gegen den
# Stand VOR Aufgabe 3s Korrektur lief. Reihenfolge: wie von `find` geliefert,
# nicht alphabetisch sortiert nachbearbeitet, um Diffs beim Abarbeiten von
# Issue #71 klein zu halten (eine Zeile raus, kein Sortier-Diff daneben).
exceptions=(
  ".github/workflows/test.yml"
  "cmd/fileee-mcp-server/main_test.go"
  "cmd/fileee-mcp-server/main.go"
  "internal/config/config.go"
  "internal/config/version.go"
  "internal/config/config_test.go"
  "internal/tools/read_sync_test.go"
  "internal/tools/write_test.go"
  "internal/tools/write_people_test.go"
  "internal/tools/write_documents.go"
  "internal/tools/write_documents_test.go"
  "internal/tools/read_generic_sync_diag_test.go"
  "internal/tools/ops_test.go"
  "internal/tools/read_generic_test.go"
  "internal/tools/descriptions_test.go"
  "internal/tools/read_people_test.go"
  "internal/tools/read_account_test.go"
  "internal/tools/write_boxes_test.go"
  "internal/tools/read_binary_test.go"
  "internal/tools/read_document_test.go"
  "internal/tools/read_reference_test.go"
  "internal/clientpool/pool_test.go"
  "internal/server/scopes_test.go"
  "internal/server/scopes.go"
  "internal/server/scopes_advertise_test.go"
  "internal/server/ratelimit_test.go"
  "internal/server/server.go"
  "internal/server/ratelimit.go"
  "internal/server/e2e_testidp_test.go"
  "internal/server/server_test.go"
)

is_exception() {
  local f="$1"
  local e
  for e in "${exceptions[@]}"; do
    [[ "$f" == "$e" ]] && return 0
  done
  return 1
}

pattern_body="$(printf '%s|' "${wrong_words[@]}")"
pattern="\\b(${pattern_body%|})\\b"
# Der eigentliche Abgleich unten laeuft case-insensitiv (grep -Ei): ein
# Satzanfang oder ein via Wortverkettung grossgeschriebenes "Fuer"/"Ueber"/
# "Muesste"/"Koennte" faellt sonst durch, weil die Liste oben nur die
# jeweils tatsaechlich beobachtete Schreibung fuehrt (meist Kleinschreibung),
# nicht jede grammatikalisch moegliche Gross-/Kleinschreibvariante. Das
# erzeugt KEINE neuen Fehlalarme: die Woerter in der Liste sind konkrete,
# vollstaendige Zeichenketten (kein "ae|oe|ue"-Fragment), also aendert
# Gross-/Kleinschreibung nichts daran, dass "true"/"value"/"queue"/"Quelle"/
# "Modulquellcode"/"wuerdig" (mit echtem ü) weiterhin nicht matchen.

violations=0
while IFS= read -r -d '' file; do
  rel="${file#./}"
  is_exception "$rel" && continue

  # Vorauswahl: volle "//"-Kommentarzeilen ODER Zeilen mit einem doppelten
  # Anführungszeichen (deckt String-Literale samt Fortsetzungszeilen ab).
  candidates="$(grep -nE '^[[:space:]]*//|"' "$file" || true)"
  [[ -z "$candidates" ]] && continue

  hits="$(printf '%s\n' "$candidates" | grep -Ei "$pattern" || true)"
  if [[ -n "$hits" ]]; then
    violations=$((violations + 1))
    echo "=== $rel ==="
    printf '%s\n' "$hits"
    echo
  fi
done < <(find . -name '*.go' -not -path './vendor/*' -print0)

if [[ "$violations" -gt 0 ]]; then
  echo "Ersatzschreibungen deutscher Umlaute in ${violations} Datei(en) gefunden."
  echo "Echte Umlaute verwenden (ä ö ü ß) statt ae/oe/ue/ss — siehe .claude/rules/deutsche-umlaute.md."
  echo "Bestandsdateien mit bekannter Altlast gehören in die Ausnahmeliste NUR, wenn sie unter"
  echo "Issue #71 laufen — nicht, um ein rotes Gate für neuen Code stillzulegen."
  exit 1
fi
echo "Keine Ersatzschreibungen gefunden."
