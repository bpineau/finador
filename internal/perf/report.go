package perf

import (
	"strconv"
	"strings"

	"finador/internal/domain"

	"github.com/bpineau/pofo/pkg/metrics"
)

// RiskFreeFromConfig reads the annualized risk-free rate from a book config map.
// Expected key: "risk-free", value: "2.4" or "2.4%" → returns 0.024.
// Returns 0 if absent or unparseable.
func RiskFreeFromConfig(cfg map[string]string) float64 {
	s := strings.TrimSuffix(strings.TrimSpace(cfg["risk-free"]), "%")
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v / 100
}

// Row is one period line of a performance report.
type Row struct {
	Name    string
	TWR     float64
	HasTWR  bool
	Gain    float64 // money gained/lost over the window, net of contributions (display ccy)
	HasGain bool
}

// Minimum track records before annualized figures mean anything: annualizing a
// return earned over a few days compounds noise into absurdity. Below these
// spans the report exposes the cumulative since-inception return instead, and
// the Has* flags tell the renderer to hide the annualized cells.
const (
	MinDaysForRisk = 90  // vol/Sharpe/Sortino - about a quarter of daily returns
	MinDaysForCAGR = 365 // CAGR is a *compound annual* rate: needs at least a year
)

// Metrics holds the summary statistics computed over the full origin window.
// InceptionTWR/Since/Days always describe the track record; the annualized
// figures (CAGR, Vol, Sharpe, Sortino) are only set - and HasCAGR/HasRisk only
// true - once enough history backs them.
type Metrics struct {
	InceptionTWR                         float64 // cumulative TWR since the first point
	Since                                domain.Date
	Days                                 int
	CAGR, Vol, Sharpe, Sortino, RiskFree float64
	HasCAGR, HasRisk                     bool
	Drawdown                             Drawdown
}

// Report builds the standard period table + metrics for a daily series:
// the window assembly, slicing conventions and track-record gating are
// pofo's metrics.Report; this facade owns the domain types, the display
// windows (Names + the inception row, which for finador means "since the
// scope holds it"), the flat-window dedup and the house 0-instead-of-NaN
// convention. Points and flows are expressed in display currency; rf is
// the annualized risk-free rate.
func Report(points []Point, flows []Flow, evalTo domain.Date, rf float64) ([]Row, Metrics) {
	if len(points) == 0 {
		return nil, Metrics{}
	}
	origin := points[0].Date

	windows := make([]metrics.Window, 0, len(Names())+1)
	for _, name := range Names() {
		pf, pt, err := PeriodRange(name, evalTo)
		if err != nil {
			continue
		}
		windows = append(windows, metrics.Window{Name: name, From: pf.Time(), To: pt.Time()})
	}
	windows = append(windows, metrics.Window{Name: "inception", From: origin.Time(), To: evalTo.Time()})

	dates, values := toSeries(points)
	mrows, sum := metrics.Report(dates, values, toFlows(flows), metrics.ReportOptions{
		Windows:     windows,
		RiskFree:    rf,
		MinRiskDays: MinDaysForRisk,
		MinCAGRDays: MinDaysForCAGR,
	})

	// Flat-window dedup: a longer today-anchored window that measures
	// exactly what a shorter one already showed (bit-equal TWR and gain:
	// the signature of a series flat before the shorter window) adds no
	// information and reads as "a year measured" when only a month really
	// moved. Presentation, not measurement - so it lives here, not in
	// pofo. Only today-anchored windows compare; "prev-yr" ends elsewhere
	// and "inception" always shows.
	var rows []Row
	var last *Row
	for _, mr := range mrows {
		row := Row{Name: mr.Name, TWR: mr.TWR, HasTWR: mr.OK, Gain: mr.Gain, HasGain: mr.OK}
		anchored := mr.Name != "prev-yr" && mr.Name != "inception"
		if anchored && last != nil && row.HasTWR && last.HasTWR &&
			row.TWR == last.TWR && row.Gain == last.Gain {
			continue
		}
		rows = append(rows, row)
		if anchored {
			last = &rows[len(rows)-1]
		}
	}

	m := Metrics{
		InceptionTWR: sum.TWR,
		Since:        origin,
		Days:         sum.Days,
		RiskFree:     rf,
		Drawdown:     toDrawdown(sum.MaxDrawdown),
		HasRisk:      sum.HasRisk,
		HasCAGR:      sum.HasCAGR,
	}
	if sum.HasRisk {
		m.Vol, m.Sharpe, m.Sortino = orZero(sum.Vol), orZero(sum.Sharpe), orZero(sum.Sortino)
	}
	if sum.HasCAGR {
		m.CAGR = sum.CAGR
	}
	return rows, m
}
