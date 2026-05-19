// Package versioned persists datasets under ~/.apprentice/datasets/<pattern-id>/v<n>/
// with a manifest.json per version, and provides version pruning.  Satisfies
// dataset-builder-08.
package versioned

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Manifest captures metadata about one versioned dataset.
type Manifest struct {
	Version           int       `json:"version"`
	PatternID         string    `json:"pattern_id"`
	CreatedAt         time.Time `json:"created_at"`
	SHA256            string    `json:"sha256"`
	TrainCount        int       `json:"train_count"`
	ValCount          int       `json:"val_count"`
	TestCount         int       `json:"test_count"`
	TotalCount        int       `json:"total_count"`
	OriginalCount     int       `json:"original_count"`
	AugmentationCount int       `json:"augmentation_count"`
}

// Save copies train/val/test gzip files from workDir into
// <baseDir>/<patternID>/v<N>/ and writes manifest.json.  Returns the version
// number and the version directory path.
func Save(baseDir, patternID string, workDir string, originalCount, augmentedCount, trainN, valN, testN int) (int, string, error) {
	patternDir := filepath.Join(baseDir, patternID)
	nextV, err := nextVersion(patternDir)
	if err != nil {
		return 0, "", fmt.Errorf("next version: %w", err)
	}

	verDir := filepath.Join(patternDir, fmt.Sprintf("v%d", nextV))
	if err := os.MkdirAll(verDir, 0o755); err != nil {
		return 0, "", fmt.Errorf("mkdir: %w", err)
	}

	// Copy train/val/test gzip files.
	for _, fn := range []string{"train.jsonl.gz", "val.jsonl.gz", "test.jsonl.gz"} {
		if err := copyFile(
			filepath.Join(workDir, fn),
			filepath.Join(verDir, fn),
		); err != nil {
			return 0, "", fmt.Errorf("copy %s: %w", fn, err)
		}
	}

	// Compute SHA-256 over the three gzip files.
	hasher := sha256.New()
	for _, fn := range []string{"train.jsonl.gz", "val.jsonl.gz", "test.jsonl.gz"} {
		f, err := os.Open(filepath.Join(verDir, fn))
		if err != nil {
			return 0, "", fmt.Errorf("open %s for hash: %w", fn, err)
		}
		if _, err := io.Copy(hasher, f); err != nil {
			f.Close()
			return 0, "", fmt.Errorf("hash %s: %w", fn, err)
		}
		f.Close()
	}

	m := Manifest{
		Version:           nextV,
		PatternID:         patternID,
		CreatedAt:         time.Now().UTC(),
		SHA256:            hex.EncodeToString(hasher.Sum(nil)),
		TrainCount:        trainN,
		ValCount:          valN,
		TestCount:         testN,
		TotalCount:        trainN + valN + testN,
		OriginalCount:     originalCount,
		AugmentationCount: augmentedCount,
	}

	manifestPath := filepath.Join(verDir, "manifest.json")
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return 0, "", fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		return 0, "", fmt.Errorf("write manifest: %w", err)
	}

	return nextV, verDir, nil
}

// Load reads the manifest at <dir>/manifest.json.
func Load(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal manifest: %w", err)
	}
	return &m, nil
}

// PruneKeep removes all but the most recent keep versions in the pattern
// directory.  Versions are sorted numerically; the highest keep versions
// are retained.
func PruneKeep(patternDir string, keep int) error {
	return prune(patternDir, keep, 0)
}

// PruneOlderThan removes versions whose CreatedAt is older than maxAge.
func PruneOlderThan(patternDir string, maxAge time.Duration) error {
	return prune(patternDir, 0, maxAge)
}

func prune(patternDir string, keep int, maxAge time.Duration) error {
	entries, err := os.ReadDir(patternDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	type versionEntry struct {
		dir       string
		version   int
		createdAt time.Time
	}
	var vers []versionEntry
	cutoff := time.Now().UTC().Add(-maxAge)

	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "v") {
			continue
		}
		v, err := strconv.Atoi(e.Name()[1:])
		if err != nil {
			continue
		}
		m, err := Load(filepath.Join(patternDir, e.Name()))
		var ct time.Time
		if err == nil {
			ct = m.CreatedAt
		}
		vers = append(vers, versionEntry{
			dir:       filepath.Join(patternDir, e.Name()),
			version:   v,
			createdAt: ct,
		})
	}

	// Sort by version ascending.
	sort.Slice(vers, func(i, j int) bool { return vers[i].version < vers[j].version })

	for i, ve := range vers {
		remove := false
		if maxAge > 0 && !ve.createdAt.IsZero() && ve.createdAt.Before(cutoff) {
			remove = true
		}
		if keep > 0 && i < len(vers)-keep {
			remove = true
		}
		if remove {
			if err := os.RemoveAll(ve.dir); err != nil {
				return fmt.Errorf("remove %s: %w", ve.dir, err)
			}
		}
	}
	return nil
}

// Decompress reads a .gz file from srcPath and writes plain JSONL to
// dstPath.  If dstPath is empty the output replaces the ".gz" extension.
func Decompress(srcPath string, dstPath string) error {
	if dstPath == "" {
		dstPath = strings.TrimSuffix(srcPath, ".gz")
		if dstPath == srcPath {
			return fmt.Errorf("src does not end with .gz: %s", srcPath)
		}
	}

	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	out, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, gr); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

// nextVersion returns the next available version number by scanning
// <patternDir>/v<n>/ directories.
func nextVersion(patternDir string) (int, error) {
	entries, err := os.ReadDir(patternDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, err
	}
	maxV := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "v") {
			continue
		}
		v, err := strconv.Atoi(e.Name()[1:])
		if err != nil {
			continue
		}
		if v > maxV {
			maxV = v
		}
	}
	return maxV + 1, nil
}

// copyFile copies src to dst, preserving mode 0o644.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
