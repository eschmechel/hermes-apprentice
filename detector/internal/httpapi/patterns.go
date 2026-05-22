package httpapi

import (
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/eschmechel/hermes-apprentice/detector/internal/patternstore"
)

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type PatternHandler struct {
	store *patternstore.Store
}

func NewPatternHandler(store *patternstore.Store) *PatternHandler {
	return &PatternHandler{store: store}
}

func (h *PatternHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /patterns", h.handleList)
	mux.HandleFunc("POST /patterns/{id}/approve", h.handleApprove)
}

func (h *PatternHandler) handleList(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if h.store == nil {
		_, _ = w.Write([]byte("[]"))
		return
	}
	patterns, err := h.store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list patterns failed")
		return
	}
	if patterns == nil {
		patterns = []patternstore.Manifest{}
	}
	_ = json.NewEncoder(w).Encode(patterns)
}

func (h *PatternHandler) handleApprove(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing pattern id")
		return
	}
	if !uuidRE.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid pattern id format")
		return
	}
	if h.store == nil {
		writeError(w, http.StatusInternalServerError, "pattern store not available")
		return
	}
	if err := h.store.SetStatus(id, patternstore.StatusApproved); err != nil {
		writeError(w, http.StatusNotFound, "pattern not found")
		return
	}
	m, err := h.store.Load(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "re-read after approve failed")
		return
	}
	_ = json.NewEncoder(w).Encode(m)
}
