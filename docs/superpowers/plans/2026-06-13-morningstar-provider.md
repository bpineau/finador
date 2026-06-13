# Morningstar/Boursorama Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Morningstar (via Boursorama) fallback provider to finador's multi-source market layer, with tests and README docs.

**Architecture:** New `Morningstar` struct in `internal/market/morningstar.go` that resolves an ISIN to a Morningstar `0P…` ID via Boursorama's AJAX search, then fetches daily NAV COMPACTJSON from Morningstar. Added as the third entry in `Multi.Default()`. Tests use httptest, no network, no new dependencies.

**Tech Stack:** Go stdlib only — `net/http`, `encoding/json`, `regexp`, `net/url`.

---

### Task 1: Write morningstar.go

**Files:**
- Create: `internal/market/morningstar.go`

- [ ] **Step 1: Write the file**

```go
package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"finador/internal/domain"
)

// morningstarIDRe extracts the Morningstar fund id (0P…) from a Boursorama
// search result page — stable across minor layout changes.
var morningstarIDRe = regexp.MustCompile(`/bourse/(?:opcvm|trackers)/cours/(0P[0-9A-Za-z]+)/`)

// morningstarToken is the view id embedded in Morningstar's public chart
// pages; it has been stable for years (ported from portfodor).
const morningstarToken = "ok91jeenoo"

// Morningstar fetches daily NAVs for funds identified by ISIN. It first
// resolves the ISIN to a Morningstar "0P…" id via Boursorama's search, then
// downloads the COMPACTJSON timeseries from tools.morningstar.fr. Currency is
// left empty because Morningstar's API doesn't disclose it; finador's Refresh
// skips the currency-mismatch warning when the fetched currency is "".
// Ported faithfully from portfodor's boursorama.go + morningstar.go.
type Morningstar struct {
	BaseURL    string // e.g. "https://tools.morningstar.fr"
	BoursoBase string // e.g. "https://www.boursorama.com"
	HTTP       *http.Client
}

// NewMorningstar returns a ready-to-use Morningstar provider.
func NewMorningstar() *Morningstar {
	return &Morningstar{
		BaseURL:    "https://tools.morningstar.fr",
		BoursoBase: "https://www.boursorama.com",
		HTTP:       &http.Client{Timeout: 15 * time.Second},
	}
}

// Name identifies the provider in the Multi chain.
func (m *Morningstar) Name() string { return "morningstar" }

// Daily resolves ref.ISIN to a Morningstar id via Boursorama, then fetches
// the daily NAV series. Returns ErrNotCovered when:
//   - ref.ISIN is empty
//   - Boursorama search yields no 0P… link
//   - Morningstar returns an empty series
func (m *Morningstar) Daily(ctx context.Context, ref Ref, from domain.Date) (DailyData, error) {
	if ref.ISIN == "" {
		return DailyData{}, ErrNotCovered
	}
	msID, err := m.resolveViaBoursourma(ctx, ref.ISIN)
	if err != nil {
		return DailyData{}, err // includes ErrNotCovered when no 0P… link
	}
	return m.fetchNAV(ctx, msID, from)
}

// resolveViaBoursourma queries Boursorama's AJAX search for an ISIN and
// scrapes the Morningstar "0P…" id from the returned HTML fragment.
func (m *Morningstar) resolveViaBoursourma(ctx context.Context, isin string) (string, error) {
	u := fmt.Sprintf("%s/recherche/ajax?query=%s", m.BoursoBase, url.QueryEscape(isin))
	body, err := m.do(ctx, u, map[string]string{"X-Requested-With": "XMLHttpRequest"})
	if err != nil {
		return "", err
	}
	match := morningstarIDRe.FindSubmatch(body)
	if match == nil {
		return "", ErrNotCovered
	}
	return string(match[1]), nil
}

// fetchNAV downloads the daily NAV series for a Morningstar id from the
// tools.morningstar.fr timeseries API (COMPACTJSON format).
func (m *Morningstar) fetchNAV(ctx context.Context, msID string, from domain.Date) (DailyData, error) {
	u := fmt.Sprintf(
		"%s/api/rest.svc/timeseries_price/%s?id=%s&idtype=Morningstar&frequency=daily&startDate=%s&outputType=COMPACTJSON",
		m.BaseURL, morningstarToken, url.QueryEscape(msID), from.String(),
	)
	body, err := m.do(ctx, u, nil)
	if err != nil {
		return DailyData{}, err
	}
	// COMPACTJSON: [[epoch_ms, value], …]. Errors come back as XML/HTML.
	var rows [][]float64
	if err := json.Unmarshal(body, &rows); err != nil {
		return DailyData{}, fmt.Errorf("morningstar: unreadable response for %s", msID)
	}
	if len(rows) == 0 {
		return DailyData{}, ErrNotCovered // empty → let the chain fall through
	}
	out := DailyData{} // Currency intentionally empty — Morningstar doesn't disclose it
	var prevDate domain.Date
	for _, row := range rows {
		if len(row) < 2 || row[1] <= 0 {
			continue
		}
		day := domain.DateOf(time.UnixMilli(int64(row[0])).UTC())
		if day.Before(from) {
			continue
		}
		// Skip rows whose date is not strictly after the previous kept date.
		if !prevDate.IsZero() && !day.After(prevDate) {
			continue
		}
		out.Closes = append(out.Closes, domain.PricePoint{Date: day, Close: row[1]})
		prevDate = day
	}
	if len(out.Closes) == 0 {
		return DailyData{}, ErrNotCovered
	}
	return out, nil
}

// do performs a GET with a browser User-Agent, the given extra headers, and
// one retry on 429/5xx — matching yahoo.go/ft.go's politeness convention.
func (m *Morningstar) do(ctx context.Context, rawURL string, extraHeaders map[string]string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		for k, v := range extraHeaders {
			req.Header.Set(k, v)
		}
		resp, err := m.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		retriable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if retriable && attempt < 1 {
			resp.Body.Close()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Second):
			}
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("morningstar: HTTP %d", resp.StatusCode)
		}
		return body, nil
	}
}
```

