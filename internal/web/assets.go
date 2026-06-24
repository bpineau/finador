package web

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"finador/internal/chart"
	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/perf"
	"finador/internal/portfolio"
)

// sparkInk is the sparkline stroke color: the body ink (= CSS --encre), so the
// sparklines read as quiet text rather than green/red signals.
const sparkInk = "#1c1914"

type assetRow struct {
	Name, URL, EditURL string
	Day1d              float64
	HasDay1d           bool
	Spark1M, Spark1Y   template.HTML
	Gross, Net         float64
}

type assetSection struct {
	Group, URL   string
	Gross, Net   float64
	Rows         []assetRow
	PropertyOnly bool
}

type assetsData struct {
	Today    domain.Date
	Ccy      domain.Currency
	Sections []assetSection
	Assets   []*domain.Asset
	Warnings []string
	Flash    string
	Error    string
}

func (s *Server) assetsPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.renderAssetsPage(w, http.StatusOK, r.URL.Query().Get("flash"), r.URL.Query().Get("error"))
}

// renderAssetsPage builds the per-asset valuation table and the create form. It is
// called with the lock (R or W) already held.
func (s *Server) renderAssetsPage(w http.ResponseWriter, status int, flash, errMsg string) {
	b := s.file.Book
	today := domain.Today()
	ccy := b.DisplayCurrency()
	fx := market.Converter{FX: b.Market.FX}

	data := assetsData{Today: today, Ccy: ccy, Assets: b.Assets, Flash: flash, Error: errMsg}
	bySection := map[string]*assetSection{}
	var rawWarnings []string
	rf := perf.RiskFreeFromConfig(b.Config)

	for _, asset := range b.Assets {
		scope, err := portfolio.ParseScope(b, string(asset.ID))
		if err != nil {
			continue
		}
		val, err := portfolio.Value(b, scope, today, ccy, fx)
		if err != nil || val.Gross == 0 {
			continue
		}
		res, err := portfolio.Series(b, scope, today.AddDays(-365), today, ccy, fx)
		if err != nil {
			continue
		}
		day1d, hasDay1d := perfDay1d(res, today, rf)
		ps := b.Market.Price(asset.ID)
		row := assetRow{
			Name:     asset.Name,
			URL:      "/asset/" + url.PathEscape(string(asset.ID)),
			EditURL:  "/assets/" + url.PathEscape(string(asset.ID)) + "/edit",
			Day1d:    day1d,
			HasDay1d: hasDay1d,
			Spark1M:  spark(assetPricePoints(ps, today.AddDays(-30), today, asset.Currency, ccy, fx)),
			Spark1Y:  spark(assetPricePoints(ps, today.AddDays(-365), today, asset.Currency, ccy, fx)),
			Gross:    val.Gross,
			Net:      val.Net,
		}
		g := asset.Group
		if g == "" {
			g = "(ungrouped)"
		}
		sec, ok := bySection[g]
		if !ok {
			sec = &assetSection{Group: g, PropertyOnly: true}
			if g != "(ungrouped)" {
				sec.URL = "/group/" + escapeGroup(strings.ToLower(g))
			}
			bySection[g] = sec
		}
		sec.PropertyOnly = sec.PropertyOnly && asset.Kind == domain.Property
		sec.Gross += val.Gross
		sec.Net += val.Net
		sec.Rows = append(sec.Rows, row)
		rawWarnings = append(rawWarnings, res.Warnings...)
	}

	for _, sec := range bySection {
		slices.SortStableFunc(sec.Rows, func(a, b assetRow) int {
			switch {
			case a.Gross > b.Gross:
				return -1
			case a.Gross < b.Gross:
				return 1
			}
			return strings.Compare(a.Name, b.Name)
		})
		data.Sections = append(data.Sections, *sec)
	}
	sortSections(data.Sections)
	data.Warnings = dedupeWarnings(rawWarnings)
	s.render(w, status, "assets.html", data)
}

