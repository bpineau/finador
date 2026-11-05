package domain

import "testing"

func TestParsePercentRejectsNonFinite(t *testing.T) {
	for _, s := range []string{"NaN", "nan%", "Inf", "-Inf", "101%", "-1"} {
		if _, err := ParsePercent(s); err == nil {
			t.Errorf("ParsePercent(%q) accepted", s)
		}
	}
	if v, err := ParsePercent("12.8%"); err != nil || v != 0.128 {
		t.Errorf("ParsePercent(12.8%%) = %v, %v", v, err)
	}
}
