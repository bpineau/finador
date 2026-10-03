package portfolio

import (
	"fmt"
	"strconv"

	"finador/internal/domain"
)

// A fund priced once a day and published with a lag (an employee-savings
// fund, whose NAV of day D appears around D+2) would otherwise sit at a
// stale price beside securities quoted to the minute: today's value, the
// day's move and every short window would all miss the days its NAV has not
// covered yet. Such a fund is CARRIED by a listed proxy whose moves stand in
// for it from its last published NAV onward (the proxy and its currency are
// cached as domain.ProxyQuotes).
//
// The estimate is recomputed at every read and stored nowhere (D48): the
// cache holds raw quotes only (the fund's published NAVs, the proxy's closes
// and opening prints, the FX series), so a NAV published tomorrow replaces
// the estimate by the next read, and a figure derived from it says so.

// Estimate describes how a fund's price is estimated past its last
// published NAV: the NAV the estimate stands on and the proxy that carries
// it. The estimated days themselves are the points of the series Prices
// returns after NAV.Date.
type Estimate struct {
	Asset *domain.Asset
	NAV   domain.PricePoint // the last published price, carried forward
	Proxy string            // the listed instrument carrying it
}

// Note labels a price read from the estimated tail, for display next to a
// figure it entered.
func (e Estimate) Note(p domain.PricePoint) string {
	label := e.Asset.Name
	if e.Asset.Ticker != "" {
		label = e.Asset.Ticker
	}
	return fmt.Sprintf("%s: estimate for %s, %s %s (NAV of %s carried by %s, no published price yet)",
		label, p.Date, strconv.FormatFloat(p.Close, 'f', 2, 64), e.Asset.Currency, e.NAV.Date, e.Proxy)
}

// estimated reports whether a price dated d was read from the estimated tail.
func (e Estimate) estimated(d domain.Date) bool { return e.NAV.Date.Before(d) }

// Prices returns the daily price series every valuation reads: the cached
// published series, except for a fund with a cached proxy, which gets a
// copy extended by its estimated days, and an Estimate per such fund. The
// cache itself is never touched, so nothing derived can reach it.
//
// The estimate of day d is NAV × P(d) / P(anchor), P being the proxy's close
// converted into the fund's currency at its own date, and the anchor the
// print the last NAV was struck on: the proxy's close of the NAV's day, or
// its OPEN that day when the fund is valued at the opening (OnOpen; the open
// is read as the session's open-to-close factor, and a day without one keeps
// the close). A day whose rate is missing is left out, never estimated at 0.
func Prices(b *domain.Book, fx FX) (map[domain.AssetID]*domain.PriceSeries, map[domain.AssetID]Estimate) {
	prices := b.Market.Prices
	if len(b.Market.Proxies) == 0 {
		return prices, nil
	}
	out := make(map[domain.AssetID]*domain.PriceSeries, len(prices))
	for id, s := range prices {
		out[id] = s
	}
	estimates := map[domain.AssetID]Estimate{}
	for _, asset := range b.Assets {
		id := asset.ID
		px := b.Market.Proxies[id]
		if px == nil {
			continue
		}
		pub := prices[id]
		nav, ok := pub.Last()
		if !ok {
			continue
		}
		tail := carry(px, nav, asset.Currency, fx)
		if len(tail) == 0 {
			continue
		}
		ext := *pub
		ext.Points = append(append(make([]domain.PricePoint, 0, len(pub.Points)+len(tail)), pub.Points...), tail...)
		out[id] = &ext
		estimates[id] = Estimate{Asset: asset, NAV: nav, Proxy: px.Symbol}
	}
	return out, estimates
}

// carry returns the estimated days after nav: one per proxy close.
func carry(px *domain.ProxyQuotes, nav domain.PricePoint, ccy domain.Currency, fx FX) []domain.PricePoint {
	close, on, ok := px.Closes.At(nav.Date)
	if !ok || close <= 0 {
		return nil
	}
	base, err := fx.Convert(close, px.Currency, ccy, on)
	if err != nil || base <= 0 {
		return nil
	}
	if px.OnOpen && on == nav.Date {
		if f, fd, ok := px.Opens.At(nav.Date); ok && fd == nav.Date && f > 0 {
			base *= f
		}
	}
	var tail []domain.PricePoint
	for _, p := range px.Closes.Points {
		if !nav.Date.Before(p.Date) {
			continue
		}
		v, err := fx.Convert(p.Close, px.Currency, ccy, p.Date)
		if err != nil {
			continue
		}
		tail = append(tail, domain.PricePoint{Date: p.Date, Close: nav.Close * v / base})
	}
	return tail
}
