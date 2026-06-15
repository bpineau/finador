package domain

import "strings"

var accents = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i", "ô", "o", "ö", "o", "ù", "u", "û", "u", "ü", "u", "ç", "c",
)

// Slugify turns a free-form name into a stable identifier:
// "PEA Zephyr" → "pea-zephyr", "Maison à Rénover" → "maison-a-renover".
func Slugify(name string) string {
	s := accents.Replace(strings.ToLower(name))
	var out []rune
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			out = append(out, r)
		case len(out) > 0 && out[len(out)-1] != '-':
			out = append(out, '-')
		}
	}
	return strings.TrimSuffix(string(out), "-")
}
