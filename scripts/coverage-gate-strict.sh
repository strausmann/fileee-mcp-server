#!/usr/bin/env bash
# Hartes Per-Datei-Coverage-Gate (siehe test-coverage-pflicht.md im homelab-management-Repo).
# Aufruf: ./scripts/coverage-gate-strict.sh cover.out datei:schwelle ...
# "datei" ist ein Substring-Match gegen die Dateipfade im Coverprofil,
# z.B. "cmd/fileee-mcp-server/auth_oidc.go".
#
# Die Coverage wird STATEMENT-GEWICHTET direkt aus dem Coverprofil berechnet, nicht als
# arithmetisches Mittel der Funktionsprozente aus "go tool cover -func". Das ist der
# Unterschied zwischen "wie viel Code ist abgedeckt" und "wie gut ist die durchschnittliche
# Funktion abgedeckt": eine Datei mit einer grossen, ungetesteten Funktion und vielen kleinen,
# voll getesteten Funktionen besteht ein ungewichtetes Mittel muehelos, obwohl der Grossteil
# der Statements ungeprueft ist. Fuer ein Gate ist nur die gewichtete Zahl aussagekraeftig.
#
# Coverprofil-Format (eine Zeile je Block):
#   <pfad>:<startZeile>.<startSpalte>,<endZeile>.<endSpalte> <anzahlStatements> <trefferzahl>
# Datei-Coverage = Summe der Statements mit Trefferzahl > 0 / Summe aller Statements.
set -euo pipefail

if [[ $# -lt 2 ]]; then
    echo "Usage: $0 <coverprofile> <datei-oder-präfix>:<schwelle> [...]" >&2
    exit 2
fi

coverprofile="$1"
shift

if [[ ! -r "$coverprofile" ]]; then
    echo "FAIL: Coverprofil '$coverprofile' nicht lesbar" >&2
    exit 1
fi

fail=0
for spec in "$@"; do
    pfad="${spec%%:*}"
    schwelle="${spec##*:}"

    # Substring-Match in awk statt grep: das umgeht sowohl die Options-Erkennung von grep bei
    # fuehrendem Bindestrich im Pfad als auch das Risiko, dass ein Kein-Treffer unter
    # set -euo pipefail die Auswertung STILLSCHWEIGEND beendet. Ein Tippfehler im Pfad oder eine
    # umbenannte Datei muss LAUT als FAIL erscheinen, nicht als uebersprungene Pruefung.
    read -r gefunden werte < <(
        awk -v pfad="$pfad" '
            NR == 1 && /^mode:/ { next }
            {
                # Alles vor dem letzten ":" ist der Dateipfad — Doppelpunkte im Pfad selbst
                # (Windows-Laufwerksbuchstaben, exotische Modulpfade) bleiben so intakt.
                pos = index($1, ".go:")
                if (pos == 0) next
                datei = substr($1, 1, pos + 2)
                if (index(datei, pfad) == 0) next
                gefunden = 1
                gesamt += $2
                if ($3 > 0) abgedeckt += $2
            }
            END {
                if (!gefunden || gesamt == 0) { print 0, "0.0"; exit }
                printf "1 %.1f\n", (abgedeckt * 100.0) / gesamt
            }
        ' "$coverprofile"
    )

    if [[ "$gefunden" -ne 1 ]]; then
        echo "FAIL: $pfad — keine Coverage-Daten (Datei im Profil nicht gefunden?)" >&2
        fail=1
        continue
    fi

    erfuellt=$(awk -v a="$werte" -v t="$schwelle" 'BEGIN{print (a+0 >= t+0) ? 1 : 0}')
    if [[ "$erfuellt" -ne 1 ]]; then
        echo "FAIL: $pfad Coverage ${werte}% < erforderlich ${schwelle}%" >&2
        fail=1
    else
        echo "OK:   $pfad Coverage ${werte}% >= erforderlich ${schwelle}%"
    fi
done

exit "$fail"
