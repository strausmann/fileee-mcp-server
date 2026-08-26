#!/usr/bin/env bash
# Erzwingt die beiden Dauer-Regeln aus docs/adr/README.md:
#
#   Registry-Pflicht (Abschnitt "ADR-Regelwerk", Regel "Registry-Pflicht") —
#   "Ein ADR, das nicht in der Registry steht, gilt als uebersehen und damit
#   als nicht existent." Ohne Gate faellt genau das still aus: die Datei ist
#   da, die Entscheidung findet niemand.
#
#   Lineage beidseitig (Abschnitt "ADR-Regelwerk", Regel "Lineage (beidseitig
#   pflegen)") — nennt ADR A im Header ADR B, muss B auch A nennen. Einseitige
#   Verweise sind der haeufige Fall, weil man beim Schreiben des neuen ADR nur
#   nach vorn schaut und die alten Header vergisst.
#
# Geprueft wird nur der Header (erste 10 Zeilen). Fliesstext und der Abschnitt
# Referenzen duerfen ADRs frei nennen, ohne einen Rueckverweis zu erzwingen.
#
# Nur REPO-INTERNE Verweise zaehlen, erkennbar am relativen Link-Ziel
# [ADR-NNNN](NNNN-slug.md). Cross-Repo-ADRs (0001-0008 in go-fileee und
# fileee-server) stehen laut Abschnitt "Nummernkreis: fortlaufend ueber die
# Repo-Familie" als vollstaendige URL da; fuer sie gibt es hier weder eine
# Datei noch einen Header, in den man einen Rueckverweis schreiben koennte.
# Ohne diese Unterscheidung meldet das Gate sie als tote Verweise — und ein
# Gate mit False Positives wird umgangen statt befolgt.
set -uo pipefail
cd "$(dirname "$0")/.."
reg=docs/adr/README.md
funde="$(mktemp)"
trap 'rm -f "$funde"' EXIT

{
  # 1. Jede ADR-Datei steht in der Registry.
  for f in docs/adr/[0-9][0-9][0-9][0-9]-*.md; do
    grep -qF "($(basename "$f"))" "$reg" \
      || echo "$f: fehlt in der Registry ($reg)"
  done

  # 2. Kein Registry-Link zeigt ins Leere.
  grep -oE '\([0-9]{4}-[a-z0-9-]+\.md\)' "$reg" | tr -d '()' | sort -u | while read -r b; do
    [ -f "docs/adr/$b" ] || echo "$reg: toter Link auf $b"
  done

  # 3. Lineage beidseitig.
  for f in docs/adr/[0-9][0-9][0-9][0-9]-*.md; do
    a=$(basename "$f" | cut -c1-4)
    for b in $(sed -n '1,10p' "$f" | grep -oE '\[ADR-[0-9]{4}\]\([0-9]{4}-' \
               | grep -oE '^\[ADR-[0-9]{4}' | sed 's/\[ADR-//' | sort -u); do
      g=$(ls docs/adr/"$b"-*.md 2>/dev/null | head -1)
      if [ -z "$g" ]; then
        echo "$f: Header verweist auf ADR-$b, aber die Datei fehlt"
      elif ! sed -n '1,10p' "$g" | grep -q "ADR-$a"; then
        echo "$g: Rueckverweis auf ADR-$a fehlt (Lineage wird beidseitig gepflegt)"
      fi
    done
  done
} | sort -u | tee "$funde"

n=$(wc -l < "$funde" | tr -d ' ')
echo "ADR-Registry-Verstoesse: $n"
[ "$n" -eq 0 ] || exit 1