// assetsCSV serves every holding as a CSV download (kind, ticker, name, ISIN,
// gross, net, currency), cash included.
func (s *Server) assetsCSV(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b := s.file.Book
	rows, err := portfolio.AllRows(b, domain.Today(), b.DisplayCurrency(),
		market.Converter{FX: b.Market.FX})
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="finador-assets.csv"`)
	_ = portfolio.WriteAssetCSV(w, rows)
}

func (s *Server) assetCreate(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := parseAssetForm(r)
	if err != nil {
		s.renderAssetsPage(w, http.StatusBadRequest, "", err.Error())
		return
	}
	asset := &domain.Asset{
		ID:          domain.AssetID(domain.NewID()),
		Kind:        f.kind,
		Name:        f.name,
		Ticker:      f.ticker,
		ISIN:        f.isin,
		Aliases:     f.aliases,
		Currency:    f.ccy,
		Group:       f.group,
		Withholding: f.withholding,
	}
	if err := s.file.Book.AddAsset(asset); err != nil {
		s.renderAssetsPage(w, http.StatusBadRequest, "", err.Error())
		return
	}
	if err := s.persist(r.Context(), "web: new asset"); err != nil {
		s.renderAssetsPage(w, http.StatusInternalServerError, "", "could not save: "+err.Error())
		return
	}
	http.Redirect(w, r, "/assets?flash="+url.QueryEscape("created "+asset.Name), http.StatusSeeOther)
}

type assetEditData struct {
	Today       domain.Date
	Asset       *domain.Asset
	AliasesCSV  string
	WithholdPct string // withholding rate as a percentage string, e.g. "15"
	Error       string
}

func (s *Server) findAsset(w http.ResponseWriter, r *http.Request) (*domain.Asset, bool) {
	asset, err := s.file.Book.Asset(r.PathValue("id"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, "asset not found")
		return nil, false
	}
	return asset, true
}

func (s *Server) assetEditPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	asset, ok := s.findAsset(w, r)
	if !ok {
		return
	}
	s.renderAssetEdit(w, http.StatusOK, asset, "")
}

// renderAssetEdit is called with the lock already held.
func (s *Server) renderAssetEdit(w http.ResponseWriter, status int, asset *domain.Asset, errMsg string) {
	s.render(w, status, "asset-edit.html", assetEditData{
		Today:       domain.Today(),
		Asset:       asset,
		AliasesCSV:  strings.Join(asset.Aliases, ", "),
		WithholdPct: withholdPct(asset.Withholding),
		Error:       errMsg,
	})
}

func (s *Server) assetEditSubmit(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	asset, ok := s.findAsset(w, r)
	if !ok {
		return
	}
	f, err := parseAssetForm(r)
	if err != nil {
		s.renderAssetEdit(w, http.StatusBadRequest, asset, err.Error())
		return
	}
	// asset is a live pointer in the book: apply the edit, but restore the previous
	// state if validation or save fails so the in-memory book stays consistent.
	prev := *asset
	asset.Kind, asset.Name, asset.Ticker, asset.ISIN = f.kind, f.name, f.ticker, f.isin
	asset.Aliases, asset.Currency, asset.Group, asset.Withholding = f.aliases, f.ccy, f.group, f.withholding
	if err := s.file.Book.CheckAssetRefs(asset); err != nil {
		*asset = prev
		s.renderAssetEdit(w, http.StatusBadRequest, asset, err.Error())
		return
	}
	if err := s.file.Save(); err != nil {
		*asset = prev
		s.renderAssetEdit(w, http.StatusInternalServerError, asset, "could not save: "+err.Error())
		return
	}
	if err := s.syncSaved(r.Context(), "web: edit asset"); err != nil {
		s.renderAssetEdit(w, http.StatusInternalServerError, asset, "saved locally, but could not sync to the remote: "+err.Error())
		return
	}
	http.Redirect(w, r, "/assets?flash="+url.QueryEscape("updated "+asset.Name), http.StatusSeeOther)
}

func (s *Server) assetDelete(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// RemoveAsset refuses to orphan transactions (and purges the asset's market cache);
	// surface that refusal as a page error rather than a hard error page.
	if err := s.file.Book.RemoveAsset(r.PathValue("id")); err != nil {
		s.renderAssetsPage(w, http.StatusBadRequest, "", err.Error())
		return
	}
	if err := s.persist(r.Context(), "web: delete asset"); err != nil {
		s.renderAssetsPage(w, http.StatusInternalServerError, "", "could not save: "+err.Error())
		return
	}
	http.Redirect(w, r, "/assets?flash="+url.QueryEscape("asset deleted"), http.StatusSeeOther)
}

// assetForm holds the validated fields shared by create and edit.
type assetForm struct {
	kind        domain.AssetKind
	name        string
	ticker      string
	isin        string
	aliases     []string
	ccy         domain.Currency
	group       string
	withholding float64
}

func parseAssetForm(r *http.Request) (assetForm, error) {
	var f assetForm
	f.name = strings.TrimSpace(r.FormValue("name"))
	if f.name == "" {
		return f, fmt.Errorf("a name is required")
	}
	kindStr := strings.TrimSpace(r.FormValue("kind"))
	if kindStr == "" {
		kindStr = "security"
	}
	kind, err := domain.ParseAssetKind(kindStr)
	if err != nil {
		return f, err
	}
	f.kind = kind
	ccyStr := strings.TrimSpace(r.FormValue("ccy"))
	if ccyStr == "" {
		ccyStr = "EUR"
	}
	ccy, err := domain.ParseCurrency(ccyStr)
	if err != nil {
		return f, err
	}
	f.ccy = ccy
	f.ticker = strings.TrimSpace(r.FormValue("ticker"))
	f.isin = strings.TrimSpace(r.FormValue("isin"))
	f.group = strings.TrimSpace(r.FormValue("group"))
	f.aliases = parseAliasList(r.FormValue("aliases"))
	if wh := strings.TrimSpace(r.FormValue("withholding")); wh != "" {
		if f.withholding, err = domain.ParsePercent(wh); err != nil {
			return f, err
		}
	}
	return f, nil
}

// withholdPct renders a withholding fraction (0.15) as a percentage string ("15"),
// trailing zeros trimmed; the empty string for no withholding.
func withholdPct(w float64) string {
	if w <= 0 {
		return ""
	}
	return strconv.FormatFloat(w*100, 'f', -1, 64)
}

// perfDay1d returns the asset's flow-neutralized 1-day return (the "1d" perf
// row), so a buy that lands today reads as deployed capital, not a gain.
// Same machinery as the overview day TWR, hence the same number per asset.
func perfDay1d(res portfolio.SeriesResult, today domain.Date, rf float64) (float64, bool) {
	rows, _ := perf.Report(res.PerfPoints(false), res.PerfFlows(), today, rf)
	for _, r := range rows {
		if r.Name == "1d" && r.HasTWR {
			return r.TWR, true
		}
	}
	return 0, false
}

// assetPricePoints converts a price series into perf.Points for a sparkline,
// filtering to [from, today] and converting from the asset's native currency
// to the display currency. FX failures are skipped gracefully.
func assetPricePoints(ps *domain.PriceSeries, from, today domain.Date, assetCcy, displayCcy domain.Currency, fx market.Converter) []perf.Point {
	if ps == nil {
		return nil
	}
	var pts []perf.Point
	for _, p := range ps.Points {
		if p.Date.Before(from) || today.Before(p.Date) {
			continue
		}
		v := p.Close
		if assetCcy != displayCcy {
			if converted, err := fx.Convert(p.Close, assetCcy, displayCcy, p.Date); err == nil {
				v = converted
			}
		}
		pts = append(pts, perf.Point{Date: p.Date, Value: v})
	}
	return pts
}

// spark renders a window as a quiet sparkline in the body ink color.
func spark(pts []perf.Point) template.HTML {
	if len(pts) < 2 {
		return ""
	}
	return template.HTML(chart.Sparkline(pts, 72, 20, sparkInk))
}

// sortSections sorts sections: non-property sections first (gross desc, then name),
// property-only sections last (same sub-order within each block).
func sortSections(secs []assetSection) {
	slices.SortStableFunc(secs, func(a, b assetSection) int {
		// different "blocks"? property-only goes last
		if a.PropertyOnly != b.PropertyOnly {
			if a.PropertyOnly {
				return 1
			}
			return -1
		}
		// within the same block: gross desc then name
		switch {
		case a.Gross > b.Gross:
			return -1
		case a.Gross < b.Gross:
			return 1
		}
		return strings.Compare(a.Group, b.Group)
	})
}

// dedupeWarnings removes duplicate warning strings while preserving first-seen order.
func dedupeWarnings(ws []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range ws {
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}
