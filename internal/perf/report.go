package perf

import (
	"slices"
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
//
// The track record starts with the first point the scope holds anything:
// a scope's series starts with the book's first record, and the zeros in
// front of a line bought later would otherwise date its inception years
// early and annualize a few weeks' return over them.
func Report(points []Point, flows []Flow, evalTo domain.Date, rf float64) ([]Row, Metrics) {
	points = Held(points)
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

	// pofo measures the flow-neutral growth (see growth), whose own flows
	// only mark the days with nothing to earn on; the money gains come from
	// the values and the real flows (gain).
	dates, index, dead := growth(points, flows)
	mrows, sum := metrics.Report(dates, index, dead, metrics.ReportOptions{
		Windows:     windows,
		RiskFree:    rf,
		MinRiskDays: MinDaysForRisk,
		MinCAGRDays: MinDaysForCAGR,
	})
	for i, mr := range mrows {
		if mr.OK {
			mrows[i].Gain = gain(points, flows, domain.DateOf(mr.From), domain.DateOf(mr.To))
		}
	}

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
		Drawdown:     toDrawdown(sum.MaxDrawdown), // read on the growth, never on the value (D49)
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

// Held drops the points in front of the first one the scope holds anything
// at: where a scope's track record, and every window measured on it, starts.
// A series that never holds anything is returned whole.
func Held(points []Point) []Point {
	if first := slices.IndexFunc(points, func(p Point) bool { return p.Value != 0 }); first > 0 {
		return points[first:]
	}
	return points
}

// gain is the money made over [from, to], net of contributions: the last
// value less the first, less the flows booked strictly after from and at or
// before to (a flow on the base day is already part of the first value).
func gain(points []Point, flows []Flow, from, to domain.Date) float64 {
	var first, last float64
	seen := false
	for _, p := range points {
		if p.Date.Before(from) || to.Before(p.Date) {
			continue
		}
		if !seen {
			first, seen = p.Value, true
		}
		last = p.Value
	}
	net := 0.0
	for _, f := range flows {
		if from.Before(f.Date) && !to.Before(f.Date) {
			net += f.Amount
		}
	}
	return last - first - net
}
