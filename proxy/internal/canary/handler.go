package canary

import (
	"encoding/json"
	"net/http"
)

type ManagerAPI struct {
	mgr *Manager
}

func NewHandler(mgr *Manager) *ManagerAPI {
	return &ManagerAPI{mgr: mgr}
}

func (api *ManagerAPI) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /canary/state", api.handleListState)
	mux.HandleFunc("GET /canary/state/{pattern_id}", api.handleGetState)
	mux.HandleFunc("POST /canary/advance", api.handleAdvance)
	mux.HandleFunc("POST /canary/set-state", api.handleSetState)
	mux.HandleFunc("POST /canary/compare", api.handleCompare)
}

type advanceRequest struct {
	PatternID string  `json:"pattern_id"`
	Score     float64 `json:"score"`
}

func (api *ManagerAPI) handleAdvance(w http.ResponseWriter, r *http.Request) {
	var req advanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	state, transitioned, err := api.mgr.Advance(req.PatternID, req.Score)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	resp := map[string]any{
		"pattern_id":   req.PatternID,
		"state":        state,
		"transitioned": transitioned,
	}
	if state == StateBroken && transitioned {
		alert := api.mgr.SendAlert(req.PatternID)
		if alert != "" {
			resp["alert"] = alert
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type setStateRequest struct {
	PatternID string    `json:"pattern_id"`
	State     RampState `json:"state"`
	Pct       int       `json:"pct"`
}

func (api *ManagerAPI) handleSetState(w http.ResponseWriter, r *http.Request) {
	var req setStateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if err := api.mgr.SetState(req.PatternID, req.State, req.Pct); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (api *ManagerAPI) handleListState(w http.ResponseWriter, r *http.Request) {
	states := api.mgr.List()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(states)
}

func (api *ManagerAPI) handleGetState(w http.ResponseWriter, r *http.Request) {
	patternID := r.PathValue("pattern_id")
	state, ok := api.mgr.State(patternID)
	if !ok {
		http.Error(w, `{"error":"pattern not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

type compareRequest struct {
	Specialist []byte `json:"specialist"`
	Upstream   []byte `json:"upstream"`
}

func (api *ManagerAPI) handleCompare(w http.ResponseWriter, r *http.Request) {
	var req compareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	score := CompareResponses(req.Specialist, req.Upstream)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]float64{"agreement": score})
}
