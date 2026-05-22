package alias

import (
	"encoding/json"
	"net/http"
)

type Handler struct {
	store *Store
}

func NewHandler(store *Store) *Handler {
	return &Handler{store: store}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /aliases", h.handleList)
	mux.HandleFunc("POST /aliases", h.handleRegister)
	mux.HandleFunc("DELETE /aliases/{alias_id}", h.handleRemove)
	mux.HandleFunc("GET /aliases/{alias_id}", h.handleResolve)
}

type registerRequest struct {
	AliasID  string `json:"alias_id"`
	TargetID string `json:"target_id"`
}

func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if err := h.store.Register(req.AliasID, req.TargetID); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	entries := h.store.List()
	if entries == nil {
		entries = []Entry{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

func (h *Handler) handleRemove(w http.ResponseWriter, r *http.Request) {
	aliasID := r.PathValue("alias_id")
	if aliasID == "" {
		http.Error(w, `{"error":"alias_id is required"}`, http.StatusBadRequest)
		return
	}
	if err := h.store.Remove(aliasID); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) handleResolve(w http.ResponseWriter, r *http.Request) {
	aliasID := r.PathValue("alias_id")
	targetID, ok := h.store.Resolve(aliasID)
	if !ok {
		http.Error(w, `{"error":"alias not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"alias_id":  aliasID,
		"target_id": targetID,
	})
}
