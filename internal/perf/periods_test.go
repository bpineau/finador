package perf

import "testing"

func TestPeriodRange(t *testing.T) {
	today := d("2026-06-10") // un mercredi
	for _, tc := range []struct {
		name string
		from string
	}{
		{"1d", "2026-06-09"},
		{"2d", "2026-06-08"},
		{"3d", "2026-06-07"},
		{"5d", "2026-06-05"},
		{"7d", "2026-06-03"},
		{"1m", "2026-05-10"},
		{"3m", "2026-03-10"},
		{"ytd", "2025-12-31"},
		{"1y", "2025-06-10"},
	} {
		from, to, err := PeriodRange(tc.name, today)
		if err != nil || from != d(tc.from) || to != today {
			t.Errorf("PeriodRange(%s) = %s..%s, %v ; attendu %s..%s", tc.name, from, to, err, tc.from, today)
		}
	}
	// previous calendar year: fixed bounds
	from, to, err := PeriodRange("prev-yr", today)
	if err != nil || from != d("2024-12-31") || to != d("2025-12-31") {
		t.Errorf("prev-yr = %s..%s, %v", from, to, err)
	}
	if _, _, err := PeriodRange("42x", today); err == nil {
		t.Error("unknown period accepted")
	}
}

// Months and years clamp to the end of a shorter month, as EDATE does: Go's
// AddDate normalization put "1m" from March 31 on March 3, a month-end row
// three days short (D54).
func TestPeriodRangeMonthEnd(t *testing.T) {
	for _, tc := range []struct{ name, today, from string }{
		{"1m", "2026-03-31", "2026-02-28"},
		{"1m", "2028-03-31", "2028-02-29"}, // leap year
		{"3m", "2026-05-31", "2026-02-28"},
		{"1m", "2026-07-31", "2026-06-30"},
		{"1y", "2028-02-29", "2027-02-28"},
		{"1m", "2026-03-15", "2026-02-15"}, // mid-month: unchanged
	} {
		from, _, err := PeriodRange(tc.name, d(tc.today))
		if err != nil || from != d(tc.from) {
			t.Errorf("PeriodRange(%s, %s) from = %s, %v; want %s", tc.name, tc.today, from, err, tc.from)
		}
	}
}
