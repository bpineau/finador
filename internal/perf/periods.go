package perf

import (
	"fmt"

	"finador/internal/domain"

	"github.com/bpineau/pofo/pkg/metrics"
)

// CloseAnchor is the "as of" date for the period table: the most recent settled
// close at or before `on` across the book's priced securities. Calendar today
// routinely runs ahead of the last real session (a shut exchange, a pre-open
// morning). Anchoring the windows on today then makes "1d" compare a stale,
// forward-filled close against yesterday while a fresh 24/5 FX point drifts
// underneath - noise, not a session (e.g. a EUR book holding a USD name shows
// -0.4% the morning it actually closed +1.67%). Anchoring on the last close
// makes "1d" mean "last close vs the previous close", matching Yahoo/Google.
// A lagged fund's proxy counts too: the session it closed is the one the
// fund's estimate stands on (portfolio.Prices).
// Returns `on` unchanged when no security has any close (e.g. a property-only
// book), so the caller's calendar-today behaviour is preserved.
func CloseAnchor(m *domain.MarketData, on domain.Date) domain.Date {
	if m == nil {
		return on
	}
	best := domain.Date{}
	for _, s := range m.Prices {
		if _, d, ok := s.At(on); ok && best.Before(d) {
			best = d
		}
	}
	for _, px := range m.Proxies {
		if _, d, ok := px.Closes.At(on); ok && best.Before(d) {
			best = d
		}
	}
	if best.IsZero() {
		return on
	}
	return best
}

// PeriodRange resolves a period name into [from, to]: the value at `from` is
// the comparison base, so "ytd" starts at Dec 31 of last year. The table's
// windows are pofo's metrics.StandardWindows, whose months and years are
// counted on the calendar and clamped to the end of a shorter month, as a
// spreadsheet's EDATE does: from March 31, "1m" starts on February 28 (29 in
// a leap year), never on Go's AddDate normalization to March 3 (D54). "2d",
// "3d" and "5d" are plain calendar-day spans.
func PeriodRange(name string, today domain.Date) (from, to domain.Date, err error) {
	switch name {
	case "2d":
		return today.AddDays(-2), today, nil
	case "3d":
		return today.AddDays(-3), today, nil
	case "5d":
		return today.AddDays(-5), today, nil
	}
	for _, w := range metrics.StandardWindows(today.Time()) {
		if w.Name == name {
			return domain.DateOf(w.From), domain.DateOf(w.To), nil
		}
	}
	return from, to, fmt.Errorf("unknown period %q (1d, 2d, 3d, 5d, 7d, 1m, 3m, ytd, 1y, prev-yr)", name)
}

// Names lists the period table shown by `finador perf`, in display order.
// 7d, not 5d: five calendar days span only 3-4 trading sessions, while a
// calendar week holds the five sessions a human means by "a week".
func Names() []string {
	return []string{"1d", "7d", "1m", "3m", "ytd", "1y", "prev-yr"}
}
