package market

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"finador/internal/domain"
)

// proxySource is batchSource plus the NowcastSource capability: CW8.PA is
// carried by URTH, quoted in USD, its NAV struck on the open when onOpen.
type proxySource struct {
	batchSource
	onOpen    bool
	none      bool
	closesErr error
	opensErr  error
	froms     []string // "CLOSES from", "OPENS from"
}

func (p *proxySource) NowcastProxy(ref Ref) (Proxy, bool) {
	if p.none || ref.Symbol != "CW8.PA" {
		return Proxy{}, false
	}
	return Proxy{Symbol: "URTH", Currency: domain.USD, OnOpen: p.onOpen}, true
}

func (p *proxySource) ProxyCloses(_ context.Context, _ Proxy, from domain.Date) ([]domain.PricePoint, error) {
	p.froms = append(p.froms, "CLOSES "+from.String())
	if p.closesErr != nil {
		return nil, p.closesErr
	}
	return []domain.PricePoint{{Date: mustDate("2026-06-05"), Close: 100}, {Date: mustDate("2026-06-08"), Close: 105}}, nil
}

func (p *proxySource) OpenFactors(_ context.Context, _ Proxy, from domain.Date) ([]domain.PricePoint, error) {
	p.froms = append(p.froms, "OPENS "+from.String())
	if p.opensErr != nil {
		return nil, p.opensErr
	}
	return []domain.PricePoint{{Date: mustDate("2026-06-05"), Close: 0.98}}, nil
}

func newProxySource() *proxySource {
	return &proxySource{batchSource: batchSource{fakeSource: fakeSource{daily: map[string]DailyData{
		"CW8.PA": {Currency: domain.EUR, Closes: []domain.PricePoint{{Date: mustDate("2026-06-05"), Close: 560}}},
	}}}}
}

// TestRefreshCachesTheProxyQuotes: the daily pass caches the proxy's raw
// closes from a fortnight before the fund's last NAV, its opening prints when
// the NAV is struck there, and the proxy's currency joins the FX the book
// fetches. The fund's own series keeps its published points only.
func TestRefreshCachesTheProxyQuotes(t *testing.T) {
	b := bookWithTrade(t)
	src := newProxySource()
	src.onOpen = true
	sum := Refresh(context.Background(), b, src, false)
	if len(sum.Warnings) != 0 {
		t.Fatalf("warnings = %v", sum.Warnings)
	}
	px := b.Market.Proxies["cw8"]
	if px == nil || px.Symbol != "URTH" || px.Currency != domain.USD || !px.OnOpen {
		t.Fatalf("proxy = %+v, want URTH in USD on the open", px)
	}
	if len(px.Closes.Points) != 2 || len(px.Opens.Points) != 1 || px.Closes.FetchedAt != domain.Today() {
		t.Errorf("closes = %+v, opens = %+v", px.Closes, px.Opens)
	}
	if want := "CLOSES 2026-05-22"; len(src.froms) == 0 || src.froms[0] != want {
		t.Errorf("fetches = %v, want %q first", src.froms, want)
	}
	if pts := b.Market.Price("cw8").Points; len(pts) != 1 {
		t.Errorf("the fund's series = %v, want its published NAV only", pts)
	}

	// Fetched today: the next pass leaves the proxy alone.
	src.froms = nil
	Refresh(context.Background(), b, src, false)
	if len(src.froms) != 0 {
		t.Errorf("fetches = %v, want none on the same day", src.froms)
	}
}

// TestRefreshProxyFailures: a failed proxy fetch warns and keeps what the
// cache had; missing opening prints leave the estimate on the close; a fund
// the source no longer names a proxy for loses its entry.
func TestRefreshProxyFailures(t *testing.T) {
	b := bookWithTrade(t)
	src := newProxySource()
	src.closesErr = errors.New("throttled")
	sum := Refresh(context.Background(), b, src, true)
	if !strings.Contains(strings.Join(sum.Warnings, "|"), "proxy URTH: throttled") {
		t.Errorf("warnings = %v, want the proxy failure named", sum.Warnings)
	}

	src.closesErr, src.onOpen, src.opensErr = nil, true, ErrNotCovered
	sum = Refresh(context.Background(), b, src, true)
	if px := b.Market.Proxies["cw8"]; px == nil || px.Opens != nil || len(px.Closes.Points) != 2 {
		t.Errorf("proxy = %+v, want closes and no opens", px)
	}
	if !strings.Contains(strings.Join(sum.Warnings, "|"), "anchored on its close") {
		t.Errorf("warnings = %v, want the close fallback said", sum.Warnings)
	}

	src.none = true
	Refresh(context.Background(), b, src, true)
	if _, ok := b.Market.Proxies["cw8"]; ok {
		t.Error("a fund with no proxy any more kept its proxy quotes")
	}
}

// TestSpotRefreshQuotesTheProxyNotTheFund: the spot pass moves the estimate
// by quoting the proxy (its price joins the proxy's closes), and no longer
// asks for the fund's own estimate.
func TestSpotRefreshQuotesTheProxyNotTheFund(t *testing.T) {
	b := bookWithTrade(t)
	src := newProxySource()
	Refresh(context.Background(), b, src, false)
	at := domain.Today().Time().Add(16 * time.Hour)
	src.batch = map[Ref]Quote{{Symbol: "URTH", Currency: domain.USD}: {Price: 107, Time: at, Currency: domain.USD, Live: true}}

	SpotRefresh(context.Background(), b, src)

	if close, _, ok := b.Market.Proxies["cw8"].Closes.At(domain.Today()); !ok || close != 107 {
		t.Errorf("proxy today = %v, want the 107 spot", close)
	}
	if src.batchRefs != 2 { // URTH and the EUR cross
		t.Errorf("refs spotted = %d, want the proxy and the cross, not the fund", src.batchRefs)
	}
	if last, _ := b.Market.Price("cw8").Last(); last.Date == domain.Today() {
		t.Error("the fund's series got a point today")
	}
}
