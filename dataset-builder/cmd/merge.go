package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/eschmechel/hermes-apprentice/dataset-builder/internal/dedup"
	"github.com/eschmechel/hermes-apprentice/dataset-builder/internal/fetcher"
	"github.com/eschmechel/hermes-apprentice/dataset-builder/internal/quality"
	"github.com/eschmechel/hermes-apprentice/dataset-builder/internal/redact"
	"github.com/eschmechel/hermes-apprentice/dataset-builder/internal/splitter"
	"github.com/eschmechel/hermes-apprentice/dataset-builder/internal/versioned"
	"github.com/spf13/cobra"
)

type mergeSourceEntry struct {
	PatternID string `json:"pattern_id"`
	Records   int    `json:"records"`
}

type dataCard struct {
	Schema      string            `json:"schema"`
	Version     int               `json:"version"`
	MergedAt    string            `json:"merged_at"`
	MergedID    string            `json:"merged_id"`
	Sources     []mergeSourceEntry `json:"sources"`
	TotalBefore int               `json:"total_before_dedup"`
	TotalAfter  int               `json:"total_after_dedup"`
	TrainCount  int               `json:"train_count"`
	ValCount    int               `json:"val_count"`
	TestCount   int               `json:"test_count"`
}

func mergeCmd() *cobra.Command {
	var (
		patternA     string
		patternB     string
		mergedID     string
		observerURL  string
		presidioURL  string
		systemPrompt string
		outputDir    string
		pruneKeep    int
	)

	cmd := &cobra.Command{
		Use:   "merge",
		Short: "Merge datasets from two patterns into one.",
		Long: `Fetches records for two patterns, redacts PII, quality-filters,
fuzzy-deduplicates across both sets, splits 80/10/10, and writes
a versioned merged dataset with a data_card tracking lineage.`,
		RunE: func(c *cobra.Command, _ []string) error {
			if patternA == "" || patternB == "" {
				return fmt.Errorf("--pattern-a and --pattern-b are required")
			}
			if mergedID == "" {
				mergedID = patternA + "+" + patternB
			}
			if systemPrompt == "" {
				systemPrompt = "You are Hermes, an AI assistant powered by Nous Research. You are helpful, accurate, and concise. Use tools when appropriate."
			}

			logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
			ctx := context.Background()

			logger.Info("dataset-builder merge starting",
				"pattern_a", patternA,
				"pattern_b", patternB,
				"merged_id", mergedID,
				"observer_url", observerURL,
				"output_dir", outputDir,
			)

			fc := fetcher.NewClient(observerURL)
			rc := redact.NewClient(presidioURL)

			// Fetch both patterns.
			recordsA, err := fc.FetchAll(ctx, patternA)
			if err != nil {
				return fmt.Errorf("fetch pattern %s: %w", patternA, err)
			}
			logger.Info("fetched pattern A", "pattern_id", patternA, "count", len(recordsA))

			recordsB, err := fc.FetchAll(ctx, patternB)
			if err != nil {
				return fmt.Errorf("fetch pattern %s: %w", patternB, err)
			}
			logger.Info("fetched pattern B", "pattern_id", patternB, "count", len(recordsB))

			totalBefore := len(recordsA) + len(recordsB)

			// Redact PII for both sets.
			if presidioURL != "" {
				for i := range recordsA {
					if in, err := rc.Redact(ctx, recordsA[i].InputText); err == nil {
						recordsA[i].InputText = in
					}
					if out, err := rc.Redact(ctx, recordsA[i].OutputText); err == nil {
						recordsA[i].OutputText = out
					}
				}
				for i := range recordsB {
					if in, err := rc.Redact(ctx, recordsB[i].InputText); err == nil {
						recordsB[i].InputText = in
					}
					if out, err := rc.Redact(ctx, recordsB[i].OutputText); err == nil {
						recordsB[i].OutputText = out
					}
				}
				logger.Info("PII redacted")
			}

			// Quality filter (drop re-asks) each set separately.
			recordsA = quality.Filter(recordsA)
			recordsB = quality.Filter(recordsB)
			logger.Info("quality filtered",
				"pattern_a", len(recordsA),
				"pattern_b", len(recordsB),
			)

			// Combine and dedup across both sets.
			combined := append(recordsA, recordsB...)
			deduped := dedup.Filter(combined, dedup.Config{Threshold: 0.85})
			dedupRemoved := len(combined) - len(deduped)
			totalAfter := len(deduped)
			logger.Info("deduped across both patterns",
				"combined", len(combined),
				"removed", dedupRemoved,
				"after", totalAfter,
			)

			if totalAfter == 0 {
				return fmt.Errorf("no records remain after dedup for merged pattern %s", mergedID)
			}

			// Split 80/10/10.
			workDir, err := os.MkdirTemp("", "dataset-merge-*")
			if err != nil {
				return fmt.Errorf("temp dir: %w", err)
			}
			defer os.RemoveAll(workDir)

			seed := time.Now().UnixNano()
			trainPath, valPath, testPath, err := splitter.WriteSplits(workDir, systemPrompt, deduped, seed)
			if err != nil {
				return fmt.Errorf("write splits: %w", err)
			}

			sr := splitter.Split(deduped, seed)
			logger.Info("splits written",
				"train", filepath.Base(trainPath),
				"val", filepath.Base(valPath),
				"test", filepath.Base(testPath),
			)

			// Versioned save under merged ID.
			origCount := totalAfter
			v, verDir, err := versioned.Save(outputDir, mergedID, workDir,
				origCount, 0, len(sr.Train), len(sr.Val), len(sr.Test))
			if err != nil {
				return fmt.Errorf("versioned save: %w", err)
			}
			logger.Info("merged dataset saved", "version", v, "dir", verDir)

			// Write data_card.json with lineage.
			card := dataCard{
				Schema:  "apprentice-merge-data-card",
				Version: 1,
				MergedAt: time.Now().UTC().Format(time.RFC3339),
				MergedID: mergedID,
				Sources: []mergeSourceEntry{
					{PatternID: patternA, Records: len(recordsA)},
					{PatternID: patternB, Records: len(recordsB)},
				},
				TotalBefore: totalBefore,
				TotalAfter:  totalAfter,
				TrainCount:  len(sr.Train),
				ValCount:    len(sr.Val),
				TestCount:   len(sr.Test),
			}

			cardPath := filepath.Join(verDir, "data_card.json")
			cardData, err := json.MarshalIndent(card, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal data card: %w", err)
			}
			if err := os.WriteFile(cardPath, cardData, 0o644); err != nil {
				return fmt.Errorf("write data card: %w", err)
			}

			// Patch lineage into manifest.
			manifestPath := filepath.Join(verDir, "manifest.json")
			if manifestData, err := os.ReadFile(manifestPath); err == nil {
				var m map[string]any
				if err := json.Unmarshal(manifestData, &m); err == nil {
					m["merged_from"] = []map[string]any{
						{"pattern_id": patternA, "records": len(recordsA)},
						{"pattern_id": patternB, "records": len(recordsB)},
					}
					if patched, err := json.MarshalIndent(m, "", "  "); err == nil {
						_ = os.WriteFile(manifestPath, patched, 0o644)
					}
				}
			}

			// Prune old versions of the merged pattern.
			patternDir := filepath.Join(outputDir, mergedID)
			if pruneKeep > 0 {
				if err := versioned.PruneKeep(patternDir, pruneKeep); err != nil {
					logger.Warn("prune-keep failed", "error", err)
				}
			}

			fmt.Printf("Merged dataset v%d saved: %s\n", v, verDir)
			fmt.Printf("  Sources: %s (%d) + %s (%d)\n", patternA, len(recordsA), patternB, len(recordsB))
			fmt.Printf("  Total: %d (dedup removed %d)\n", totalAfter, dedupRemoved)
			fmt.Printf("  Train: %d | Val: %d | Test: %d\n", len(sr.Train), len(sr.Val), len(sr.Test))
			return nil
		},
	}

	cmd.Flags().StringVar(&patternA, "pattern-a", "", "First pattern ID to merge (required)")
	cmd.Flags().StringVar(&patternB, "pattern-b", "", "Second pattern ID to merge (required)")
	cmd.Flags().StringVar(&mergedID, "merged-id", "", "Pattern ID for merged result (default: <pattern-a>+<pattern-b>)")
	cmd.Flags().StringVar(&observerURL, "observer-url", "http://10.0.2.2:8080", "Observer base URL")
	cmd.Flags().StringVar(&presidioURL, "presidio-url", "http://localhost:5002", "Presidio analyzer base URL")
	cmd.Flags().StringVar(&systemPrompt, "system-prompt", "", "Override default Hermes system prompt")
	cmd.Flags().StringVar(&outputDir, "output-dir", os.ExpandEnv("$HOME/.apprentice/datasets"), "Directory for versioned dataset output")
	cmd.Flags().IntVar(&pruneKeep, "prune-keep", 3, "Keep last N versions; prune older ones")
	return cmd
}
