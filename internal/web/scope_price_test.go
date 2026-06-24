package web

import (
	"strings"
	"testing"
)

// A single-asset scope page shows a price-history section with its own (prange)
// selector and the daily quote curve. Account/group scopes have no single quote,
// so they must not render a price chart.
func TestAssetPageHasPriceHistory(t *testing.T) {
	srv, _ := testServer(t)

	code, body := get(t, srv, "/asset/cw8")
	if code != 200 {
		t.Fatalf("asset page status %d", code)
	}
	if !strings.Contains(body, "history") {
		t.Fatal("asset page missing the history section")
	}
	if !strings.Contains(body, "prange=") {
		t.Fatal("asset page missing the price-range selector")
	}
	if !strings.Contains(body, "price EUR") {
		t.Fatalf("asset page missing the price curve legend: %q", body)
	}

	code, body = get(t, srv, "/account/pea")
	if code != 200 {
		t.Fatalf("account page status %d", code)
	}
	if strings.Contains(body, "prange=") {
		t.Fatal("account page must not render a price chart")
	}
}

// The two selectors are independent: a prange link preserves the current value
// chart range, and vice versa.
func TestRangeSelectorsAreIndependent(t *testing.T) {
	srv, _ := testServer(t)
	_, body := get(t, srv, "/asset/cw8?range=3m")
	if !strings.Contains(body, "prange=1m&amp;range=3m") && !strings.Contains(body, "range=3m&amp;prange=1m") {
		t.Fatalf("prange links did not preserve the value range param: %q", body)
	}
}

// The default price range is 1y and the selector shows all 5 labels including 1d.
func TestPriceRangeDefault(t *testing.T) {
	srv, _ := testServer(t)
	_, body := get(t, srv, "/asset/cw8")
	for _, label := range []string{"1d", "1m", "3m", "1y", "all"} {
		if !strings.Contains(body, label) {
			t.Errorf("price range selector missing label %q", label)
		}
	}
	// "1y" is active by default
	if !strings.Contains(body, "active-range") {
		t.Error("no active-range marker on asset page")
	}
	// "1d" link must exist (uses prange=1d since it's not the default)
	if !strings.Contains(body, "prange=1d") {
		t.Error("1d link must carry prange=1d param")
	}
}

// Explicit prange=1y falls through to the daily chart path.
func TestPriceRangeExplicit(t *testing.T) {
	srv, _ := testServer(t)
	code, body := get(t, srv, "/asset/cw8?prange=1y")
	if code != 200 {
		t.Fatalf("prange=1y = %d", code)
	}
	if !strings.Contains(body, "price EUR") {
		t.Errorf("1y daily chart legend missing:\n%s", excerpt(body))
	}
}
