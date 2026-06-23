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
