package api

import (
	"errors"
	"net/http"

	"linkd/internal/ingestpreview"
)

func (a *API) previewIngest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.IngestPreviewer == nil {
		http.Error(w, "ingest preview unavailable", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var request ingestpreview.Request
	if err := decode(r, &request); err != nil {
		http.Error(w, "invalid ingest preview request", http.StatusBadRequest)
		return
	}
	result, err := a.IngestPreviewer.Preview(r.Context(), request)
	if err != nil {
		var e *ingestpreview.Error
		if errors.As(err, &e) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(e.Status)
			output(w, map[string]any{"error": map[string]string{"message": e.Message}})
			return
		}
		http.Error(w, "ingest preview failed", http.StatusInternalServerError)
		return
	}
	output(w, result)
}
