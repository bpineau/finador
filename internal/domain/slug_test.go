package domain

import "testing"

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"PEA BforBank":     "pea-bforbank",
		"CTO Saxo":         "cto-saxo",
		"Maison à Rénover": "maison-a-renover",
		"CW8.PA":           "cw8-pa",
		"  défi  élevé!  ": "defi-eleve",
	} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, attendu %q", in, got, want)
		}
	}
}
