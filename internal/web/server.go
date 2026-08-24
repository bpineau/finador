// Package web is the zero-JavaScript facade of finador: server-rendered
// html/template pages over the same portfolio engine as the CLI, all assets
// embedded. The encrypted file is shared behind a RWMutex; every mutation
// saves atomically then redirects (303).
package web

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/store"
)

// Sync wires the web server to the remote (nil in local mode). Push persists
// the just-saved working copy (commit + push) and reports whether reconciling a
// conflict rewrote the working copy - in which case the server must Reload its
// in-memory File so it reflects the merged remote records (e.g. a transaction
// added concurrently from the Android client) and its disk stamp is fresh
// again. Both run under the server's write lock and must not call back in.
type Sync struct {
	Push   func(ctx context.Context, msg string) (reload bool, err error)
	Reload func() (*store.File, error)
}

const intradayTTL = 3 * time.Minute

type intradayEntry struct {
	day domain.Date
	at  time.Time
	pts []market.IntradayPoint
}

// Server serves the whole web app over one open store.File. All state lives
// behind mu (writers save then redirect); the intraday quote cache has its own
// lock so a slow fetch never blocks page reads.
type Server struct {
	mu         sync.RWMutex
	file       *store.File
	source     market.Source
	offline    bool
	sync       *Sync
	intradayMu sync.Mutex
	intraday   map[domain.AssetID]intradayEntry
	spot       map[domain.AssetID]market.Quote // freshest quotes; written under mu
}

// NewServer wraps an already-unlocked file. sync is nil in local mode;
// offline disables every network fetch (quotes come from the cache only).
func NewServer(f *store.File, src market.Source, offline bool, sync *Sync) *Server {
	return &Server{
		file:     f,
		source:   src,
		offline:  offline,
		sync:     sync,
		intraday: make(map[domain.AssetID]intradayEntry),
	}
}

// intradayFor returns the 5-minute intraday series for the current day. It
// reads from an in-memory cache (protected by intradayMu, separate from mu)
// and fetches from the network only when the cache is stale or absent. Never
// holds intradayMu across a network call.
func (s *Server) intradayFor(ctx context.Context, asset *domain.Asset) ([]market.IntradayPoint, bool) {
	today := domain.Today()

	s.intradayMu.Lock()
	e, cached := s.intraday[asset.ID]
	fresh := cached && e.day == today && time.Since(e.at) < intradayTTL
	s.intradayMu.Unlock()

	if fresh {
		return e.pts, true
	}
	if s.offline {
		if cached && e.day == today {
			return e.pts, true
		}
		return nil, false
	}

	ref := market.Ref{Symbol: asset.Ticker, ISIN: asset.ISIN}
	data, err := s.source.Intraday(ctx, ref)
	if err != nil {
		return nil, false
	}

	s.intradayMu.Lock()
	s.intraday[asset.ID] = intradayEntry{day: today, at: time.Now(), pts: data.Points}
	s.intradayMu.Unlock()

	return data.Points, true
}

// syncSaved pushes an already-saved working copy to the remote, then reloads the
// in-memory File if a merge rewrote the working copy. In local mode (no Sync) it
// is a no-op. Pushing inline (under the write lock) is what makes a web edit
// durable: the sync layer marks the working copy dirty until the push lands, so
// a later startup pull can no longer clobber an unpushed edit. A push failure
// means the edit is saved locally but not yet on the remote - surface it, but
// never roll the in-memory edit back over it.
func (s *Server) syncSaved(ctx context.Context, msg string) error {
	if s.sync == nil {
		return nil
	}
	reload, err := s.sync.Push(ctx, msg)
	if err != nil {
		return err
	}
	if reload && s.sync.Reload != nil {
		f, rerr := s.sync.Reload()
		if rerr != nil {
			return rerr
		}
		s.file = f
	}
	return nil
}

