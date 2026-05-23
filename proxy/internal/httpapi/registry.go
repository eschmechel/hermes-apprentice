package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// registryHandler serves GET /api/registry/{pattern_id}/latest
// by reading the highest v<N> registry manifest on disk.
type registryHandler struct {
	registryRoot string
	logger       *slog.Logger
}

func newRegistryHandler(registryRoot string, logger *slog.Logger) *registryHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &registryHandler{registryRoot: registryRoot, logger: logger}
}

func (rh *registryHandler) handleLatest(w http.ResponseWriter, r *http.Request) {
	patternID := r.PathValue("pattern_id")
	skillDir := filepath.Join(rh.registryRoot, patternID)

	latest := resolveLatest(skillDir)
	if latest == "" {
		writeError(w, http.StatusNotFound, "no promoted versions for pattern '"+patternID+"'")
		return
	}

	manifestPath := filepath.Join(skillDir, latest, "registry_manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		rh.logger.Warn("registry manifest read failed", "path", manifestPath, "err", err)
		writeError(w, http.StatusNotFound, "registry manifest not found for pattern '"+patternID+"'")
		return
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		rh.logger.Warn("registry manifest parse failed", "path", manifestPath, "err", err)
		writeError(w, http.StatusInternalServerError, "invalid registry manifest")
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// resolveLatest returns the version directory name the registry considers
// current: the `latest` symlink's target if present (W10 demote repoints it),
// otherwise the highest v<N>. Empty string when the skill has no versions.
func resolveLatest(skillDir string) string {
	if target, err := os.Readlink(filepath.Join(skillDir, "latest")); err == nil {
		name := filepath.Base(target)
		if len(name) > 1 && name[0] == 'v' {
			if _, e := strconv.Atoi(name[1:]); e == nil {
				if fi, e2 := os.Stat(filepath.Join(skillDir, name)); e2 == nil && fi.IsDir() {
					return name
				}
			}
		}
	}
	vers := findVersionDirs(skillDir)
	if len(vers) == 0 {
		return ""
	}
	return vers[len(vers)-1]
}

// findVersionDirs returns sorted ascending v<N> directory names under skillDir.
func findVersionDirs(skillDir string) []string {
	entries, err := os.ReadDir(skillDir)
	if err != nil {
		return nil
	}
	var vers []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < 2 || name[0] != 'v' {
			continue
		}
		if _, err := strconv.Atoi(name[1:]); err == nil {
			vers = append(vers, name)
		}
	}
	sort.Slice(vers, func(i, j int) bool {
		ni, _ := strconv.Atoi(vers[i][1:])
		nj, _ := strconv.Atoi(vers[j][1:])
		return ni < nj
	})
	return vers
}
