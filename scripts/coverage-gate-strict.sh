#!/usr/bin/env bash
# Hartes Per-Datei-Coverage-Gate für fileee-mcp-server (siehe test-coverage-pflicht.md im
# homelab-management-Repo). Aufruf: ./scripts/coverage-gate-strict.sh cover.out datei:schwelle ...
# datei ist ein Präfix-Match gegen die Zeilen aus "go tool cover -func" (z.B. "cmd/fileee-mcp-server/auth.go").
set -euo pipefail

if [[ $# -lt 2 ]]; then
    echo "Usage: $0 <coverprofile> <datei-oder-präfix>:<schwelle> [...]" >&2
    exit 2
fi

coverprofile="$1"
shift

# Der komplette "go tool cover -func" Output wird EINMAL geholt (nicht pro Schwelle neu
# aufgerufen) — sonst würde ein Fehlschlag von "go tool cover" selbst (defektes Coverprofil)
# unter set -e/pipefail den Loop mittendrin abbrechen, statt für jede Schwelle ausgewertet
# zu werden.
func_output=$(go tool cover -func="$coverprofile")

fail=0
for spec in "$@"; do
    pfad="${spec%%:*}"
    schwelle="${spec##*:}"

    # WICHTIG: "|| true" verhindert, dass ein Kein-Treffer von grep (Exit 1) unter
    # set -euo pipefail die komplette Zuweisung — und damit das Script — an dieser Stelle
    # STILLSCHWEIGEND beendet (Finding: "Silent skip on filename mismatch"). Ein Tippfehler im
    # Pfad oder eine umbenannte/entfernte Datei in der Schwellen-Liste muss LAUT als FAIL
    # gemeldet werden, statt alle nachfolgenden Prüfungen kommentarlos zu überspringen.
    #
    # WICHTIG: "--" nach "-F" terminiert die Options-Erkennung von grep. Ohne "--" wird ein
    # führender Bindestrich in $pfad (z.B. "-fileee", Präfix von "go-fileee/...") als
    # Grep-Option interpretiert statt als literales Suchmuster — belegt live in dieser Umgebung
    # (grep ist auf ugrep gemapped): "grep -F "-fileee"" bricht mit "grep: ileee: No such file or
    # directory" ab (interpretiert als "-f ileee"), was via "|| true" zu einem FALSCHEN
    # "keine Coverage-Daten" führt, obwohl der Substring real im Profil vorhanden ist.
    treffer=$(printf '%s\n' "$func_output" | grep -F -- "$pfad" || true)

    if [[ -z "$treffer" ]]; then
        echo "FAIL: $pfad — keine Coverage-Daten (Datei im Profil nicht gefunden?)" >&2
        fail=1
        continue
    fi

    werte=$(printf '%s\n' "$treffer" | awk '{gsub("%","",$NF); sum+=$NF; count++} END {if (count>0) printf "%.1f", sum/count; else print "0.0"}')
    erfuellt=$(awk -v a="$werte" -v t="$schwelle" 'BEGIN{print (a+0 >= t+0) ? 1 : 0}')
    if [[ "$erfuellt" -ne 1 ]]; then
        echo "FAIL: $pfad Coverage ${werte}% < erforderlich ${schwelle}%" >&2
        fail=1
    else
        echo "OK:   $pfad Coverage ${werte}% >= erforderlich ${schwelle}%"
    fi
done

exit "$fail"
