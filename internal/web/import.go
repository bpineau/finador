package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/portfolio"
)

type importData struct {
	Today domain.Date
	Flash string
	Error string
}

func (s *Server) importPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.render(w, http.StatusOK, "import.html", importData{
		Today: domain.Today(),
		Flash: r.URL.Query().Get("flash"),
		Error: r.URL.Query().Get("error"),
	})
}

func (s *Server) importUpload(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// a personal transaction CSV fits well under 10 MB: anti-DoS bound
	if r.ContentLength > 10<<20 {
		http.Redirect(w, r, "/import?error="+url.QueryEscape("file too large (10 MB max)"), http.StatusSeeOther)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Redirect(w, r, "/import?error="+url.QueryEscape("no file received"), http.StatusSeeOther)
		return
	}
	defer file.Close()
	// if an error occurs mid-file, the in-memory Book retains already-added lines
	// (unsaved); the next successful save will persist them - "last write wins"
	// posture assumed (D9), the error is displayed.
	added, skipped, err := portfolio.ImportCSV(s.file.Book, file)
	if err != nil {
		http.Redirect(w, r, "/import?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if err := s.persist(r.Context(), "web: import CSV"); err != nil {
		http.Redirect(w, r, "/import?error="+url.QueryEscape("could not save: "+err.Error()), http.StatusSeeOther)
		return
	}
	flash := fmt.Sprintf("%d imported, %d skipped (duplicates)", added, skipped)
	http.Redirect(w, r, "/import?flash="+url.QueryEscape(flash), http.StatusSeeOther)
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.offline {
		http.Redirect(w, r, "/assets?error="+url.QueryEscape("offline: cannot refresh quotes"), http.StatusSeeOther)
		return
	}
	// Same contract as `finador refresh`: an explicit refresh means "give me
	// the market now", so it always ends with a spot pass. Without it this
	// button forced the daily fetch only, which on an already-fetched day is
	// a no-op - the click did nothing and said "0 series refreshed".
	ctx := detached(r.Context())
	sum := market.Refresh(ctx, s.file.Book, s.source, true)
	spot := market.SpotRefresh(ctx, s.file.Book, s.source)
	s.mergeSpot(spot.Quotes)
	if err := s.file.SaveCache(); err != nil {
		http.Redirect(w, r, "/assets?error="+url.QueryEscape("could not save: "+err.Error()), http.StatusSeeOther)
		return
	}
	flash := fmt.Sprintf("%d series refreshed, %d quotes", len(sum.Fetched), len(spot.Quotes))
	if n := len(sum.Warnings) + len(spot.Warnings); n > 0 {
		flash += fmt.Sprintf(" (%d warning(s): %s)", n, strings.Join(append(sum.Warnings, spot.Warnings...), "; "))
	}
	http.Redirect(w, r, "/assets?flash="+url.QueryEscape(flash), http.StatusSeeOther)
}