// persist saves the ledger then pushes it. Used by writes that have no
// in-memory rollback step; handlers that revert the book on save failure call
// s.file.Save() and s.syncSaved() separately, so a push error does not trigger
// a rollback that would diverge memory from the saved-and-dirty working copy.
func (s *Server) persist(ctx context.Context, msg string) error {
	if err := s.file.Save(); err != nil {
		return err
	}
	return s.syncSaved(ctx, msg)
}

// AutoRefresh refreshes the market cache every interval until ctx is done, so a
// long-running server keeps the day figures (overview day TWR, the /assets 1D
// column, valuations) fresh without a manual click. Each tick runs the full
// daily fetch only when a series has not been fetched today (history depth,
// dividends), then a light spot pass (market.SpotRefresh) so today's prices
// and FX follow the market in between; the observed quotes also tell the UI
// how live each price is. Quote data lives in a local cache sidecar, so this
// never touches the ledger or the remote. A no-op in offline mode.
func (s *Server) AutoRefresh(ctx context.Context, interval time.Duration) {
	if s.offline || interval <= 0 {
		return
	}
	s.refreshOnce(ctx) // immediate first pass: live figures from the first page
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.refreshOnce(ctx)
		}
	}
}

// pageRefreshTimeout bounds the refresh a page load may wait on. A browser
// must never hang on the market: past it the page renders with what the cache
// has and the background ticker catches up.
const pageRefreshTimeout = 5 * time.Second

// detached returns a context that inherits nothing but the values of parent.
// A refresh runs under the write lock and writes a cache every other request
// reads, so it must not be cancelled halfway by the one browser that happened
// to trigger it and then navigated away.
func detached(parent context.Context) context.Context { return context.WithoutCancel(parent) }

// refreshOnce refreshes the market cache once, in place, under the write
// lock: the daily fetch when due, then the spot pass. Exposed for AutoRefresh
// and tests.
func (s *Server) refreshOnce(ctx context.Context) { s.refresh2(ctx, 0, true) }

// refreshSpotIfStale refreshes only the live quotes, and only when the last
// pass is older than maxAge. This is the page-load path: one batched call,
// bounded, no daily fetch. The daily pass is deliberately left to the ticker -
// it walks a per-instrument fallback chain (Yahoo, then FT, then Morningstar,
// each with its own timeout), which is minutes of blocking on a bad day and
// has nothing to say about a price that moved since this morning.
func (s *Server) refreshSpotIfStale(ctx context.Context, maxAge time.Duration) {
	s.refresh2(ctx, maxAge, false)
}

