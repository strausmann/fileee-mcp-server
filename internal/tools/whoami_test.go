package tools

import "testing"

func TestMaskUsername(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"email", "bjoern@strausmann.net", "b***n@strausmann.net"},
		{"email short local", "a@strausmann.net", "*@strausmann.net"},
		{"email two-char local", "ab@x.de", "a***b@x.de"},
		{"no at", "operator", "o***r"},
		{"single char", "x", "*"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := maskUsername(c.in); got != c.want {
				t.Fatalf("maskUsername(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
