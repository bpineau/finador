package web

import (
	"testing"
)

func TestSortSectionsPropertyLast(t *testing.T) {
	secs := []assetSection{
		{Group: "realty", Gross: 500000, PropertyOnly: true},
		{Group: "equities", Gross: 10000},
		{Group: "bonds", Gross: 20000},
		{Group: "land", Gross: 900000, PropertyOnly: true},
	}
	sortSections(secs)
	got := []string{secs[0].Group, secs[1].Group, secs[2].Group, secs[3].Group}
	want := []string{"bonds", "equities", "land", "realty"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	// keep domain imported — remove if unused elsewhere
}
