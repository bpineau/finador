package web

import (
	"fmt"
	"net/http"
	"net/url"

	"finador/internal/domain"
	"finador/internal/market"
	"finador/internal/portfolio"
)

type importData struct {
	Aujourdhui domain.Date
	Flash      string
	Erreur     string
}

func (s *Server) importPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.render(w, http.StatusOK, "import.html", importData{
		Aujourdhui: domain.Today(),
		Flash:      r.URL.Query().Get("flash"),
		Erreur:     r.URL.Query().Get("erreur"),
	})
}

func (s *Server) importUpload(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, _, err := r.FormFile("fichier")
	if err != nil {
		http.Redirect(w, r, "/import?erreur="+url.QueryEscape("aucun fichier reçu"), http.StatusSeeOther)
		return
	}
	defer file.Close()
	added, skipped, err := portfolio.ImportCSV(s.file.Book, file)
	if err != nil {
		http.Redirect(w, r, "/import?erreur="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if err := s.file.Save(); err != nil {
		http.Redirect(w, r, "/import?erreur="+url.QueryEscape("sauvegarde impossible : "+err.Error()), http.StatusSeeOther)
		return
	}
	flash := fmt.Sprintf("%d importée(s), %d ignorée(s) (doublons)", added, skipped)
	http.Redirect(w, r, "/import?flash="+url.QueryEscape(flash), http.StatusSeeOther)
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.offline {
		http.Redirect(w, r, "/?erreur="+url.QueryEscape("hors ligne : refresh impossible"), http.StatusSeeOther)
		return
	}
	sum := market.Refresh(r.Context(), s.file.Book, s.source, true)
	if err := s.file.Save(); err != nil {
		http.Redirect(w, r, "/?erreur="+url.QueryEscape("sauvegarde impossible"), http.StatusSeeOther)
		return
	}
	flash := fmt.Sprintf("%d série(s) rafraîchie(s)", len(sum.Fetched))
	http.Redirect(w, r, "/?flash="+url.QueryEscape(flash), http.StatusSeeOther)
}
