package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/hermes-apprentice/proxy/internal/patterns"
)

type patternsHandler struct {
	store  *patterns.Store
	logger *slog.Logger
}

func newPatternsHandler(store *patterns.Store, logger *slog.Logger) *patternsHandler {
	return &patternsHandler{store: store, logger: logger}
}

// handleRegister upserts a pattern.  The body shape matches the detector's
// approved-pattern manifest extended with specialist_url:
//
//	{ "id": "...", "description": "...", "centroid": [...float32...], "specialist_url": "http://..." }
func (h *patternsHandler) handleRegister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.store == nil {
		writeError(w, http.StatusInternalServerError, "pattern store not available")
		return
	}

	var p patterns.Pattern
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := h.store.Upsert(p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.logger.Info("pattern registered",
		"pattern_id", p.ID,
		"specialist_url", p.SpecialistURL,
		"centroid_len", len(p.Centroid),
	)
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(p)
}

func (h *patternsHandler) handleList(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.store == nil {
		_, _ = w.Write([]byte("[]"))
		return
	}
	list := h.store.List()
	if list == nil {
		list = []patterns.Pattern{}
	}
	_ = json.NewEncoder(w).Encode(list)
}
