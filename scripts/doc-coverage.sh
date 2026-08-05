#!/usr/bin/env bash
# Prüft, dass jedes EXPORTIERTE Symbol (Typ/Funktion/Methode/Const/Var) einen Doc-Comment trägt.
# Ausgenommen sind Standard-Interface-Methoden, die konventionell keinen Kommentar brauchen.
set -euo pipefail
cd "$(dirname "$0")/.."
exempt='^(MarshalJSON|UnmarshalJSON|String|Error)$'
gaps="$(mktemp)"
trap 'rm -f "$gaps"' EXIT
while IFS= read -r file; do
  awk -v F="$file" -v EXEMPT="$exempt" '
    /^\/\// { hascomment=1; next }
    /^func \([^)]*\) [A-Z]/ {
      name=$0; sub(/^func \([^)]*\) /,"",name); sub(/[(\[].*/,"",name)
      if (name !~ EXEMPT && !hascomment) print F":"NR": Methode "name
      hascomment=0; next
    }
    /^func [A-Z]/ { name=$2; sub(/[(\[].*/,"",name); if(!hascomment) print F":"NR": func "name; hascomment=0; next }
    /^type [A-Z]/ { if(!hascomment) print F":"NR": type "$2; hascomment=0; next }
    /^(const|var) [A-Z]/ { if(!hascomment) print F":"NR": "$1" "$2; hascomment=0; next }
    { hascomment=0 }
  ' "$file"
done < <(find cmd -name '*.go' ! -name '*_test.go') | sort | tee "$gaps"
n=$(wc -l < "$gaps" | tr -d ' ')
echo "Undokumentierte exportierte Symbole: $n"
[ "$n" -eq 0 ] || exit 1
