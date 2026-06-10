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
