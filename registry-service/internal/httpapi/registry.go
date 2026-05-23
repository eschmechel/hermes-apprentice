package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type RegistryHandler struct {
	registryDir string
	logger      *slog.Logger
}

func NewRegistryHandler(registryDir string, logger *slog.Logger) *RegistryHandler {
	return &RegistryHandler{
		registryDir: registryDir,
		logger:      logger,
	}
}

func (h *RegistryHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /registry/{skillId}/latest", h.handleLatest)
}

type latestResponse struct {
	SkillID  string          `json:"skill_id"`
	Version  int             `json:"version"`
	Manifest json.RawMessage `json:"manifest"`
	Found    bool            `json:"found"`
}

func (h *RegistryHandler) handleLatest(w http.ResponseWriter, r *http.Request) {
	skillID := r.PathValue("skillId")
	if skillID == "" {
		writeError(w, http.StatusBadRequest, "skillId is required")
		return
	}

	skillDir := filepath.Join(h.registryDir, skillID)
	latest, err := findLatest(skillDir)
	if err != nil {
		h.logger.Error("registry lookup failed", "skill_id", skillID, "err", err)
		writeError(w, http.StatusInternalServerError, "registry lookup failed")
		return
	}

	if latest == 0 {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(latestResponse{
			SkillID: skillID,
			Found:   false,
		})
		return
	}

	manifestPath := filepath.Join(skillDir, "v"+strconv.Itoa(latest), "registry_manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		h.logger.Error("manifest read failed", "skill_id", skillID, "version", latest, "err", err)
		writeError(w, http.StatusInternalServerError, "manifest read failed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(latestResponse{
		SkillID:  skillID,
		Version:  latest,
		Manifest: json.RawMessage(data),
		Found:    true,
	})
}

func findLatest(skillDir string) (int, error) {
	// Honor the `latest` symlink (W10 demote repoints it) before falling back
	// to the highest v<N> on disk.
	if target, err := os.Readlink(filepath.Join(skillDir, "latest")); err == nil {
		name := filepath.Base(target)
		if strings.HasPrefix(name, "v") {
			if v, e := strconv.Atoi(name[1:]); e == nil && v > 0 {
				if fi, e2 := os.Stat(filepath.Join(skillDir, name)); e2 == nil && fi.IsDir() {
					return v, nil
				}
			}
		}
	}

	entries, err := os.ReadDir(skillDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	versions := make([]int, 0)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "v") {
			continue
		}
		v, err := strconv.Atoi(name[1:])
		if err != nil || v <= 0 {
			continue
		}
		versions = append(versions, v)
	}

	if len(versions) == 0 {
		return 0, nil
	}
	sort.Ints(versions)
	return versions[len(versions)-1], nil
}
