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
# Ausgenommen sind Standard-Interface-Methoden, die konventionell keinen Kommentar brauchen.
set -euo pipefail
cd "$(dirname "$0")/.."
exempt='^(MarshalJSON|UnmarshalJSON|String|Error)$'
gaps="$(mktemp)"
trap 'rm -f "$gaps"' EXIT
while IFS= read -r file; do
  awk -v F="$file" -v EXEMPT="$exempt" '
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
      # Exportiertes Mitglied innerhalb des Blocks (fuehrender Whitespace ist Gofmt-Standard).
      if ($0 ~ /^[ \t]+[A-Z][A-Za-z0-9_]*/) {
        name=$1; sub(/[(\[,].*/,"",name)
        if (!blockdoc && !hascomment) print F":"NR": Block-Mitglied "name
      }
      hascomment=0; next
    }

    /^func \([^)]*\) [A-Z]/ {
      name=$0; sub(/^func \([^)]*\) /,"",name); sub(/[(\[].*/,"",name)
      if (name !~ EXEMPT && !hascomment) print F":"NR": Methode "name
      hascomment=0; next
    }
    /^func [A-Z]/ { name=$2; sub(/[(\[].*/,"",name); if(!hascomment) print F":"NR": func "name; hascomment=0; next }
    # Wie bei func/Methoden den Namen von Generics-Parametern und Mehrfach-Deklarationen
    # bereinigen, damit die Meldung den reinen Bezeichner nennt (type Foo[T any] -> Foo).
    /^type [A-Z]/ { name=$2; sub(/[(\[,].*/,"",name); if(!hascomment) print F":"NR": type "name; hascomment=0; next }
    /^(const|var) [A-Z]/ { name=$2; sub(/[(\[,].*/,"",name); if(!hascomment) print F":"NR": "$1" "name; hascomment=0; next }
    { hascomment=0 }
  ' "$file"
done < <(find . -name '*.go' ! -name '*_test.go' \
           -not -path './vendor/*' -not -path './node_modules/*' -not -path './.git/*') \
  | sort | tee "$gaps"
n=$(wc -l < "$gaps" | tr -d ' ')
echo "Undokumentierte exportierte Symbole: $n"
[ "$n" -eq 0 ] || exit 1
