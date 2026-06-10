package web

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"finador/internal/domain"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/style.css
var styleCSS []byte

var funcs = template.FuncMap{
	"frMoney": frMoney,
	"frPct":   frPct,
	"frDate":  frDate,
	"frDelta": frDelta,
	"signe":   signe,
}

// pages maps page filename → a clone of base with the page parsed in.
// Each page gets its own clone so its "title" and "main" blocks are independent.
var pages = map[string]*template.Template{}

func init() {
	baseSrc := template.Must(template.New("base").Funcs(funcs).ParseFS(templateFS, "templates/base.html"))
	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		name := e.Name()
		if name == "base.html" {
			continue
		}
		clone := template.Must(baseSrc.Clone())
		pages[name] = template.Must(clone.ParseFS(templateFS, "templates/"+name))
	}
}

// render executes a page template inside base.html.
func (s *Server) render(w http.ResponseWriter, status int, page string, data any) {
	tmpl, ok := pages[page]
	if !ok {
		http.Error(w, "template introuvable: "+page, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		fmt.Fprintf(w, "<!-- erreur de rendu: %v -->", err)
	}
}

func (s *Server) renderError(w http.ResponseWriter, status int, msg string) {
	s.render(w, status, "error.html", map[string]any{"Message": msg, "Aujourdhui": domain.Today()})
}

func (s *Server) stylesheet(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write(styleCSS)
}

// frMoney typesets an amount the French way: thin no-break thousands (U+202F),
// decimal comma, currency symbol after a no-break space (U+00A0).
func frMoney(v float64, ccy domain.Currency) string {
	neg := v < 0
	if neg {
		v = -v
	}
	whole := int64(v)
	cents := int64(v*100+0.5) % 100
	digits := fmt.Sprintf("%d", whole)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteRune(' ') // U+202F NARROW NO-BREAK SPACE
		}
		b.WriteRune(r)
	}
	out := fmt.Sprintf("%s,%02d %s", b.String(), cents, symbol(ccy))
	if neg {
		return "−" + out
	}
	return out
}

func symbol(c domain.Currency) string {
	switch c {
	case domain.EUR:
		return "€"
	case domain.USD:
		return "$"
	case "GBP":
		return "£"
	}
	return string(c)
}

// frPct: « +2,00 % » (espace fine avant %).
func frPct(x float64) string {
	s := fmt.Sprintf("%+.2f", x*100)
	return strings.ReplaceAll(s, ".", ",") + " %"
}

func signe(x float64) string {
	switch {
	case x > 0:
		return "pos"
	case x < 0:
		return "neg"
	}
	return ""
}

func frDelta(x float64) string { return frPct(x) }

var frMonths = [...]string{"janvier", "février", "mars", "avril", "mai", "juin",
	"juillet", "août", "septembre", "octobre", "novembre", "décembre"}
var frDays = [...]string{"dimanche", "lundi", "mardi", "mercredi", "jeudi", "vendredi", "samedi"}

// frDate: « mercredi 10 juin 2026 ».
func frDate(d domain.Date) string {
	t := d.Time()
	return fmt.Sprintf("%s %d %s %d", frDays[int(t.Weekday())], d.Day, frMonths[int(d.Month)-1], d.Year)
}

var _ = time.Now // (gardé si besoin d'horodatage ultérieur)
