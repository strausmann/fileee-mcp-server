package config

// version ist der Platzhalter fuer die Release-Version. Der Release-Workflow
// ueberschreibt ihn beim Container-Build per ldflags
// (-X main.version=${VERSION}); ohne Override bleibt es bei "dev".
//
// Bewusst eine Variable und keine Konstante: eine Konstante liefe erneut in das
// Problem aus strausmann/fileee-server#17, wo der hartkodierte Wert hinter dem
// tatsaechlichen Git-Tag zurueckblieb.
var version = "dev"

// Version liefert die Version dieses Binaries. Ein leerer ldflags-Wert — etwa aus
// einem Build ohne gesetztes VERSION-Build-Arg — wird zu "unknown" normalisiert,
// damit weder Log-Zeilen noch die MCP-Server-Kennung einen leeren String tragen.
func Version() string {
	if version == "" {
		return "unknown"
	}
	return version
}
