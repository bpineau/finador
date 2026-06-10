package perf

import "testing"

func TestPeriodRange(t *testing.T) {
	today := d("2026-06-10") // un mercredi
	for _, tc := range []struct {
		name string
		from string
	}{
		{"1j", "2026-06-09"},
		{"2j", "2026-06-08"},
		{"5j", "2026-06-05"},
		{"7j", "2026-06-03"},
		{"1m", "2026-05-10"},
		{"3m", "2026-03-10"},
		{"ytd", "2025-12-31"},
		{"1a", "2025-06-10"},
	} {
		from, to, err := PeriodRange(tc.name, today)
		if err != nil || from != d(tc.from) || to != today {
			t.Errorf("PeriodRange(%s) = %s..%s, %v ; attendu %s..%s", tc.name, from, to, err, tc.from, today)
		}
	}
	// année civile précédente : bornes fixes
	from, to, err := PeriodRange("an-1", today)
	if err != nil || from != d("2024-12-31") || to != d("2025-12-31") {
		t.Errorf("an-1 = %s..%s, %v", from, to, err)
	}
	if _, _, err := PeriodRange("42x", today); err == nil {
		t.Error("période inconnue acceptée")
	}
}
