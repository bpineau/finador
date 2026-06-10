package perf

import (
	"fmt"

	"finador/internal/domain"
)

// PeriodRange resolves a period name into [from, to]: the value at `from` is
// the comparison base, so "ytd" starts at Dec 31 of last year.
func PeriodRange(name string, today domain.Date) (from, to domain.Date, err error) {
	to = today
	switch name {
	case "1j":
		return today.AddDays(-1), to, nil
	case "2j":
		return today.AddDays(-2), to, nil
	case "5j":
		return today.AddDays(-5), to, nil
	case "7j":
		return today.AddDays(-7), to, nil
	case "1m":
		return domain.DateOf(today.Time().AddDate(0, -1, 0)), to, nil
	case "3m":
		return domain.DateOf(today.Time().AddDate(0, -3, 0)), to, nil
	case "ytd":
		return domain.Date{Year: today.Year - 1, Month: 12, Day: 31}, to, nil
	case "1a":
		return domain.DateOf(today.Time().AddDate(-1, 0, 0)), to, nil
	case "an-1":
		return domain.Date{Year: today.Year - 2, Month: 12, Day: 31},
			domain.Date{Year: today.Year - 1, Month: 12, Day: 31}, nil
	}
	return from, to, fmt.Errorf("période %q inconnue (1j, 2j, 5j, 7j, 1m, 3m, ytd, 1a, an-1)", name)
}

// Names lists the period table shown by `finador perf`, in display order.
func Names() []string {
	return []string{"1j", "5j", "1m", "3m", "ytd", "1a", "an-1"}
}
