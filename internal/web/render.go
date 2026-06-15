package web

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"finador/internal/domain"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/style.css
var styleCSS []byte

//go:embed static/favicon.ico
var faviconICO []byte

var funcs = template.FuncMap{
	"fmtMoney": fmtMoney,
	"fmtNum":   fmtNum,
	"fmtPct":   fmtPct,
	"fmtDate":  fmtDate,
	"signe":    signe,
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
		http.Error(w, "template not found: "+page, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		fmt.Fprintf(w, "<!-- render error: %v -->", err)
	}
}

func (s *Server) renderError(w http.ResponseWriter, status int, msg string) {
	s.render(w, status, "error.html", map[string]any{"Message": msg, "Today": domain.Today()})
}

func (s *Server) stylesheet(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write(styleCSS)
}

func (s *Server) favicon(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(faviconICO)
}

// fmtMoney typesets an amount the English way: comma thousands, point decimal,
// currency symbol after a no-break space (U+00A0). Negative amounts use U+2212.
func fmtMoney(v float64, ccy domain.Currency) string {
	neg := v < 0
	if neg {
		v = -v
	}
	// round once to total cents to avoid carry loss (e.g. 0.995 -> "0.00")
	total := int64(v*100 + 0.5)
	whole, cents := total/100, total%100
	digits := fmt.Sprintf("%d", whole)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteRune(',')
		}
		b.WriteRune(r)
	}
	out := fmt.Sprintf("%s.%02d %s", b.String(), cents, symbol(ccy))
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

// fmtPct: "+2.00%" (no space before %).
func fmtPct(x float64) string {
	s := fmt.Sprintf("%+.2f", x*100)
	return strings.ReplaceAll(s, "-", "−") + "%"
}

// fmtNum: short decimal number (Sharpe 1.26).
func fmtNum(x float64) string {
	return strings.ReplaceAll(fmt.Sprintf("%.2f", x), "-", "−")
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

var enMonths = [...]string{"January", "February", "March", "April", "May", "June",
	"July", "August", "September", "October", "November", "December"}
var enDays = [...]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

// fmtDate: "Wednesday 10 June 2026".
func fmtDate(d domain.Date) string {
	t := d.Time()
	return fmt.Sprintf("%s %d %s %d", enDays[int(t.Weekday())], d.Day, enMonths[int(d.Month)-1], d.Year)
}
