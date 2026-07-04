package market

import (
	"context"
	"errors"
	"net/http"
	"time"

	"finador/internal/domain"

	"github.com/bpineau/pofo/pkg/marketdata"
)

// Pofo is the standard Source, backed by the pofo marketdata client: the
// same multi-source resolution and download chain (Yahoo, then Financial
// Times, then Morningstar via Boursorama) finador used to carry itself,
// plus pofo's pinned catalog of common assets. The client is cache-less on
// purpose: plaintext quote files on disk would reveal the holdings the
// encrypted book protects; the book stores the series and fetches
// incrementally instead.
type Pofo struct {
	Client *marketdata.Client
}

// Default is finador's standard market source.
func Default() *Pofo { return NewPofo() }

// NewPofo returns a ready-to-use pofo-backed Source.
func NewPofo() *Pofo {
	c := marketdata.NewClient("") // no disk cache: privacy first
	c.HTTP = &http.Client{Timeout: 15 * time.Second}
	return &Pofo{Client: c}
}

// Resolve finds the best instrument for a free query (ticker, ISIN or
// name) and returns its symbol and display name.
func (p *Pofo) Resolve(ctx context.Context, query string) (SymbolInfo, error) {
	candidates, err := p.Client.Search(ctx, query)
	if err != nil || len(candidates) == 0 {
		return SymbolInfo{}, errNotFound(query, err)
	}
	best := candidates[0]
	symbol := best.Symbol
	if symbol == "" {
		// An FT-pinned resolution has no quotable symbol: keep the query
		// (pofo resolves it again at fetch time through the same pin).
		symbol = query
	}
	return SymbolInfo{Symbol: symbol, Name: best.Name}, nil
}

func errNotFound(query string, err error) error {
	if err != nil {
		return err
	}
	return domain.ErrNotFound
}

// ids lists the identifiers to try, most precise first.
func (r Ref) ids() []string {
	ids := make([]string, 0, 2)
	if r.ISIN != "" {
		ids = append(ids, r.ISIN)
	}
	if r.Symbol != "" {
		ids = append(ids, r.Symbol)
	}
	return ids
}

// Daily returns closes and dividend events from `from` to today. Prices
// are RAW closes (dividends not reinvested): finador values holdings at
// market price and books dividends as cash, so adjusted closes would
// double-count income. The declared currency is enforced natively
// (NoConvert): converted twin-listing closes never splice cleanly into
// the persisted history.
func (p *Pofo) Daily(ctx context.Context, ref Ref, from domain.Date) (DailyData, error) {
	ids := ref.ids()
	if len(ids) == 0 {
		return DailyData{}, ErrNotCovered
	}
	s, err := p.Client.FetchAny(ctx, ids, marketdata.FetchOptions{
		From:      from.Time(),
		NoSim:     true,
		Raw:       true,
		Currency:  string(ref.Currency),
		NoConvert: ref.Currency != "",
	})
	if err != nil {
		return DailyData{}, err
	}
	return toDailyData(s), nil
}

// Latest returns the freshest available price: the live market quote when
// one exists, otherwise the last daily close (a fund NAV). A quote that
// only exists off-currency converts at its own timestamp - acceptable for
// a spot point, which the next real close overwrites.
func (p *Pofo) Latest(ctx context.Context, ref Ref) (Quote, error) {
	ids := ref.ids()
	if len(ids) == 0 {
		return Quote{}, ErrNotCovered
	}
	q, err := p.Client.LatestAny(ctx, ids, marketdata.QuoteOptions{
		Currency: string(ref.Currency),
	})
	if err != nil {
		return Quote{}, err
	}
	return Quote{
		Price:    q.Price,
		Time:     q.Time,
		Currency: domain.Currency(q.Currency),
		Live:     q.Live,
	}, nil
}

// LatestBatch fetches the freshest price of many instruments: one live
// Yahoo quote call keyed on the declared tickers (the quote API answers
// exact symbols - no resolution, no twin-listing substitution), then the
// per-ref Latest fallback (ISIN first: FT/Morningstar NAV) for the rest.
func (p *Pofo) LatestBatch(ctx context.Context, refs []Ref) map[Ref]Quote {
	symbols := make([]string, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref.Symbol != "" && !seen[ref.Symbol] {
			seen[ref.Symbol] = true
			symbols = append(symbols, ref.Symbol)
		}
	}
	live := p.Client.LatestBatchLive(ctx, symbols)
	out := make(map[Ref]Quote, len(refs))
	for _, ref := range refs {
		if q, ok := live[ref.Symbol]; ok {
			out[ref] = Quote{Price: q.Price, Time: q.Time, Currency: domain.Currency(q.Currency), Live: q.Live}
			continue
		}
		if q, err := p.Latest(ctx, ref); err == nil {
			out[ref] = q
		}
	}
	return out
}

// Intraday returns 5-minute ticks for the current trading day. Yahoo is
// the only intraday source; unknown symbols map to ErrNotCovered.
func (p *Pofo) Intraday(ctx context.Context, ref Ref) (IntradayData, error) {
	if ref.Symbol == "" {
		return IntradayData{}, ErrNotCovered
	}
	s, err := p.Client.Intraday(ctx, ref.Symbol)
	if err != nil {
		if errors.Is(err, marketdata.ErrNotCovered) {
			return IntradayData{}, ErrNotCovered
		}
		return IntradayData{}, err
	}
	out := IntradayData{Currency: domain.Currency(s.Currency)}
	for _, pt := range s.Points {
		out.Points = append(out.Points, IntradayPoint{Time: pt.Time, Close: pt.Close})
	}
	return out, nil
}

// toDailyData maps a pofo series to finador's domain types.
func toDailyData(s *marketdata.Series) DailyData {
	out := DailyData{Currency: domain.Currency(s.Currency)}
	for _, pt := range s.Points {
		out.Closes = append(out.Closes, domain.PricePoint{Date: domain.DateOf(pt.Date), Close: pt.Close})
	}
	for _, d := range s.Dividends {
		out.Dividends = append(out.Dividends, domain.DividendEvent{ExDate: domain.DateOf(d.Date), Amount: d.Amount})
	}
	return out
}
