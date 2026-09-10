// Package market fetches and converts public market data: daily closes,
// dividends and FX, behind a pluggable Source.
package market

import (
	"context"
	"errors"
	"time"

	"finador/internal/domain"
)

// ErrNotCovered means the source cannot handle a given Ref (e.g. an
// intraday request without a quotable symbol).
var ErrNotCovered = errors.New("instrument not covered by this provider")

// Ref identifies an instrument to quote: the ISIN is preferred when both
// are set (most precise), the symbol otherwise. Currency, when set, is the
// instrument's declared quote currency and part of the Source contract:
// Daily must serve quotes natively in it or fail; Latest may convert a
// last-resort quote into it (a spot point is overwritten by the next real
// close, so a conversion never leaves a seam in the persisted history).
type Ref struct {
	Symbol, ISIN string
	Currency     domain.Currency
}

// IntradayPoint is one 5-minute tick in an intraday price series.
type IntradayPoint struct {
	Time  time.Time
	Close float64
}

// IntradayData carries an intraday series for one instrument.
type IntradayData struct {
	Currency domain.Currency
	Points   []IntradayPoint
}

// Quote is the most recent known price of an instrument. Live reports how
// fresh it is: true means a real-time market price whose Time is an intraday
// instant; false means the last daily close (a fund NAV), whose Time is that
// close's date.
type Quote struct {
	Price    float64
	Time     time.Time
	Currency domain.Currency
	Live     bool

	// Estimated marks a nowcast rather than a print: a fund priced once a
	// day and published with a lag (an employee-savings fund) carried
	// forward by a listed proxy. Live says it moves with the session;
	// Estimated says nobody has struck that number yet.
	Estimated bool

	// Session names the trading session the price was struck in: "regular",
	// "pre", "post", or "" when the source names no session (a daily close,
	// a fund NAV, a nowcast - which therefore never claims one). Only the
	// extended-hours opt-in ever yields "pre" or "post".
	Session string
}

// The sessions Quote.Session can name beyond the regular one.
const (
	SessionPre  = "pre"
	SessionPost = "post"
)

// Extended reports whether the quote is an off-hours print: a pre-market or
// after-hours trade rather than the regular session's. Such a print is
// thinner than a close and is displayed, never stored.
func (q Quote) Extended() bool { return q.Session == SessionPre || q.Session == SessionPost }

// Source provides daily market data. finador fetches serially, politely.
// The standard implementation is Pofo (see Default).
type Source interface {
	// Resolve finds the best symbol for a free query: ticker, ISIN or name.
	Resolve(ctx context.Context, query string) (SymbolInfo, error)
	// Daily returns raw closes and dividends from `from` (inclusive) to today.
	Daily(ctx context.Context, ref Ref, from domain.Date) (DailyData, error)
	// Intraday returns 5-minute ticks for the current trading day.
	Intraday(ctx context.Context, ref Ref) (IntradayData, error)
	// Latest returns the freshest available price: live when the market
	// quotes one, otherwise the last daily close.
	Latest(ctx context.Context, ref Ref) (Quote, error)
}

// BatchSource is an optional Source capability: the freshest price of many
// instruments in one call. The answer is authoritative - a ref absent from
// Quotes means the source, all its fallbacks included, could not serve it -
// so SpotRefresh never re-asks per instrument behind a batch.
type BatchSource interface {
	LatestBatch(ctx context.Context, refs []Ref) BatchQuotes
}

// ExtendedSource is an optional Source capability: the same batched pass as
// BatchSource, but allowed to answer with a venue's extended-hours print when
// one is newer than the regular session's last price. A source that does not
// implement it simply never serves off-hours prices, and the opt-in degrades
// to the regular batch.
type ExtendedSource interface {
	LatestBatchExtended(ctx context.Context, refs []Ref) BatchQuotes
}

// BatchQuotes is what one batched spot pass learned: a quote per ref the
// source could serve, and why each other ref failed. Carrying the reasons
// is the point - a batch that silently drops half its refs is how a whole
// portfolio ends up displaying yesterday's prices with nothing said.
type BatchQuotes struct {
	Quotes map[Ref]Quote
	Errs   map[Ref]error
}

// SymbolInfo is what Resolve learns about a free query: the canonical
// quotable symbol and the instrument's full name.
type SymbolInfo struct {
	Symbol string
	Name   string
}

// DailyData carries one instrument's raw daily history from a Source.
type DailyData struct {
	Currency  domain.Currency // quote currency (exchange metadata)
	Closes    []domain.PricePoint
	Dividends []domain.DividendEvent
}
