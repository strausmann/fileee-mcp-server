#!/bin/sh
# Startet fileee-mcp-server — mit Infisical davor, wenn dessen Zugangsdaten
# gesetzt sind, sonst direkt.
#
# Warum ueberhaupt eine Entscheidung: Der Server soll auch dem taugen, der
# weder Identity Provider noch Secret-Verwaltung betreibt (ADR-0010). Ein fest
# verdrahtetes `infisical run` im ENTRYPOINT macht das unmoeglich — wer das
# Abbild aus der Registry zieht und seine Werte als schlichte
# Umgebungsvariablen setzt, bekaeme einen Container, der beim Start scheitert.
#
# Erkannt wird an den Infisical-Variablen selbst; einen zusaetzlichen Schalter
# gibt es bewusst nicht.
#
# Aufruf ueber PATH statt ueber absolute Pfade: So laesst sich das Skript ohne
# Container pruefen, indem ein Test Attrappen in einem eigenen Verzeichnis
# davorhaengt.
set -eu

client_id="${INFISICAL_UNIVERSAL_AUTH_CLIENT_ID:-}"
client_secret="${INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET:-}"
token="${INFISICAL_TOKEN:-}"

# Sammelt die optionalen Infisical-Flags ein. Jedes bleibt weg, wenn die
# zugehoerige Variable leer ist — `infisical` faellt dann auf seine eigenen
# Vorgaben zurueck.
infisical_flags() {
	[ -n "${INFISICAL_API_URL:-}" ] && printf ' --domain=%s' "$INFISICAL_API_URL"
	[ -n "${INFISICAL_PROJECT_ID:-}" ] && printf ' --projectId=%s' "$INFISICAL_PROJECT_ID"
	[ -n "${INFISICAL_ENVIRONMENT:-}" ] && printf ' --env=%s' "$INFISICAL_ENVIRONMENT"
	[ -n "${INFISICAL_SECRET_PATH:-}" ] && printf ' --path=%s' "$INFISICAL_SECRET_PATH"
	return 0
}

# Halb gesetzte Zugangsdaten sind nie Absicht, sondern ein Tippfehler oder ein
# vergessenes Geheimnis. Still auf den Umgebungs-Weg zu fallen waere der
# schlechteste Ausgang: Der Container startet, findet keine Werte und scheitert
# spaeter an einer Stelle, die nichts mit der Ursache zu tun hat.
if [ -n "$client_id" ] && [ -z "$client_secret" ]; then
	echo "fileee-mcp-server: INFISICAL_UNIVERSAL_AUTH_CLIENT_ID ist gesetzt, INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET fehlt." >&2
	echo "  Entweder beide setzen (Infisical-Weg) oder beide weglassen (schlichte Umgebungsvariablen)." >&2
	exit 1
fi
if [ -z "$client_id" ] && [ -n "$client_secret" ]; then
	echo "fileee-mcp-server: INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET ist gesetzt, INFISICAL_UNIVERSAL_AUTH_CLIENT_ID fehlt." >&2
	echo "  Entweder beide setzen (Infisical-Weg) oder beide weglassen (schlichte Umgebungsvariablen)." >&2
	exit 1
fi

# Ein fertiges Token schlaegt die Anmeldung — dann gibt es nichts zu holen.
if [ -n "$token" ]; then
	# shellcheck disable=SC2046 # Flags sollen hier in Woerter zerfallen
	exec infisical run $(infisical_flags) -- fileee-mcp-server "$@"
fi

if [ -n "$client_id" ]; then
	# Zwei Schritte, weil `infisical run` die Universal-Auth-Variablen NICHT
	# selbst auswertet: Ohne vorherige Anmeldung faellt es auf den
	# interaktiven Anmeldeweg zurueck und bleibt stehen.
	#
	# Die Ausgabe geht ausschliesslich in eine Variable — das Token wird nie
	# gedruckt, nicht umgeleitet und nicht in eine Datei geschrieben.
	INFISICAL_TOKEN="$(infisical login --method=universal-auth \
		--client-id="$client_id" \
		--client-secret="$client_secret" \
		--domain="${INFISICAL_API_URL:-}" \
		--silent --plain)"
	export INFISICAL_TOKEN
	# Das Geheimnis wird nach der Anmeldung nicht mehr gebraucht und hat im
	# Environment des Serverprozesses nichts verloren.
	unset INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET

	# shellcheck disable=SC2046 # Flags sollen hier in Woerter zerfallen
	exec infisical run $(infisical_flags) -- fileee-mcp-server "$@"
fi

exec fileee-mcp-server "$@"
