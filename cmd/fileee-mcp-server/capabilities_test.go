package main

import "testing"

func TestParseCapabilitiesAkzeptiertGueltigeListen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"einzeln", "read", "read"},
		{"mehrere", "read,write", "read,write"},
		{"alle", "read,write,share,destructive", "read,write,share,destructive"},
		{"unsortiert wird kanonisiert", "share,read", "read,share"},
		{"leerraum wird getrimmt", " read , write ", "read,write"},
		{"doppelte werte werden zusammengefasst", "read,read,write", "read,write"},
		{"leerer string ergibt leere menge", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseCapabilities(tt.raw)
			if err != nil {
				t.Fatalf("ParseCapabilities(%q) = Fehler %v, erwartet kein Fehler", tt.raw, err)
			}
			if got.String() != tt.want {
				t.Fatalf("ParseCapabilities(%q).String() = %q, erwartet %q", tt.raw, got.String(), tt.want)
			}
		})
	}
}

func TestParseCapabilitiesLehntUnbekannteWerteAb(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"lesen", "read,admin", "READ", "read;write"} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			if _, err := ParseCapabilities(raw); err == nil {
				t.Fatalf("ParseCapabilities(%q) = kein Fehler, erwartet Fehler — ein Tippfehler in der "+
					"Konfiguration darf nicht stillschweigend zu weniger Rechten fuehren", raw)
			}
		})
	}
}

func TestSetHasUndIntersect(t *testing.T) {
	t.Parallel()

	rw := mustParse(t, "read,write")
	rs := mustParse(t, "read,share")

	if !rw.Has(CapWrite) {
		t.Error("Has(CapWrite) = false, erwartet true")
	}
	if rw.Has(CapShare) {
		t.Error("Has(CapShare) = true, erwartet false")
	}
	if got := rw.Intersect(rs).String(); got != "read" {
		t.Fatalf("Intersect = %q, erwartet %q", got, "read")
	}
	if got := rw.Without(CapWrite).String(); got != "read" {
		t.Fatalf("Without(CapWrite) = %q, erwartet %q", got, "read")
	}
	if !mustParse(t, "").IsEmpty() {
		t.Error("leere Menge meldet IsEmpty() = false")
	}
}

// Die Faelle entsprechen der Rangfolge aus ADR-0011.
func TestResolveRangfolge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   Resolution
		want string
	}{
		{
			name: "stufe 4: nur die obergrenze",
			in:   Resolution{Global: mustParse(t, "read,write")},
			want: "read,write",
		},
		{
			name: "stufe 3: konto schraenkt ein",
			in: Resolution{
				Global:     mustParse(t, "read,write,share"),
				Account:    mustParse(t, "read"),
				HasAccount: true,
			},
			want: "read",
		},
		{
			name: "stufe 2: claim gewinnt gegen die konto-einstellung",
			in: Resolution{
				Global:          mustParse(t, "read,write,share"),
				ClaimConfigured: true,
				ClaimValues:     []string{"read", "write"},
				Account:         mustParse(t, "read"),
				HasAccount:      true,
			},
			want: "read,write",
		},
		{
			name: "stufe 1: der claim kann die obergrenze nicht ueberschreiten",
			in: Resolution{
				Global:          mustParse(t, "read"),
				ClaimConfigured: true,
				ClaimValues:     []string{"read", "write", "share"},
			},
			want: "read",
		},
		{
			name: "fail-closed: claim konfiguriert, aber im token unbelegt",
			in: Resolution{
				Global:          mustParse(t, "read,write,share"),
				ClaimConfigured: true,
				ClaimValues:     nil,
				Account:         mustParse(t, "read,write"),
				HasAccount:      true,
			},
			want: "read",
		},
		{
			name: "fail-closed: claim enthaelt nur unbekannte werte",
			in: Resolution{
				Global:          mustParse(t, "read,write"),
				ClaimConfigured: true,
				ClaimValues:     []string{"Admins", "Domain Users"},
			},
			want: "read",
		},
		{
			name: "fail-closed greift auch, wenn read global gesperrt ist",
			in: Resolution{
				Global:          mustParse(t, "write"),
				ClaimConfigured: true,
				ClaimValues:     nil,
			},
			want: "",
		},
		{
			name: "destructive ist ueber den claim nicht erreichbar",
			in: Resolution{
				Global:          mustParse(t, "read,write,destructive"),
				ClaimConfigured: true,
				ClaimValues:     []string{"read", "destructive"},
			},
			want: "read",
		},
		{
			name: "destructive bleibt ohne claim erhalten",
			in:   Resolution{Global: mustParse(t, "read,destructive")},
			want: "read,destructive",
		},
		{
			name: "fremde gruppen im claim werden ignoriert, bekannte zaehlen",
			in: Resolution{
				Global:          mustParse(t, "read,write,share"),
				ClaimConfigured: true,
				ClaimValues:     []string{"Domain Users", "write", "irgendwas"},
			},
			want: "write",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Resolve(tt.in).String(); got != tt.want {
				t.Fatalf("Resolve() = %q, erwartet %q", got, tt.want)
			}
		})
	}
}

func mustParse(t *testing.T, raw string) Set {
	t.Helper()

	s, err := ParseCapabilities(raw)
	if err != nil {
		t.Fatalf("ParseCapabilities(%q) unerwartet fehlgeschlagen: %v", raw, err)
	}
	return s
}
