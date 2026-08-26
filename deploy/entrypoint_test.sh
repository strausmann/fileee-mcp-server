#!/bin/sh
# Prueft die Entscheidung in entrypoint.sh ohne Container: Attrappen fuer
# `infisical` und `fileee-mcp-server` liegen in einem eigenen Verzeichnis, das
# dem PATH vorangestellt wird. Jede Attrappe schreibt ihren Aufruf in eine
# Datei, die der Test danach liest.
#
# Bewusst OHNE Subshells je Fall: Ein Fehlschlag in einer Subshell wuerde den
# Zaehler nicht erhoehen, das Skript meldete trotzdem "bestanden" — eine
# Pruefung, die nur so aussieht, als pruefe sie.
set -eu

hier="$(cd "$(dirname "$0")" && pwd)"
arbeit="$(mktemp -d)"
trap 'rm -rf "$arbeit"' EXIT

mkdir -p "$arbeit/bin"
protokoll="$arbeit/aufrufe"

cat >"$arbeit/bin/infisical" <<'ATTRAPPE'
#!/bin/sh
if [ "${1:-}" = "login" ]; then
	echo "attrappen-token"
	exit 0
fi
echo "infisical $*" >>"$AUFRUF_PROTOKOLL"
exit 0
ATTRAPPE

cat >"$arbeit/bin/fileee-mcp-server" <<'ATTRAPPE'
#!/bin/sh
echo "server $*" >>"$AUFRUF_PROTOKOLL"
exit 0
ATTRAPPE

chmod +x "$arbeit/bin/infisical" "$arbeit/bin/fileee-mcp-server"

fehler=0

# starte <env-zuweisungen...> -- laeuft entrypoint.sh mit genau diesen
# Variablen, sonst keinen. `env -i` schliesst aus, dass eine Variable aus der
# Umgebung des Testlaufs das Ergebnis faelscht.
starte() {
	: >"$protokoll"
	set +e
	ausgabe="$(env -i \
		PATH="$arbeit/bin:/usr/bin:/bin" \
		AUFRUF_PROTOKOLL="$protokoll" \
		"$@" \
		sh "$hier/entrypoint.sh" 2>&1)"
	ist_exit=$?
	set -e
}

pruefe_exit() {
	if [ "$ist_exit" != "$1" ]; then
		echo "FEHLER $2: Exit $ist_exit, erwartet $1"
		echo "       Ausgabe: $ausgabe"
		fehler=$((fehler + 1))
		return 1
	fi
	return 0
}

pruefe_protokoll() {
	if ! grep -q -- "$1" "$protokoll" 2>/dev/null; then
		echo "FEHLER $2: '$1' fehlt im Protokoll"
		echo "       Protokoll: $(cat "$protokoll" 2>/dev/null)"
		fehler=$((fehler + 1))
		return 1
	fi
	return 0
}

bestanden() { echo "ok    $1"; }

# --- Fall 1: keine Infisical-Variablen -> direkter Start ---------------------
starte
if pruefe_exit 0 "direkter Start" && pruefe_protokoll "^server" "direkter Start"; then
	bestanden "direkter Start"
fi

# --- Fall 2: beide Zugangsdaten -> Anmeldung, dann infisical run ------------
starte INFISICAL_UNIVERSAL_AUTH_CLIENT_ID=kennung INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET=geheim
if pruefe_exit 0 "Infisical-Weg" && pruefe_protokoll "^infisical run" "Infisical-Weg"; then
	bestanden "Infisical-Weg"
fi

# --- Fall 3: fertiges Token schlaegt die Anmeldung --------------------------
starte INFISICAL_TOKEN=vorhanden
if pruefe_exit 0 "Token-Weg" && pruefe_protokoll "^infisical run" "Token-Weg"; then
	bestanden "Token-Weg"
fi

# --- Fall 4/5: halb gesetztes Paar -> Abbruch mit Meldung -------------------
starte INFISICAL_UNIVERSAL_AUTH_CLIENT_ID=kennung
if pruefe_exit 1 "halbes Paar (Kennung)"; then
	case "$ausgabe" in
	*INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET*) bestanden "halbes Paar (Kennung)" ;;
	*)
		echo "FEHLER halbes Paar (Kennung): Meldung nennt die fehlende Variable nicht"
		echo "       Ausgabe: $ausgabe"
		fehler=$((fehler + 1))
		;;
	esac
fi

starte INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET=geheim
if pruefe_exit 1 "halbes Paar (Geheimnis)"; then
	case "$ausgabe" in
	*INFISICAL_UNIVERSAL_AUTH_CLIENT_ID*) bestanden "halbes Paar (Geheimnis)" ;;
	*)
		echo "FEHLER halbes Paar (Geheimnis): Meldung nennt die fehlende Variable nicht"
		echo "       Ausgabe: $ausgabe"
		fehler=$((fehler + 1))
		;;
	esac
fi

# --- Fall 6: optionale Flags werden durchgereicht ---------------------------
starte INFISICAL_TOKEN=vorhanden INFISICAL_ENVIRONMENT=dev INFISICAL_SECRET_PATH=/entra-id
if pruefe_exit 0 "Flags" &&
	pruefe_protokoll -- "--env=dev" "Flags" &&
	pruefe_protokoll -- "--path=/entra-id" "Flags"; then
	bestanden "Flags"
fi

# --- Fall 7: ohne gesetzte Flags bleiben sie weg ----------------------------
starte INFISICAL_TOKEN=vorhanden
if pruefe_exit 0 "keine Flags"; then
	if grep -q -- "--env=" "$protokoll"; then
		echo "FEHLER keine Flags: --env= steht im Aufruf, obwohl nichts gesetzt war"
		echo "       Protokoll: $(cat "$protokoll")"
		fehler=$((fehler + 1))
	else
		bestanden "keine Flags"
	fi
fi

echo
if [ "$fehler" -ne 0 ]; then
	echo "Fehlgeschlagen: $fehler"
	exit 1
fi
echo "Alle Faelle bestanden."