- [ ] **Step 2: Verify it compiles**

```bash
cd /Users/ben/projects/finador && go build ./internal/market/...
```

Expected: no output (success).

---

### Task 2: Write morningstar_test.go

**Files:**
- Create: `internal/market/morningstar_test.go`

- [ ] **Step 1: Write the test file**

```go
package market

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"finador/internal/domain"
)

// Boursorama AJAX search fixture containing an opcvm link with a 0P… id.
const boursoSearchOK = `<div class="search__results">
  <a href="/bourse/opcvm/cours/0P0001ABCD/" class="search__item-link">
    <span class="search__item-title">Independance AM Europe Small</span>
  </a>
</div>`

// Boursorama response with no 0P… link at all.
const boursoSearchNoID = `<div class="search__results"><p>Aucun résultat</p></div>`

// Morningstar COMPACTJSON fixture: two rows.
const msCompact2 = `[[1700000000000,12.3],[1700086400000,12.4]]`

// Morningstar COMPACTJSON fixture: empty array → ErrNotCovered.
const msCompactEmpty = `[]`

// msServer creates a test server routing /recherche/ajax to boursoHTML and
// /api/rest.svc/... to msJSON. It returns a Morningstar with both base URLs
// pointed at the test server.
func msServer(t *testing.T, boursoHTML, msJSON string) *Morningstar {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/recherche/ajax":
			if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				t.Errorf("missing X-Requested-With header")
			}
			w.Write([]byte(boursoHTML))
		case len(r.URL.Path) > 20 && r.URL.Path[:20] == "/api/rest.svc/timese":
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

func TestMorningstarDailyOK(t *testing.T) {
	ms := msServer(t, boursoSearchOK, msCompact2)
	got, err := ms.Daily(context.Background(), Ref{ISIN: "LU1832174962"}, mustDate("2023-11-10"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Closes) != 2 {
		t.Fatalf("closes = %+v", got.Closes)
	}
	// epoch 1700000000000 ms = 2023-11-14 22:13:20 UTC → DateOf → 2023-11-14
	if got.Closes[0].Close != 12.3 {
		t.Errorf("close[0] = %+v", got.Closes[0])
	}
	if got.Closes[1].Close != 12.4 {
		t.Errorf("close[1] = %+v", got.Closes[1])
	}
	// Currency must be empty — Morningstar doesn't disclose it.
	if got.Currency != domain.Currency("") {
		t.Errorf("currency = %q, want empty", got.Currency)
	}
}

func TestMorningstarNoISIN(t *testing.T) {
	ms := NewMorningstar()
	_, err := ms.Daily(context.Background(), Ref{Symbol: "CW8.PA"}, mustDate("2023-11-10"))
	if !errors.Is(err, ErrNotCovered) {
		t.Fatalf("err = %v, want ErrNotCovered", err)
	}
}

func TestMorningstarBoursoNoID(t *testing.T) {
	ms := msServer(t, boursoSearchNoID, msCompact2)
	_, err := ms.Daily(context.Background(), Ref{ISIN: "LU9999999999"}, mustDate("2023-11-10"))
	if !errors.Is(err, ErrNotCovered) {
		t.Fatalf("err = %v, want ErrNotCovered (no 0P link)", err)
	}
}

func TestMorningstarEmptyResult(t *testing.T) {
	ms := msServer(t, boursoSearchOK, msCompactEmpty)
	_, err := ms.Daily(context.Background(), Ref{ISIN: "LU1832174962"}, mustDate("2023-11-10"))
	if !errors.Is(err, ErrNotCovered) {
		t.Fatalf("err = %v, want ErrNotCovered (empty COMPACTJSON)", err)
	}
}

func TestMorningstarName(t *testing.T) {
	if NewMorningstar().Name() != "morningstar" {
		t.Errorf("Name = %q", NewMorningstar().Name())
	}
}
```

