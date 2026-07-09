package perf

import (
	"fmt"

	"finador/internal/domain"
)

// CloseAnchor is the "as of" date for the period table: the most recent settled
// close at or before `on` across the book's priced securities. Calendar today
// routinely runs ahead of the last real session (a shut exchange, a pre-open
// morning). Anchoring the windows on today then makes "1d" compare a stale,
// forward-filled close against yesterday while a fresh 24/5 FX point drifts
// underneath - noise, not a session (e.g. a EUR book holding a USD name shows
// -0.4% the morning it actually closed +1.67%). Anchoring on the last close
// makes "1d" mean "last close vs the previous close", matching Yahoo/Google.
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
	if best.IsZero() {
		return on
	}
	return best
}

// PeriodRange resolves a period name into [from, to]: the value at `from` is
// the comparison base, so "ytd" starts at Dec 31 of last year. Month/year
// arithmetic follows Go's AddDate normalization: from March 31, "1m" lands on
// March 3 (Feb 31 doesn't exist) - accepted quirk for month-end dates.
func PeriodRange(name string, today domain.Date) (from, to domain.Date, err error) {
	to = today
	switch name {
	case "1d":
		return today.AddDays(-1), to, nil
	case "2d":
		return today.AddDays(-2), to, nil
	case "5d":
		return today.AddDays(-5), to, nil
	case "7d":
		return today.AddDays(-7), to, nil
	case "1m":
		return domain.DateOf(today.Time().AddDate(0, -1, 0)), to, nil
	case "3m":
		return domain.DateOf(today.Time().AddDate(0, -3, 0)), to, nil
	case "ytd":
		return domain.Date{Year: today.Year - 1, Month: 12, Day: 31}, to, nil
	case "1y":
		return domain.DateOf(today.Time().AddDate(-1, 0, 0)), to, nil
	case "prev-yr":
		return domain.Date{Year: today.Year - 2, Month: 12, Day: 31},
			domain.Date{Year: today.Year - 1, Month: 12, Day: 31}, nil
	}
	return from, to, fmt.Errorf("unknown period %q (1d, 2d, 5d, 7d, 1m, 3m, ytd, 1y, prev-yr)", name)
}

// Names lists the period table shown by `finador perf`, in display order.
// 7d, not 5d: five calendar days span only 3-4 trading sessions, while a
// calendar week holds the five sessions a human means by "a week".
func Names() []string {
	return []string{"1d", "7d", "1m", "3m", "ytd", "1y", "prev-yr"}
}