// refresh2 is the one refresh path: skip when younger than maxAge (zero always
// refreshes), optionally run the daily fetch, always run the spot pass. The
// age is read under the write lock, so two page loads racing on a cold cache
// produce one refresh, not two: the pass stamps SpotAt before fetching, and
// the waiter then sees it fresh.
func (s *Server) refresh2(ctx context.Context, maxAge time.Duration, daily bool) {
	if s.offline {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if maxAge > 0 && time.Since(s.file.Book.Market.SpotAt) <= maxAge {
		return
	}
	fetched := 0
	if daily {
		fetched = len(market.Refresh(ctx, s.file.Book, s.source, false).Fetched)
	}
	spot := market.SpotRefresh(ctx, s.file.Book, s.source)
	s.mergeSpot(spot.Quotes)
	if fetched > 0 || len(spot.Quotes) > 0 {
		if err := s.file.SaveCache(); err != nil {
			log.Printf("auto-refresh: cache not saved: %v", err)
		}
	}
}

// mergeSpot folds newly observed quotes into the freshness notes rather than
// replacing them: a pass that came back empty (an outage, a cancelled request)
// knows nothing, and forgetting what the last good pass saw would turn every
// asset page silent. Callers hold the write lock.
func (s *Server) mergeSpot(quotes map[domain.AssetID]market.Quote) {
	if s.spot == nil {
		s.spot = map[domain.AssetID]market.Quote{}
	}
	for id, q := range quotes {
		s.spot[id] = q
	}
}

// quoteNote describes the freshness of an asset's price for the UI: the spot
// observed by the last refresh when there is one, otherwise the last stored
// close. Callers hold at least the read lock.
func (s *Server) quoteNote(asset *domain.Asset) string {
	if q, ok := s.spot[asset.ID]; ok {
		if q.Live && q.Estimated {
			return fmt.Sprintf("last quote %.2f %s · estimated at %s (proxy, no NAV yet)", q.Price, q.Currency, q.Time.Format("15:04 MST"))
		}
		if q.Live {
			return fmt.Sprintf("last quote %.2f %s · live at %s", q.Price, q.Currency, q.Time.Format("15:04 MST"))
		}
		return fmt.Sprintf("last quote %.2f %s · close of %s", q.Price, q.Currency, q.Time.Format("2006-01-02"))
	}
	if last, ok := s.file.Book.Market.Price(asset.ID).Last(); ok {
		return fmt.Sprintf("last quote %.2f %s · close of %s", last.Close, asset.Currency, last.Date)
	}
	return ""
}

// pageMaxAge is how stale quotes may be when a page is requested. The
// AutoRefresh ticker keeps a watched server inside it on its own; this is
// what saves the first page load after the server has been idle, which the
// ticker alone would serve from whatever the last tick happened to see.
const pageMaxAge = 2 * time.Minute

// freshen refreshes quotes before a page is rendered when they have aged past
// pageMaxAge. Static assets are skipped: they carry no figure, and a browser
// fetches them right after the page that already refreshed. The refresh is
// detached from the request (it writes shared state) and time-bounded (a
// browser must not hang on a throttled provider).
func (s *Server) freshen(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method != http.MethodGet, r.URL.Path == "/style.css", r.URL.Path == "/favicon.ico":
		default:
			ctx, cancel := context.WithTimeout(detached(r.Context()), pageRefreshTimeout)
			s.refreshSpotIfStale(ctx, pageMaxAge)
			cancel()
		}
		next.ServeHTTP(w, r)
	})
}

// Handler routes the five views. Mutating routes are POST-only.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.dashboard)
	mux.HandleFunc("GET /assets", s.assetsPage)
	mux.HandleFunc("POST /assets", s.assetCreate)
	mux.HandleFunc("GET /assets/{id}/edit", s.assetEditPage)
	mux.HandleFunc("POST /assets/{id}/edit", s.assetEditSubmit)
	mux.HandleFunc("POST /assets/{id}/delete", s.assetDelete)
	mux.HandleFunc("GET /assets.csv", s.assetsCSV)
	mux.HandleFunc("GET /style.css", s.stylesheet)
	mux.HandleFunc("GET /favicon.ico", s.favicon)
	mux.HandleFunc("GET /group/{ref...}", s.scopePage)
	mux.HandleFunc("GET /account/{ref}/group/{gpath...}", s.intersectPage)
	mux.HandleFunc("GET /account/{ref}", s.scopePage)
	mux.HandleFunc("GET /asset/{ref}", s.scopePage)
	mux.HandleFunc("POST /asset/{id}/rename", s.assetRename)
	mux.HandleFunc("GET /accounts", s.accountsPage)
	mux.HandleFunc("POST /accounts", s.accountCreate)
	mux.HandleFunc("GET /accounts/{id}/edit", s.accountEditPage)
	mux.HandleFunc("POST /accounts/{id}/edit", s.accountEditSubmit)
	mux.HandleFunc("POST /accounts/{id}/delete", s.accountDelete)
	mux.HandleFunc("GET /tx", s.txPage)
	mux.HandleFunc("POST /tx", s.txCreate)
	mux.HandleFunc("GET /tx/{id}/edit", s.txEditPage)
	mux.HandleFunc("POST /tx/{id}/edit", s.txEditSubmit)
	mux.HandleFunc("POST /tx/{id}/delete", s.txDelete)
	mux.HandleFunc("GET /import", s.importPage)
	mux.HandleFunc("POST /import", s.importUpload)
	mux.HandleFunc("POST /refresh", s.refresh)
	mux.HandleFunc("GET /", s.notFound)
	return s.freshen(mux)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.renderError(w, http.StatusNotFound, "page not found: "+r.URL.Path)
}
