package market

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"finador/internal/domain"
)

// FT quotes European mutual funds and SICAVs by ISIN through the Financial
// Times market-data endpoints, which cover many FR/LU funds Yahoo lacks.
// It is ported faithfully from portfodor's marketdata FT provider.
type FT struct {
	BaseURL string
	HTTP    *http.Client
}

func NewFT() *FT {
	return &FT{
		BaseURL: "https://markets.ft.com",
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Name identifies the provider in the Multi chain.
func (f *FT) Name() string { return "ft" }

// ftResolution is the instrument FT search maps an ISIN to.
type ftResolution struct {
	Xid      string
	Name     string
	Currency string
}

// Daily resolves ref.ISIN through FT search, then downloads its NAV series.
// ErrNotCovered when no ISIN is given or FT doesn't list it.
func (f *FT) Daily(ctx context.Context, ref Ref, from domain.Date) (DailyData, error) {
	if ref.ISIN == "" {
		return DailyData{}, ErrNotCovered // FT is keyed by ISIN
	}
	res, err := f.search(ctx, ref.ISIN)
	if err != nil {
		return DailyData{}, err
	}
	return f.series(ctx, ref.ISIN, res, from)
}

// search resolves an ISIN through the Financial Times securities search.
// Ported from portfodor's ftSearch.
func (f *FT) search(ctx context.Context, isin string) (ftResolution, error) {
	u := fmt.Sprintf("%s/data/searchapi/searchsecurities?query=%s", f.BaseURL, url.QueryEscape(isin))
	body, err := f.do(ctx, http.MethodGet, u, "", nil)
	if err != nil {
		return ftResolution{}, err
	}
	var resp struct {
		Data struct {
			Security []struct {
				Name      string `json:"name"`
				Symbol    string `json:"symbol"` // "LU0171310443:EUR" or "NTSG:GER:EUR"
				Xid       string `json:"xid"`
				IsPrimary bool   `json:"isPrimary"`
			} `json:"security"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return ftResolution{}, fmt.Errorf("unreadable FT search response: %w", err)
	}
	secs := resp.Data.Security
	// Prefer a listing not quoted in pence (GBX); fall back to any match.
	best := -1
	for i, s := range secs {
		ccy := ftSymbolCurrency(s.Symbol)
		if s.Xid == "" || ccy == "GBX" || ccy == "GBp" {
			continue
		}
		best = i
		break
	}
	if best < 0 {
		for i, s := range secs {
			if s.Xid != "" {
				best = i
				break
			}
		}
	}
	if best < 0 {
		// FT doesn't list this instrument: let the chain fall through.
		return ftResolution{}, ErrNotCovered
	}
	sec := secs[best]
	return ftResolution{Xid: sec.Xid, Name: sec.Name, Currency: ftSymbolCurrency(sec.Symbol)}, nil
}

// ftSymbolCurrency extracts the currency, the last segment of an FT symbol
// like "LU0171310443:EUR" or "NTSG:GER:EUR".
func ftSymbolCurrency(symbol string) string {
	parts := strings.Split(symbol, ":")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-1]
}

// series downloads a daily NAV series from the FT chart API for a resolved
// instrument. Ported from portfodor's fetchFT.
func (f *FT) series(ctx context.Context, isin string, res ftResolution, from domain.Date) (DailyData, error) {
	days := max(int(time.Since(from.Time()).Hours()/24)+2, 2)
	payload, err := json.Marshal(map[string]any{
		"days":              days,
		"dataPeriod":        "Day",
		"dataInterval":      1,
		"timeServiceFormat": "JSON",
		"returnDateType":    "ISO8601",
		"elements":          []map[string]any{{"Type": "price", "Symbol": res.Xid}},
	})
	if err != nil {
		return DailyData{}, err
	}
	body, err := f.do(ctx, http.MethodPost, f.BaseURL+"/data/chartapi/series", "application/json", payload)
	if err != nil {
		return DailyData{}, err
	}
	var resp struct {
		Dates    []string `json:"Dates"`
		Elements []struct {
			Currency        string `json:"Currency"`
			ComponentSeries []struct {
				Type   string     `json:"Type"`
				Values []*float64 `json:"Values"`
			} `json:"ComponentSeries"`
		} `json:"Elements"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return DailyData{}, fmt.Errorf("unreadable FT response: %w", err)
	}
	if len(resp.Elements) == 0 {
		return DailyData{}, fmt.Errorf("FT: empty response for %s", isin)
	}
	var closes []*float64
	for _, cs := range resp.Elements[0].ComponentSeries {
		if cs.Type == "Close" {
			closes = cs.Values
			break
		}
	}
	if closes == nil || len(closes) != len(resp.Dates) {
		return DailyData{}, fmt.Errorf("FT: no close series for %s", isin)
	}
	currency := resp.Elements[0].Currency
	if currency == "" {
		currency = res.Currency
	}
	out := DailyData{Currency: domain.Currency(currency)}
	for i, d := range resp.Dates {
		cl := closes[i]
		if cl == nil || *cl <= 0 {
			continue
		}
		t, err := time.ParseInLocation("2006-01-02T15:04:05", d, time.UTC)
		if err != nil {
			continue
		}
		day := domain.DateOf(t)
		if day.Before(from) {
			continue
		}
		out.Closes = append(out.Closes, domain.PricePoint{Date: day, Close: *cl})
	}
	return out, nil
}

// do performs an HTTP request with a browser User-Agent and one retry on
// 429/5xx, mirroring yahoo.go's politeness. A POST carries the JSON payload.
func (f *FT) do(ctx context.Context, method, rawURL, contentType string, payload []byte) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		var reqBody io.Reader
		if payload != nil {
			reqBody = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, rawURL, reqBody)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/json")
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := f.HTTP.Do(req)
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
			return nil, fmt.Errorf("ft: HTTP %d", resp.StatusCode)
		}
		return body, nil
	}
}
