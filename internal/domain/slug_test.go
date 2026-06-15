package domain

import "testing"

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"PEA Zephyr":     "pea-zephyr",
		"CTO Meridia":         "cto-meridia",
		"Maison à Achères": "maison-a-acheres",
		"CW8.PA":           "cw8-pa",
		"  défi  élevé!  ": "defi-eleve",
	} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, attendu %q", in, got, want)
		}
	}
}
