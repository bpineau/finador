package perf

import (
	"fmt"

	"finador/internal/domain"
)

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
