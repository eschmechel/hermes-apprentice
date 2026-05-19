// Package splitter divides records into train/val/test splits, writes
// gzipped Hermes-compatible JSONL, and provides a top-level WriteSplits
// that handles the full 80/10/10 flow.  Satisfies dataset-builder-07.
package splitter

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"

	"github.com/hermes-apprentice/dataset-builder/internal/fetcher"
)

// Fraction constants for the default 80/10/10 split.
const (
	TrainFrac = 0.80
	ValFrac   = 0.10
	TestFrac  = 0.10
)

// SplitResult holds the three splits after deterministic shuffling.
type SplitResult struct {
	Train []fetcher.Record
	Val   []fetcher.Record
	Test  []fetcher.Record
}

// Split deterministically shuffles records with the given seed and
// partitions them into 80% train, 10% val, 10% test.
func Split(records []fetcher.Record, seed int64) SplitResult {
	n := len(records)
	// Copy and shuffle.
	shuffled := make([]fetcher.Record, n)
	copy(shuffled, records)
	rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed>>1)))
	rng.Shuffle(n, func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	trainEnd := int(float64(n) * TrainFrac)
	if trainEnd == 0 && n >= 1 {
		trainEnd = 1
	}
	valEnd := trainEnd + int(float64(n)*ValFrac)
	if valEnd == trainEnd && n >= 2 && n-trainEnd >= 1 {
		valEnd = trainEnd + 1
	}
	// Clamp boundaries to n.
	if trainEnd > n {
		trainEnd = n
	}
	if valEnd < trainEnd {
		valEnd = trainEnd
	}
	if valEnd > n {
		valEnd = n
	}

	return SplitResult{
		Train: shuffled[:trainEnd],
		Val:   shuffled[trainEnd:valEnd],
		Test:  shuffled[valEnd:],
	}
}

// A hermesMessage represents one turn in the Hermes chat template.
type hermesMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type hermesExample struct {
	Messages []hermesMessage `json:"messages"`
}

// Write serialises records as gzipped Hermes JSONL to path.  Each line is
// {"messages":[{"role":"system",...},{"role":"user",...},{"role":"assistant",...}]}.
func Write(path string, records []fetcher.Record, systemPrompt string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	enc := json.NewEncoder(gw)
	for _, r := range records {
		ex := hermesExample{
			Messages: []hermesMessage{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: r.InputText},
				{Role: "assistant", Content: r.OutputText},
			},
		}
		if err := enc.Encode(ex); err != nil {
			return fmt.Errorf("encode: %w", err)
		}
	}
	return gw.Close()
}

// WriteSplits shuffles records with seed, splits 80/10/10, and writes
// train.jsonl.gz, val.jsonl.gz, test.jsonl.gz into outDir.  Returns the
// three output paths and a non-nil error for any failure.
func WriteSplits(outDir string, systemPrompt string, records []fetcher.Record, seed int64) (train, val, test string, err error) {
	sr := Split(records, seed)
	train = filepath.Join(outDir, "train.jsonl.gz")
	val = filepath.Join(outDir, "val.jsonl.gz")
	test = filepath.Join(outDir, "test.jsonl.gz")

	if err := Write(train, sr.Train, systemPrompt); err != nil {
		return "", "", "", fmt.Errorf("train: %w", err)
	}
	if err := Write(val, sr.Val, systemPrompt); err != nil {
		return "", "", "", fmt.Errorf("val: %w", err)
	}
	if err := Write(test, sr.Test, systemPrompt); err != nil {
		return "", "", "", fmt.Errorf("test: %w", err)
	}
	return train, val, test, nil
}

// sortedCopy returns a copy of records sorted by ID for deterministic
// comparison in tests.
func sortedCopy(records []fetcher.Record) []fetcher.Record {
	out := make([]fetcher.Record, len(records))
	copy(out, records)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
