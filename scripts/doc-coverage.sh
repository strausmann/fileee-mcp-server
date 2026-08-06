#!/usr/bin/env bash
# Prüft, dass jedes EXPORTIERTE Symbol (Typ/Funktion/Methode/Const/Var) einen Doc-Comment trägt.
#
# Scope: ALLE Go-Dateien des Repos ausser Tests und Fremdcode (vendor/, node_modules/).
# Bewusst nicht auf cmd/ beschränkt — sonst faellt ein spaeter hinzugefuegtes internal/- oder
# pkg/-Verzeichnis still aus dem Gate heraus, obwohl gerade dort exportierte APIs entstehen.
#
# Gruppierte Deklarationen (`const (` / `var (` / `type (`) gelten als dokumentiert, wenn
# ENTWEDER der Block einen Doc-Comment traegt ODER das einzelne exportierte Mitglied — beides
# ist idiomatisches Go, ein Zwang zum Kommentar an jedem Mitglied waere es nicht.
#
# Mehrfach-Deklarationen (`var Foo, Bar int`) werden je Bezeichner geprueft, nicht nur am
# ersten — sonst rutschen exportierte Namen hinter dem Komma am Gate vorbei.
#
# Ausgenommen sind Standard-Interface-Methoden, die konventionell keinen Kommentar brauchen.
set -euo pipefail
cd "$(dirname "$0")/.."
exempt='^(MarshalJSON|UnmarshalJSON|String|Error)$'
gaps="$(mktemp)"
trap 'rm -f "$gaps"' EXIT
while IFS= read -r file; do
  awk -v F="$file" -v EXEMPT="$exempt" '
    # Meldet jeden exportierten Bezeichner einer moeglicherweise mehrteiligen Namensliste.
    # rest ist alles nach dem Schluesselwort, z. B. "Foo, Bar int" oder "A, B = 1, 2".
    function melde(praefix, rest,   n, teile, i, nm) {
      sub(/[ \t]*=.*/, "", rest)
      n = split(rest, teile, /[ \t]*,[ \t]*/)
      for (i = 1; i <= n; i++) {
        nm = teile[i]
        sub(/[ \t].*/, "", nm)
        sub(/[(\[].*/, "", nm)
        if (nm ~ /^[A-Z]/) print F":"NR": "praefix" "nm
      }
    }

    # Block-Doc-Comment /* ... */ — in Go gueltig, auch wenn // die uebliche Form ist.
    # Ohne diesen Zweig meldet das Gate solche Symbole faelschlich als undokumentiert; ein Gate
    # mit False Positives wird umgangen statt befolgt. Deckt auch die einzeilige Form ab.
    /^[ \t]*\/\*/ { incomment=1 }
    incomment {
      if ($0 ~ /\*\//) { incomment=0; hascomment=1 }
      next
    }

    # Fuehrender Whitespace erlaubt: Doc-Comments an Block-Mitgliedern sind eingerueckt.
    /^[ \t]*\/\// { hascomment=1; next }

    # Gruppierte Deklaration: Doc-Comment des Blocks merken und an die Mitglieder vererben.
    /^(const|var|type) \(/ { inblock=1; blockdoc=hascomment; hascomment=0; next }
    inblock && /^\)/       { inblock=0; blockdoc=0; hascomment=0; next }
    inblock {
      if ($0 ~ /^[ \t]+[A-Za-z_]/ && !blockdoc && !hascomment) {
        zeile=$0; sub(/^[ \t]+/, "", zeile)
        melde("Block-Mitglied", zeile)
      }
      hascomment=0; next
    }

    /^func \([^)]*\) [A-Z]/ {
      name=$0; sub(/^func \([^)]*\) /,"",name); sub(/[(\[].*/,"",name)
      if (name !~ EXEMPT && !hascomment) print F":"NR": Methode "name
      hascomment=0; next
    }
    /^func [A-Z]/ { name=$2; sub(/[(\[].*/,"",name); if(!hascomment) print F":"NR": func "name; hascomment=0; next }
    /^type [A-Z]/ { name=$2; sub(/[(\[,].*/,"",name); if(!hascomment) print F":"NR": type "name; hascomment=0; next }
    /^(const|var) [A-Za-z_]/ {
      if (!hascomment) { zeile=$0; sub(/^(const|var)[ \t]+/, "", zeile); melde($1, zeile) }
      hascomment=0; next
    }
    { hascomment=0 }
  ' "$file"
done < <(find . -name '*.go' ! -name '*_test.go' \
           -not -path './vendor/*' -not -path './node_modules/*' -not -path './.git/*') \
  | sort | tee "$gaps"
n=$(wc -l < "$gaps" | tr -d ' ')
echo "Undokumentierte exportierte Symbole: $n"
[ "$n" -eq 0 ] || exit 1
