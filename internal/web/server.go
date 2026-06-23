// Package web is the zero-JavaScript facade of finador: server-rendered
// html/template pages over the same portfolio engine as the CLI, all assets
// embedded. The encrypted file is shared behind a RWMutex; every mutation
// saves atomically then redirects (303).
package web

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

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

type Server struct {
	mu      sync.RWMutex
	file    *store.File
	source  market.Source
	offline bool
	sync    *Sync
}

func NewServer(f *store.File, src market.Source, offline bool, sync *Sync) *Server {
	return &Server{file: f, source: src, offline: offline, sync: sync}
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
// column, valuations) fresh without a manual click - today's daily candle from
// Yahoo carries the live price, so a periodic force-refresh is enough. Quote
// data lives in a local cache sidecar, so this never touches the ledger or the
// remote. A no-op in offline mode.
func (s *Server) AutoRefresh(ctx context.Context, interval time.Duration) {
	if s.offline || interval <= 0 {
		return
	}
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

// refreshOnce force-refreshes the market cache once, in place, under the write
// lock. Exposed for AutoRefresh and tests.
func (s *Server) refreshOnce(ctx context.Context) {
	if s.offline {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sum := market.Refresh(ctx, s.file.Book, s.source, true)
	if len(sum.Fetched) > 0 {
		if err := s.file.SaveCache(); err != nil {
			log.Printf("auto-refresh: cache not saved: %v", err)
		}
	}
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
	return mux
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.renderError(w, http.StatusNotFound, "page not found: "+r.URL.Path)
}
