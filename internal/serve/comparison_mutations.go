package serve

import (
	"errors"
	"net/http"
	"os"
)

func (s *Server) handleComparisonSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allowMutation(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid saved comparison", http.StatusBadRequest)
		return
	}
	docs, canvases := r.PostForm["doc"], r.PostForm["canvas"]
	items, _, err := s.comparisonSelectionWithCanvases(docs, canvases)
	if err != nil {
		http.Error(w, "invalid saved comparison: "+err.Error(), http.StatusBadRequest)
		return
	}
	syncPage, syncView, err := parseComparisonSync(r.PostForm["sync"])
	if err != nil {
		http.Error(w, "invalid saved comparison: "+err.Error(), http.StatusBadRequest)
		return
	}
	normalizedCanvases := make([]string, len(items))
	for i := range items {
		normalizedCanvases[i] = items[i].Canvas
	}
	_, err = s.comparisons.add(savedComparison{
		Name: r.PostFormValue("name"), Docs: docs, Canvases: normalizedCanvases,
		SyncPage: syncPage, SyncView: syncView,
	})
	if err != nil {
		if errors.Is(err, ErrComparisonNameExists) {
			http.Error(w, "a saved comparison already uses that name", http.StatusConflict)
			return
		}
		http.Error(w, "could not save comparison: "+err.Error(), http.StatusBadRequest)
		return
	}
	query := comparisonQuery(docs, normalizedCanvases, syncPage, syncView)
	query.Set("saved", "1")
	http.Redirect(w, r, compareRoute+"?"+query.Encode(), http.StatusSeeOther)
}

func (s *Server) handleComparisonDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allowMutation(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid comparison deletion", http.StatusBadRequest)
		return
	}
	if err := s.comparisons.delete(r.PostFormValue("id")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "could not delete comparison", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

type savedComparisonSummary struct {
	ID   string
	Name string
	Href string
}

func comparisonSummaries(sets []savedComparison) []savedComparisonSummary {
	out := make([]savedComparisonSummary, 0, len(sets))
	for _, set := range sets {
		query := comparisonQuery(set.Docs, set.Canvases, set.SyncPage, set.SyncView)
		out = append(out, savedComparisonSummary{ID: set.ID, Name: set.Name, Href: compareRoute + "?" + query.Encode()})
	}
	return out
}
