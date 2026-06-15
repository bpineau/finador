package market

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"finador/internal/domain"
)

// Boursorama AJAX search fixture containing an opcvm link with a 0P… id.
const boursoSearchOK = `<div class="search__results">
  <a href="/bourse/opcvm/cours/0P0001ABCD/" class="search__item-link">
    <span class="search__item-title">Independance AM Europe Small</span>
  </a>
</div>`

// Boursorama fixture with a trackers link (alternative path).
const boursoSearchTracker = `<a href="/bourse/trackers/cours/0P0000AAAA/">My ETF</a>`

// Boursorama response with no 0P… link at all.
const boursoSearchNoID = `<div class="search__results"><p>Aucun résultat</p></div>`

// Morningstar COMPACTJSON fixture: two rows with valid dates.
// epoch 1700000000000 ms = 2023-11-14T22:13:20Z → DateOf → 2023-11-14
// epoch 1700086400000 ms = 2023-11-15T22:13:20Z → DateOf → 2023-11-15
const msCompact2 = `[[1700000000000,12.3],[1700086400000,12.4]]`

// Morningstar COMPACTJSON fixture: empty array → ErrNotCovered.
const msCompactEmpty = `[]`

// msServer creates a test server routing /recherche/ajax to boursoHTML and
// /api/rest.svc/... to msJSON. It returns a Morningstar with both base URLs
// pointed at the test server so no real network is used.
func msServer(t *testing.T, boursoHTML, msJSON string) *Morningstar {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/recherche/ajax":
			if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				t.Errorf("missing X-Requested-With header on Boursorama request")
			}
			w.Write([]byte(boursoHTML))
		case strings.HasPrefix(r.URL.Path, "/api/rest.svc/"):
			// Verify the Morningstar token and key params are present.
			if !strings.Contains(r.URL.Path, morningstarToken) {
				t.Errorf("path missing morningstar token: %s", r.URL.Path)
			}
			w.Write([]byte(msJSON))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	ms := NewMorningstar()
	ms.BoursoBase = srv.URL
	ms.BaseURL = srv.URL
	return ms
}

// TestMorningstarDailyOK verifies the happy path: Boursorama resolves 0P id,
// Morningstar returns two NAV rows, both are parsed correctly.
func TestMorningstarDailyOK(t *testing.T) {
	ms := msServer(t, boursoSearchOK, msCompact2)
	got, err := ms.Daily(context.Background(), Ref{ISIN: "LU1832174962"}, mustDate("2023-11-14"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Closes) != 2 {
		t.Fatalf("closes = %+v, want 2", got.Closes)
	}
	if got.Closes[0].Close != 12.3 {
		t.Errorf("close[0].Close = %v, want 12.3", got.Closes[0].Close)
	}
	if got.Closes[1].Close != 12.4 {
		t.Errorf("close[1].Close = %v, want 12.4", got.Closes[1].Close)
	}
	// Currency must be empty - Morningstar doesn't disclose it.
	if got.Currency != domain.Currency("") {
		t.Errorf("currency = %q, want empty string", got.Currency)
	}
}

// TestMorningstarTrackerLink verifies that the regex also matches /trackers/ paths.
func TestMorningstarTrackerLink(t *testing.T) {
	ms := msServer(t, boursoSearchTracker, msCompact2)
	got, err := ms.Daily(context.Background(), Ref{ISIN: "FR0000000001"}, mustDate("2023-11-14"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Closes) == 0 {
		t.Error("expected closes from tracker link")
	}
}

// TestMorningstarNoISIN verifies ErrNotCovered when ISIN is absent.
func TestMorningstarNoISIN(t *testing.T) {
	ms := NewMorningstar()
	_, err := ms.Daily(context.Background(), Ref{Symbol: "CW8.PA"}, mustDate("2023-11-10"))
	if !errors.Is(err, ErrNotCovered) {
		t.Fatalf("err = %v, want ErrNotCovered", err)
	}
}

// TestMorningstarBoursoNoID verifies ErrNotCovered when Boursorama returns no 0P link.
func TestMorningstarBoursoNoID(t *testing.T) {
	ms := msServer(t, boursoSearchNoID, msCompact2)
	_, err := ms.Daily(context.Background(), Ref{ISIN: "LU9999999999"}, mustDate("2023-11-10"))
	if !errors.Is(err, ErrNotCovered) {
		t.Fatalf("err = %v, want ErrNotCovered (no 0P link in Boursorama response)", err)
	}
}

// TestMorningstarEmptyResult verifies ErrNotCovered when Morningstar returns [].
func TestMorningstarEmptyResult(t *testing.T) {
	ms := msServer(t, boursoSearchOK, msCompactEmpty)
	_, err := ms.Daily(context.Background(), Ref{ISIN: "LU1832174962"}, mustDate("2023-11-10"))
	if !errors.Is(err, ErrNotCovered) {
		t.Fatalf("err = %v, want ErrNotCovered (empty COMPACTJSON array)", err)
	}
}

// TestMorningstarName verifies the provider name used in the Multi chain.
func TestMorningstarName(t *testing.T) {
	if NewMorningstar().Name() != "morningstar" {
		t.Errorf("Name = %q, want %q", NewMorningstar().Name(), "morningstar")
	}
}
