package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/eschmechel/hermes-apprentice/proxy/internal/patterns"
)

type patternsHandler struct {
	store  *patterns.Store
	logger *slog.Logger
}

func newPatternsHandler(store *patterns.Store, logger *slog.Logger) *patternsHandler {
	return &patternsHandler{store: store, logger: logger}
}

// handleRegister upserts a pattern.  The caller's tenant is injected into the
// pattern from the X-Apprentice-Tenant header (validated by auth middleware).
// The body shape matches the detector's approved-pattern manifest:
//
//	{ "id": "...", "description": "...", "centroid": [...float32...], "specialist_url": "http://..." }
//
// The caller may not override tenant_id in the body — it is set from the auth
// context.  Only the global tenant (owning the admin API key) may register
// global patterns (tenant_id="").
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

	// Set tenant from auth context.
	tenantID := TenantFromContext(r.Context())
	if tenantID == "global" {
		p.TenantID = "" // global patterns have empty tenant_id
	} else {
		p.TenantID = tenantID
	}

	if err := h.store.Upsert(p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.logger.Info("pattern registered",
		"pattern_id", p.ID,
		"specialist_url", p.SpecialistURL,
		"centroid_len", len(p.Centroid),
		"tenant_id", p.TenantID,
	)
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(p)
}

func (h *patternsHandler) handleList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.store == nil {
		_, _ = w.Write([]byte("[]"))
		return
	}
	tenantID := TenantFromContext(r.Context())
	var list []patterns.Pattern
	if tenantID == "global" {
		list = h.store.List()
	} else {
		list = h.store.ListByTenant(tenantID)
	}
	if list == nil {
		list = []patterns.Pattern{}
	}
	_ = json.NewEncoder(w).Encode(list)
}
