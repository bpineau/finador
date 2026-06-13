package market

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"
	_ "time/tzdata" // fuseaux des places boursières, sans dépendre de l'OS

	"finador/internal/domain"
)

// Yahoo is the default Source: the unofficial but stable Yahoo Finance API.
// No key, no auth — just a browser-looking User-Agent and polite retries.
type Yahoo struct {
	BaseURL   string
	Client    *http.Client
	RetryWait time.Duration
}

func NewYahoo() *Yahoo {
	return &Yahoo{
		BaseURL:   "https://query1.finance.yahoo.com",
		Client:    &http.Client{Timeout: 15 * time.Second},
		RetryWait: 2 * time.Second,
	}
}

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// get fetches a JSON endpoint with one retry on 429/5xx.
func (y *Yahoo) get(ctx context.Context, path string, query url.Values, into any) error {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, y.BaseURL+path+"?"+query.Encode(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := y.Client.Do(req)
		if err != nil {
			return err
		}
		retriable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if retriable && attempt < 2 {
			resp.Body.Close()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(y.RetryWait << attempt):
			}
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("yahoo %s: HTTP %d", path, resp.StatusCode)
		}
		return json.NewDecoder(resp.Body).Decode(into)
	}
}

// Name identifies the provider in the Multi chain.
func (y *Yahoo) Name() string { return "yahoo" }

func (y *Yahoo) Resolve(ctx context.Context, query string) (SymbolInfo, error) {
	var resp struct {
		Quotes []struct {
			Symbol    string `json:"symbol"`
			LongName  string `json:"longname"`
			ShortName string `json:"shortname"`
		} `json:"quotes"`
	}
	q := url.Values{"q": {query}, "quotesCount": {"5"}, "newsCount": {"0"}}
	if err := y.get(ctx, "/v1/finance/search", q, &resp); err != nil {
		return SymbolInfo{}, err
	}
	for _, quote := range resp.Quotes {
		if quote.Symbol == "" {
			continue
		}
		name := quote.LongName
		if name == "" {
			name = quote.ShortName
		}
		return SymbolInfo{Symbol: quote.Symbol, Name: name}, nil
	}
	return SymbolInfo{}, fmt.Errorf("symbol for %q: %w", query, domain.ErrNotFound)
}

func (y *Yahoo) Daily(ctx context.Context, ref Ref, from domain.Date) (DailyData, error) {
	symbol := ref.Symbol
	if symbol == "" {
		return DailyData{}, ErrNotCovered // a ticker provider needs a symbol
	}
	var resp struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Currency             string `json:"currency"`
					ExchangeTimezoneName string `json:"exchangeTimezoneName"`
				} `json:"meta"`
				Timestamp []int64 `json:"timestamp"`
				Events    struct {
					Dividends map[string]struct {
						Amount float64 `json:"amount"`
						Date   int64   `json:"date"`
					} `json:"dividends"`
				} `json:"events"`
				Indicators struct {
					Quote []struct {
						Close []*float64 `json:"close"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
			Error any `json:"error"`
		} `json:"chart"`
	}
	q := url.Values{
		"period1":  {strconv.FormatInt(from.Time().Unix(), 10)},
		"period2":  {strconv.FormatInt(time.Now().Unix()+86400, 10)},
		"interval": {"1d"},
		"events":   {"div"},
	}
	if err := y.get(ctx, "/v8/finance/chart/"+url.PathEscape(symbol), q, &resp); err != nil {
		return DailyData{}, err
	}
	if len(resp.Chart.Result) == 0 {
		return DailyData{}, fmt.Errorf("quotes for %q: %w", symbol, domain.ErrNotFound)
	}
	r := resp.Chart.Result[0]

	loc, err := time.LoadLocation(r.Meta.ExchangeTimezoneName)
	if err != nil {
		loc = time.UTC
	}
	dateOf := func(ts int64) domain.Date { return domain.DateOf(time.Unix(ts, 0).In(loc)) }

	out := DailyData{Currency: domain.Currency(r.Meta.Currency)}
	var closes []*float64
	if len(r.Indicators.Quote) > 0 {
		closes = r.Indicators.Quote[0].Close
	}
	for i, ts := range r.Timestamp {
		if i >= len(closes) || closes[i] == nil {
			continue // jour férié ou close manquant
		}
		out.Closes = append(out.Closes, domain.PricePoint{Date: dateOf(ts), Close: *closes[i]})
	}
	for _, div := range r.Events.Dividends {
		out.Dividends = append(out.Dividends, domain.DividendEvent{ExDate: dateOf(div.Date), Amount: div.Amount})
	}
	slices.SortFunc(out.Dividends, func(a, b domain.DividendEvent) int {
		return a.ExDate.Time().Compare(b.ExDate.Time())
	})
	return out, nil
}
