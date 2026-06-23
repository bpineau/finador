// Package web is the zero-JavaScript facade of finador: server-rendered
// html/template pages over the same portfolio engine as the CLI, all assets
// embedded. The encrypted file is shared behind a RWMutex; every mutation
// saves atomically then redirects (303).
package web

import (
	"context"
	"net/http"
	"sync"

	"finador/internal/market"
	"finador/internal/store"
)

// PushFunc persists the just-saved working copy to the remote (commit + push),
// reconciling conflicts. It is nil in local mode. It runs under the server's
// write lock, so it must not call back into the Server.
type PushFunc func(ctx context.Context, msg string) error

type Server struct {
	mu      sync.RWMutex
	file    *store.File
	source  market.Source
	offline bool
	push    PushFunc
}

func NewServer(f *store.File, src market.Source, offline bool, push PushFunc) *Server {
	return &Server{file: f, source: src, offline: offline, push: push}
}

// syncSaved pushes an already-saved working copy to the remote. In local mode
// (no push hook) it is a no-op. Pushing inline (under the write lock) is what
// makes a web edit durable: the sync layer marks the working copy dirty until
// the push lands, so a later startup pull can no longer clobber an unpushed
// edit. A push failure means the edit is saved locally but not yet on the
// remote - surface it, but never roll the in-memory edit back over it.
func (s *Server) syncSaved(ctx context.Context, msg string) error {
	if s.push != nil {
		return s.push(ctx, msg)
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
