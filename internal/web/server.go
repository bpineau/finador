// Package web is the zero-JavaScript façade of finador: server-rendered
// html/template pages over the same portfolio engine as the CLI, all assets
// embedded. The encrypted file is shared behind a RWMutex; every mutation
// saves atomically then redirects (303).
package web

import (
	"net/http"
	"sync"

	"finador/internal/market"
	"finador/internal/store"
)

type Server struct {
	mu      sync.RWMutex
	file    *store.File
	source  market.Source
	offline bool
}

func NewServer(f *store.File, src market.Source, offline bool) *Server {
	return &Server{file: f, source: src, offline: offline}
}

// Handler routes the five views. Mutating routes are POST-only.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.dashboard)
	mux.HandleFunc("GET /style.css", s.stylesheet)
	mux.HandleFunc("GET /group/{ref...}", s.scopePage)
	mux.HandleFunc("GET /account/{ref}", s.scopePage)
	mux.HandleFunc("GET /asset/{ref}", s.scopePage)
	mux.HandleFunc("GET /tx", s.txPage)
	mux.HandleFunc("POST /tx", s.txCreate)
	mux.HandleFunc("POST /tx/{id}/delete", s.txDelete)
	mux.HandleFunc("GET /import", s.importPage)
	mux.HandleFunc("POST /import", s.importUpload)
	mux.HandleFunc("POST /refresh", s.refresh)
	mux.HandleFunc("GET /", s.notFound)
	return mux
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.renderError(w, http.StatusNotFound, "page introuvable : "+r.URL.Path)
}
