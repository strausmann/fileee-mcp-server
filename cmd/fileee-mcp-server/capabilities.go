package main

import (
	"fmt"
	"strings"
)

// Capability benennt eine Gruppe von Tools, die gemeinsam freigeschaltet werden.
// Die Werte sind zugleich die Bezeichner in `FILEEE_CAPABILITIES` und die
// erwarteten Rollen- bzw. Gruppennamen im IdP.
type Capability string

// Die vier Capability-Gruppen. Siehe ADR-0011 fuer die Begruendung des Zuschnitts.
const (
	// CapRead umfasst alle rein lesenden Tools.
	CapRead Capability = "read"
	// CapWrite umfasst Upload sowie das Anlegen und Aendern von Entitaeten.
	CapWrite Capability = "write"
	// CapShare umfasst Freigaben, Export und Konversationen.
	CapShare Capability = "share"
	// CapDestructive umfasst die unwiderruflichen Hard-DELETEs. Ueber einen
	// IdP-Claim ist diese Gruppe bewusst nicht erreichbar.
	CapDestructive Capability = "destructive"
)

// canonical legt die Reihenfolge fest, in der Set.String die Gruppen ausgibt —
// aufsteigend nach Eingriffstiefe, nicht alphabetisch. Damit ist die
// Zeichenkette eines Sets stabil und als Schluessel verwendbar.
var canonical = []Capability{CapRead, CapWrite, CapShare, CapDestructive}

// bit ordnet jeder Capability ihr Bit in Set.bits zu.
func (c Capability) bit() (uint8, bool) {
	for i, known := range canonical {
		if known == c {
			return 1 << uint(i), true
		}
	}
	return 0, false
}

// Set ist eine Menge von Capability-Werten. Der Wert ist vergleichbar und
// eignet sich damit als Map-Schluessel — der Server haelt je Set genau eine
// MCP-Server-Instanz vor.
type Set struct{ bits uint8 }

// ParseCapabilities liest eine kommaseparierte Liste von Capability-Namen.
// Leerraum wird getrimmt, Dopplungen werden zusammengefasst, ein leerer String
// ergibt die leere Menge. Ein unbekannter Name ist ein Fehler und kein still
// ignorierter Eintrag: ein Tippfehler in der Konfiguration soll auffallen und
// nicht unbemerkt zu weniger Rechten fuehren.
func ParseCapabilities(raw string) (Set, error) {
	var s Set
	for _, teil := range strings.Split(raw, ",") {
		name := strings.TrimSpace(teil)
		if name == "" {
			continue
		}
		bit, ok := Capability(name).bit()
		if !ok {
			return Set{}, fmt.Errorf("unbekannte capability %q — erlaubt sind %s",
				name, joinCapabilities(canonical))
		}
		s.bits |= bit
	}
	return s, nil
}

// Has meldet, ob die Gruppe in der Menge enthalten ist.
func (s Set) Has(c Capability) bool {
	bit, ok := c.bit()
	return ok && s.bits&bit != 0
}

// IsEmpty meldet, ob keine einzige Gruppe enthalten ist.
func (s Set) IsEmpty() bool { return s.bits == 0 }

// Intersect liefert die Schnittmenge beider Mengen.
func (s Set) Intersect(other Set) Set { return Set{bits: s.bits & other.bits} }

// Without liefert die Menge ohne die angegebene Gruppe.
func (s Set) Without(c Capability) Set {
	bit, ok := c.bit()
	if !ok {
		return s
	}
	return Set{bits: s.bits &^ bit}
}

// String gibt die enthaltenen Gruppen kommasepariert in kanonischer Reihenfolge
// aus. Die leere Menge ergibt den leeren String.
func (s Set) String() string {
	var enthalten []Capability
	for _, c := range canonical {
		if s.Has(c) {
			enthalten = append(enthalten, c)
		}
	}
	return joinCapabilities(enthalten)
}

// Resolution buendelt die drei moeglichen Quellen des Funktionsumfangs fuer
// einen einzelnen Zugriff.
type Resolution struct {
	// Global ist die Obergrenze aus FILEEE_CAPABILITIES.
	Global Set
	// ClaimConfigured gibt an, ob MCP_OIDC_CAPABILITY_CLAIM gesetzt ist.
	ClaimConfigured bool
	// ClaimValues sind die Rohwerte aus dem Token. Sie enthalten typischerweise
	// auch Gruppen, die nichts mit diesem Server zu tun haben.
	ClaimValues []string
	// Account ist die Einstellung des aufgeloesten Kontos.
	Account Set
	// HasAccount gibt an, ob fuer das Konto ueberhaupt eine Einstellung existiert.
	HasAccount bool
}

// Resolve bestimmt den effektiven Funktionsumfang nach der in ADR-0011
// festgelegten Rangfolge: die globale Einstellung ist die Obergrenze, darunter
// gewinnt der IdP-Claim vor der Konto-Einstellung.
//
// Ist der Claim konfiguriert, traegt das Token aber keinen bekannten Wert,
// ergibt die Auswertung `read` — und faellt bewusst NICHT auf die
// Konto-Einstellung oder die Obergrenze zurueck. Andernfalls hiesse "keine
// Rolle zugewiesen" Vollzugriff, und eine vergessene Zuweisung im IdP waere
// eine stille Rechteausweitung.
//
// CapDestructive ist ueber den Claim nicht erreichbar; ein solcher Wert im
// Token wird ignoriert.
func Resolve(r Resolution) Set {
	if r.ClaimConfigured {
		var ausClaim Set
		for _, wert := range r.ClaimValues {
			// Unbekannte Werte werden uebergangen, nicht als Fehler behandelt:
			// der groups-Claim enthaelt alle Gruppen eines Benutzers, nicht nur
			// die fuer diesen Server gedachten.
			if bit, ok := Capability(strings.TrimSpace(wert)).bit(); ok {
				ausClaim.bits |= bit
			}
		}
		ausClaim = ausClaim.Without(CapDestructive)

		if ausClaim.IsEmpty() {
			ausClaim, _ = ParseCapabilities(string(CapRead))
		}
		return ausClaim.Intersect(r.Global)
	}

	if r.HasAccount {
		return r.Account.Intersect(r.Global)
	}
	return r.Global
}

func joinCapabilities(caps []Capability) string {
	namen := make([]string, 0, len(caps))
	for _, c := range caps {
		namen = append(namen, string(c))
	}
	return strings.Join(namen, ",")
}
