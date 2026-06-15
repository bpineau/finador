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
// search result page - stable across minor layout changes.
// Ported from portfodor's boursorama.go.
var morningstarIDRe = regexp.MustCompile(`/bourse/(?:opcvm|trackers)/cours/(0P[0-9A-Za-z]+)/`)

// morningstarToken is the view id embedded in Morningstar's public chart
// pages; it has been stable for years (ported from portfodor's morningstar.go).
const morningstarToken = "ok91jeenoo"

// Morningstar fetches daily NAVs for funds identified by ISIN. It first
// resolves the ISIN to a Morningstar "0P…" id via Boursorama's AJAX search,
// then downloads the COMPACTJSON timeseries from tools.morningstar.fr.
// Currency is left empty because Morningstar's API doesn't disclose it;
// finador's Refresh skips the currency-mismatch warning when fetched currency
// is "" (see refresh.go: `if data.Currency != "" && data.Currency != …`).
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
	msID, err := m.resolveViaBoursorama(ctx, ref.ISIN)
	if err != nil {
		return DailyData{}, err // includes ErrNotCovered when no 0P… link
	}
	return m.fetchNAV(ctx, msID, from)
}

// resolveViaBoursorama queries Boursorama's AJAX search for an ISIN and
// scrapes the Morningstar "0P…" id from the returned HTML fragment.
func (m *Morningstar) resolveViaBoursorama(ctx context.Context, isin string) (string, error) {
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
	// Currency is intentionally empty - Morningstar doesn't disclose it.
	var out DailyData
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
		if !prevDate.IsZero() && !prevDate.Before(day) {
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
// one retry on 429/5xx - matching yahoo.go/ft.go's politeness convention.
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
