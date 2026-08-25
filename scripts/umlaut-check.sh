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
# Wortgrenzen zwischen den Teilen) werden von der Wort-für-Wort-Prüfung
# (siehe zeile_hat_verstoss unten: EXAKTER Vergleich pro token, nicht
# Teilstring-Suche) NICHT getroffen — "issuedStore" wird als EIN token
# extrahiert und exakt mit "issuedstore" (kleingeschrieben) verglichen, das
# steht in keiner der beiden Wortlisten, selbst wenn "fuer"/"ueber"/etc. als
# Teilstring irgendwo darin vorkäme.
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

# Bekannte Ersatzschreibungen, aufgeteilt in ZWEI Klassen mit unterschiedlicher
# Ausnahmelogik (Nachprüfungs-Befund, Codex-Review — siehe die lange
# Begründung weiter unten bei zeile_hat_verstoss/ss_words):
#
#   - ae_oe_ue_words: Wörter mit ae/oe/ue statt ä/ö/ü. IMMER ein Verstoß,
#     unabhängig von Gross-/Kleinschreibung — Ä/Ö/Ü haben eine eigene
#     Versalienform, es gibt dafür KEINE legitime Alles-Grossgeschrieben-
#     Ausnahme (anders als bei ß, siehe unten).
#   - ss_words: Wörter, deren EINZIGE Ersatzschreibung "ss" statt "ß" ist
#     (schliesst/schliessen/schliesslich/ausschliesslich/ausschliesst — die
#     einzigen fünf Wörter dieser Liste ohne jede ae/oe/ue-Substitution).
#     Für diese gilt die Versalien-Ausnahme (SS ist die korrekte Wiedergabe
#     von ß in Grossbuchstaben, z. B. AUSSCHLIESSLICH) — siehe unten.
#
# NIE durch ein generisches ae|oe|ue-Muster ersetzen (siehe Kopfkommentar).
ae_oe_ue_words=(
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

# ss_words: siehe Erklärung oben bei ae_oe_ue_words. Nur diese fünf Wörter
# tragen ausschliesslich eine ss-statt-ß-Substitution ohne jedes ae/oe/ue —
# für JEDES andere Wort in ae_oe_ue_words, dessen Grundform zusätzlich ein
# "ss" enthält (z. B. Groesse, verhaeltnismaessig, zuverlaessig), matcht
# bereits das ae/oe/ue-Muster unabhängig von Gross-/Kleinschreibung, eine
# Versalien-Ausnahme wäre dort sinnlos (WAERE bliebe falsch, auch in
# Grossbuchstaben) — deshalb stehen sie bewusst in ae_oe_ue_words, nicht hier.
ss_words=(
  schliesst schliessen schliesslich ausschliesslich ausschliesst
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

# Nachprüfungs-Befund (Copilot-Review): \< / \> sind — genau wie \b — KEIN
# Teil von POSIX-ERE, sie sind eine GNU-Erweiterung. Der vorherige Kommentar
# an dieser Stelle behauptete, \< / \> seien "die portablere, traditionelle
# Alternative" zu \b — das ist so nicht haltbar: manche BSD-/musl-/
# busybox-grep-Implementierungen kennen \< / \> ebenfalls nicht zuverlässig,
# das Tor könnte auf einer anderen Plattform genauso still nichts mehr
# finden wie mit \b. Ein reiner Zeichenklassen-Ersatz für \< / \> (Wortanfang
# = Zeilenanfang ODER Nicht-Wortzeichen davor, Wortende entsprechend) hätte
# zusätzlich ein eigenes, unabhängiges Problem: grep -oE liefert NICHT
# überlappende Treffer, das Grenzzeichen NACH einem Treffer wird also vom
# nächsten Treffer-Versuch bereits "verbraucht" — bei zwei Wörtern, die nur
# durch EIN einziges Trennzeichen getrennt sind ("SCHLIESST schliesst"),
# bliebe für den zweiten Treffer kein Grenzzeichen mehr übrig und er würde
# schlicht nicht gefunden (mit einem eigenen Testlauf belegt, bevor diese
# Fassung entstand — siehe Aufgabe-Historie).
#
# Die tatsächlich robuste UND POSIX-ERE-portable Lösung ist deshalb keine
# Grenzzeichen-Regex, sondern Tokenisierung: `grep -oE '[A-Za-z]+'` zerlegt
# jede Zeile in maximale Buchstaben-Läufe (token) — ein reiner Zeichenklassen-
# Ausdruck ohne jede \b/\</\>-Erweiterung, unterstützt von jedem POSIX-ERE-
# grep. Jedes token wird danach per EXAKTEM (nicht Teilstring-)Vergleich
# gegen die beiden Wortlisten geprüft (zeile_hat_verstoss unten). Das löst
# nebenbei auch das eingangs im Kopfkommentar genannte camelCase-Problem:
# "issuedStore" ist EIN token ("issuedStore"), das exakt mit keinem Eintrag
# der Listen übereinstimmt, selbst wenn eine Ersatzschreibung als Teilstring
# darin vorkäme.
declare -A ae_oe_ue_set=()
for _w in "${ae_oe_ue_words[@]}"; do
  ae_oe_ue_set["${_w,,}"]=1
done
declare -A ss_set=()
for _w in "${ss_words[@]}"; do
  ss_set["${_w,,}"]=1
done
unset _w
# Beide Lookups sind case-insensitiv (Vergleich gegen die kleingeschriebene
# Form des tokens): ein Satzanfang oder ein via Wortverkettung
# grossgeschriebenes "Fuer"/"Ueber"/"Muesste"/"Koennte" faellt sonst durch,
# weil die Listen oben nur die jeweils tatsaechlich beobachtete Schreibung
# fuehren (meist Kleinschreibung), nicht jede grammatikalisch moegliche
# Gross-/Kleinschreibvariante. Das erzeugt KEINE neuen Fehlalarme: die
# Wörter in den Listen sind konkrete, vollstaendige Zeichenketten (kein
# "ae|oe|ue"-Fragment), also aendert Gross-/Kleinschreibung nichts daran,
# dass "true"/"value"/"queue"/"Quelle"/"Modulquellcode"/"wuerdig" (mit
# echtem ü) weiterhin nicht matchen.

# ist_reine_versalien prüft, ob token VOLLSTÄNDIG grossgeschrieben ist — die
# einzige legitime ss-statt-ß-Ausnahme (siehe ss_words' eigenen Kommentar
# oben). Erwartet das token in seiner ORIGINALEN Gross-/Kleinschreibung
# (nicht die kleingeschriebene Lookup-Form aus ae_oe_ue_set/ss_set).
ist_reine_versalien() {
  local w="$1"
  [[ -n "$w" && "$w" == "${w^^}" && "$w" != "${w,,}" ]]
}

# token_ist_verstoss ist die EINZIGE Stelle, die entscheidet, ob ein
# einzelnes token (siehe oben: ein maximaler Buchstaben-Lauf, per
# `grep -oE '[A-Za-z]+'` gewonnen) ein echter Verstoss ist — reine
# Bash-Zeichenkettenprüfung, kein weiterer Prozess-Start. Sowohl
# zeile_hat_verstoss (unten, für Tests/Gegenproben gegen eine einzelne
# Zeile) als auch die Hauptschleife (für den echten, gebündelten Lauf über
# das ganze Repo, siehe deren eigenen Kommentar bei stream_line_has_hit)
# rufen AUSSCHLIESSLICH diese eine Funktion auf — zwei parallele
# Kopien derselben Regel wären ein eigenes Fehlerrisiko (Copilot-Review-
# Musterhinweis: "je Treffer, nicht je Zeile" gilt für die ENTSCHEIDUNG,
# nicht nur für EINEN der beiden Aufrufer).
#
# Nachprüfungs-Befund, Codex-Review — "je Treffer, nicht je Zeile": diese
# Funktion prüft GENAU EIN token, nie eine ganze Zeile, und behebt damit
# zwei unabhängige Lücken der vorherigen Fassung (die einen einzigen
# kombinierten Suchlauf plus ein nachgeschaltetes zeilenweites `grep -v`
# nutzte):
#
#   1. Eine Zeile, die sowohl ein legitimes Versalien-ss-Wort ALS AUCH einen
#      echten ae/oe/ue-Verstoss trägt (z. B. "// AUSSCHLIESSLICH wuerde"),
#      liess den ae/oe/ue-Verstoss vorher unbemerkt durchrutschen, weil das
#      zeilenweite `grep -v` die GESAMTE Zeile verwarf, sobald sie IRGENDWO
#      ein Versalien-ss-Wort enthielt (der damalige Kommentar an dieser
#      Stelle behauptete das Gegenteil — "gemischte Zeilen bleiben
#      erhalten" — das stimmte nicht: `grep -v` prüft die ganze Zeile, nicht
#      den einzelnen Treffer). Da hier jedes token EINZELN bewertet wird,
#      hat "AUSSCHLIESSLICH" (legitime Versalien) keinerlei Einfluss auf
#      die Bewertung des unabhängigen tokens "wuerde".
#   2. Eine Zeile mit ZWEI ss-Wort-Treffern, von denen einer legitim
#      grossgeschrieben ist und der andere nicht (z. B.
#      "SCHLIESST schliesst"), hätte mit einer zeilenweiten Ausnahme
#      ebenfalls den echten Verstoss verdeckt. Da beide Aufrufer diese
#      Funktion pro token einzeln aufrufen, wird ist_reine_versalien für
#      "SCHLIESST" und für "schliesst" unabhängig ausgewertet.
token_ist_verstoss() {
  local token="$1"
  local key="${token,,}"

  if [[ -n "${ae_oe_ue_set[$key]:-}" ]]; then
    return 0
  fi
  if [[ -n "${ss_set[$key]:-}" ]] && ! ist_reine_versalien "$token"; then
    return 0
  fi
  return 1
}

# zeile_hat_verstoss zerlegt EINE Zeile in tokens (grep -oE, siehe
# token_ist_verstoss' eigenen Kommentar für das Warum von "token statt
# Grenzzeichen-Regex") und prüft jedes davon einzeln über
# token_ist_verstoss — für Tests/Gegenproben gegen eine einzelne Zeile;
# die Hauptschleife unten nutzt stattdessen einen gebündelten Lauf über
# alle Kandidatenzeilen einer Datei auf einmal (Performance, siehe deren
# eigenen Kommentar), ruft aber dieselbe token_ist_verstoss-Funktion auf.
zeile_hat_verstoss() {
  local zeile="$1" token

  while IFS= read -r token; do
    [[ -z "$token" ]] && continue
    if token_ist_verstoss "$token"; then
      return 0
    fi
  done < <(printf '%s\n' "$zeile" | grep -oE '[A-Za-z]+' || true)

  return 1
}

violations=0
while IFS= read -r -d '' file; do
  rel="${file#./}"
  is_exception "$rel" && continue

  # Vorauswahl: volle "//"-Kommentarzeilen ODER Zeilen mit einem doppelten
  # Anführungszeichen (deckt String-Literale samt Fortsetzungszeilen ab).
  candidates="$(grep -nE '^[[:space:]]*//|"' "$file" || true)"
  [[ -z "$candidates" ]] && continue

  # Performance: NICHT zeile_hat_verstoss (das seinerseits `grep -oE`
  # aufruft) pro Kandidatenzeile einzeln aufrufen — bei ueber 12.000
  # Kandidatenzeilen im gesamten Repo bedeutet das ueber 12.000
  # Prozess-Neustarts allein fuer diesen einen Zweck (mit einem eigenen
  # Zeitmesslauf belegt: >90s statt <5s). Stattdessen EIN `grep -noE`
  # Aufruf pro Datei ueber den GESAMTEN candidates-Block: `-n` liefert dabei
  # die Nummer der STREAM-Zeile innerhalb von candidates (1-basiert, NICHT
  # die echte Datei-Zeilennummer, die bereits als Text im Kandidatentext
  # selbst steht), zusammen mit jedem einzelnen token — genau die
  # Information, die zeile_hat_verstoss sonst pro Aufruf einzeln neu
  # beschafft haette. candidate_lines (mapfile) haelt denselben
  # candidates-Block als Array, Index i-1 fuer Stream-Zeile i, damit die
  # betroffene Originalzeile fuer die Ausgabe wiedergefunden werden kann.
  mapfile -t candidate_lines <<<"$candidates"
  declare -A stream_line_has_hit=()
  while IFS=: read -r stream_line token; do
    [[ -z "$token" ]] && continue
    if token_ist_verstoss "$token"; then
      stream_line_has_hit["$stream_line"]=1
    fi
  done < <(grep -noE '[A-Za-z]+' <<<"$candidates" || true)

  hits=""
  file_hit=0
  # Numerisch sortiert (nicht die unspezifizierte Assoziativ-Array-
  # Iterationsreihenfolge) — die Ausgabe soll in Datei-Reihenfolge bleiben,
  # damit ein Diff beim Abarbeiten von Fund-Listen nachvollziehbar ist.
  while IFS= read -r stream_line; do
    [[ -z "$stream_line" ]] && continue
    file_hit=1
    hits+="${candidate_lines[stream_line - 1]}"$'\n'
  done < <(printf '%s\n' "${!stream_line_has_hit[@]}" | sort -n)

  if [[ "$file_hit" -eq 1 ]]; then
    violations=$((violations + 1))
    echo "=== $rel ==="
    printf '%s' "$hits"
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
