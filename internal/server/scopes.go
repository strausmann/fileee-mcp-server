package server

import (
	"strings"

	"github.com/strausmann/fileee-mcp-server/internal/config"
	"github.com/strausmann/gangway/identity"
)

// scopeClaimNames sind die Claims, aus denen tokenScopes die vergebenen
// Scopes liest: "scope" nach RFC 8693 (ein einzelner, leerzeichengetrennter
// String -- die von OAuth-2.0-Resource-Servern allgemein erwartete Form)
// und "scp", wie Microsoft Entra ID delegierte Berechtigungen im
// Access-Token traegt (siehe docs/idp/entra-id.md, Abschnitt "Zu den
// Scopes"). Ein Aussteller setzt in der Praxis nur einen der beiden, aber
// nichts im Code haengt daran, dass es dabei bleibt -- beide werden gelesen
// und zusammengefuehrt.
var scopeClaimNames = [...]string{"scope", "scp"}

// tokenScopes liest die im Token vergebenen Scopes aus scopeClaimNames.
//
// Die ueblichen Auspraegungen sind ein einzelner, leerzeichengetrennter
// String (RFC 8693 fuer "scope", Entra fuer "scp") -- dieser Fall wird per
// strings.Fields aufgespalten. Eine JSON-Liste wird defensiv ebenfalls
// akzeptiert, falls ein anderer Aussteller den Claim so befuellt (derselbe
// Grundsatz wie bei claimStrings fuer den Capability-Claim: encoding/json
// dekodiert eine Liste beim Dekodieren nach map[string]any immer als
// []any, nie als []string -- der []string-Zweig deckt eine von Hand
// gebaute Identity ab, wie sie TestScopesSatisfied verwendet).
func tokenScopes(claims map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, key := range scopeClaimNames {
		switch v := claims[key].(type) {
		case string:
			for _, s := range strings.Fields(v) {
				out[s] = true
			}
		case []any:
			for _, e := range v {
				if s, ok := e.(string); ok {
					out[s] = true
				}
			}
		case []string:
			for _, s := range v {
				out[s] = true
			}
		}
	}
	return out
}

// scopesSatisfied prueft, ob id alle in cfg.OIDCRequiredScopes geforderten
// Scopes traegt.
//
// Ist die Liste leer (MCP_OIDC_REQUIRED_SCOPES nicht gesetzt), ist jeder
// Aufrufer erlaubt -- das unveraenderte Verhalten dieses Servers: die
// Einstellung wurde zwar in config.go/LoadConfig aus der Umgebung gelesen,
// aber vor dieser Aenderung nirgends ausgewertet (Pruefbefund zu dieser
// Aufgabe). Ein Betreiber, der die Variable nie setzt, sieht also weiterhin
// exakt das bisherige Verhalten.
//
// id == nil bei einer konfigurierten Pflicht-Liste ist fail-closed: keine
// verifizierte Identitaet, keine Claims, keine Scopes, keine Freigabe.
// Praktisch sollte Gangway den Selector nie mit einer unverifizierten
// Identitaet aufrufen (derselbe Fall wie in capabilitiesFor, dort ebenso
// dokumentiert) -- dieser Zweig ist wie dort reine Verteidigung gegen einen
// theoretischen Fall, kein regulaerer Pfad.
func scopesSatisfied(cfg *config.Config, id *identity.Identity) bool {
	if len(cfg.OIDCRequiredScopes) == 0 {
		return true
	}
	if id == nil {
		return false
	}
	granted := tokenScopes(id.Claims)
	for _, required := range cfg.OIDCRequiredScopes {
		if !granted[required] {
			return false
		}
	}
	return true
}

// missingScopes reports which of cfg.OIDCRequiredScopes id's token does
// not carry — for the diagnostic log line AttachMCPSelector writes when
// scopesSatisfied rejects a caller (internal/server/server.go), so an
// operator sees WHICH scope was missing, not just that "something" was.
//
// This deliberately duplicates scopesSatisfied's own granted-scope
// comparison instead of having scopesSatisfied return it: scopesSatisfied
// is the actual authorization decision AttachMCPSelector's fail-closed
// branch depends on, and changing its signature only to serve a logging
// need would put logging code on the same line as a security decision —
// see internal/diag's own doc comment on why this task keeps that
// boundary sharp. missingScopes must never influence what
// scopesSatisfied decides, only describe it after the fact.
func missingScopes(cfg *config.Config, id *identity.Identity) []string {
	if len(cfg.OIDCRequiredScopes) == 0 {
		return nil
	}
	var granted map[string]bool
	if id != nil {
		granted = tokenScopes(id.Claims)
	}
	var missing []string
	for _, required := range cfg.OIDCRequiredScopes {
		if !granted[required] {
			missing = append(missing, required)
		}
	}
	return missing
}