- [ ] **Step 2: Run the tests**

```bash
cd /Users/ben/projects/finador && go test ./internal/market/... -run TestMorningstar -v -count=1
```

Expected: all 5 tests PASS.

---

### Task 3: Add Morningstar to Multi.Default()

**Files:**
- Modify: `internal/market/multi.go`

- [ ] **Step 1: Update Default()**

Change the `providers` slice in `Default()` from:
```go
providers: []Provider{NewYahoo(), NewFT()},
```
to:
```go
providers: []Provider{NewYahoo(), NewFT(), NewMorningstar()},
```

Also update the comment above `Default()` to mention Morningstar.

- [ ] **Step 2: Run full test suite**

```bash
cd /Users/ben/projects/finador && go test ./... -count=1
```

Expected: all tests pass, no failures.

---

### Task 4: Update README.md

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add subsection under "Quotes: refresh and --offline"**

After the existing `### Quotes: refresh and --offline` section block (ending with `--offline` skips all of it…), add a new `### Atypical assets (funds by ISIN)` subsection.

Content to insert (after the last line of the Quotes section and before `### Web: serve`):

```markdown
### Atypical assets (funds by ISIN)

Yahoo Finance is the primary quote source (ticker-based). When Yahoo doesn't cover an asset, finador automatically falls back — **by ISIN** — to two additional providers on every `finador refresh`:

1. **Financial Times** (`markets.ft.com`) — covers a wide range of European funds (SICAV/OPCVM).
2. **Morningstar via Boursorama** — resolves the ISIN to a Morningstar `0P…` id through Boursorama's fund search, then fetches the daily NAV from `tools.morningstar.fr`.

The chain is: Yahoo → FT → Morningstar. The first provider that returns data wins; a provider that can't find the asset signals `ErrNotCovered` and the chain falls through transparently.

**Typical usage — a French/Luxembourg fund:**

```sh
finador asset add "Indépendance AM Europe Small" --isin LU1832174962
finador refresh    # priced via FT or Morningstar automatically
```

**Honest limitation — French employee-savings funds (FCPE/PEE).** Funds distributed through employer plans (e.g. an Eres Sélection fund) are identified by an internal AMF code that is _not_ a real ISIN and is not listed on any public quote source. No provider in the chain (Yahoo, FT, Morningstar, or portfodor's equivalent) covers them. Value them manually:

```sh
finador asset set "Eres Sélection Équilibre" 4250.00 --account "PEE Entreprise"
```

All three providers are implemented with no extra dependency — stdlib HTTP and `regexp` only.
```

- [ ] **Step 2: Verify**

```bash
cd /Users/ben/projects/finador && grep -n "Atypical assets" README.md
```

Expected: finds the new heading.

---

### Task 5: Final verification and commit

- [ ] **Step 1: Run go vet**

```bash
cd /Users/ben/projects/finador && go vet ./...
```

Expected: no output.

- [ ] **Step 2: Run full test suite**

```bash
cd /Users/ben/projects/finador && go test ./... -count=1
```

Expected: all tests pass.

- [ ] **Step 3: Run go build**

```bash
cd /Users/ben/projects/finador && go build ./...
```

Expected: no output.

- [ ] **Step 4: Run golangci-lint**

```bash
cd /Users/ben/projects/finador && golangci-lint run ./...
```

Expected: no issues.

- [ ] **Step 5: Commit**

```bash
cd /Users/ben/projects/finador && git add internal/market/morningstar.go internal/market/morningstar_test.go internal/market/multi.go README.md && git commit -m "$(cat <<'EOF'
feat(market): Morningstar/Boursorama fallback provider + docs for funds-by-ISIN

Adds a third provider to the Multi chain (Yahoo → FT → Morningstar).
Morningstar resolves an ISIN to a 0P… id via Boursorama's AJAX search,
then fetches the daily NAV COMPACTJSON from tools.morningstar.fr.
Currency is left empty (Morningstar doesn't disclose it); Refresh already
skips the mismatch warning when fetched currency is "". Empty result →
ErrNotCovered so the chain falls through gracefully. No new dependencies
(stdlib HTTP + regexp). README documents the fallback chain and its limits
(FCPE/PEE funds with AMF codes are not covered by any public source).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

Expected: commit succeeds (pre-commit hook `go vet ./... && go test ./...` passes).
