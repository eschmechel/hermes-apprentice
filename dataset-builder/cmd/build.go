package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/eschmechel/hermes-apprentice/dataset-builder/internal/augment"
	"github.com/eschmechel/hermes-apprentice/dataset-builder/internal/dedup"
	"github.com/eschmechel/hermes-apprentice/dataset-builder/internal/fetcher"
	"github.com/eschmechel/hermes-apprentice/dataset-builder/internal/quality"
	"github.com/eschmechel/hermes-apprentice/dataset-builder/internal/redact"
	"github.com/eschmechel/hermes-apprentice/dataset-builder/internal/splitter"
	"github.com/eschmechel/hermes-apprentice/dataset-builder/internal/versioned"
	"github.com/spf13/cobra"
)

func buildCmd() *cobra.Command {
	var (
		patternID        string
		observerURL      string
		presidioURL      string
		systemPrompt     string
		systemPromptFile string
		outputDir        string
		model            string
		apiKey           string
		pruneKeep        int
		pruneOlderThan   string
	)

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build a training dataset for a pattern.",
		Long: `Fetches approved records from the observer, redacts PII via Presidio,
quality-filters, fuzzy-deduplicates, optionally augments via a teacher model,
splits 80/10/10, and writes gzipped Hermes JSONL into a versioned output directory.`,
		RunE: func(c *cobra.Command, _ []string) error {
			if patternID == "" {
				return fmt.Errorf("--pattern-id is required")
			}

			// Resolve system prompt.
			sp := systemPrompt
			if sp == "" && systemPromptFile != "" {
				data, err := os.ReadFile(systemPromptFile)
				if err != nil {
					return fmt.Errorf("read system prompt file: %w", err)
				}
				sp = string(data)
			}
			if sp == "" {
				sp = "You are Hermes, an AI assistant powered by Nous Research. You are helpful, accurate, and concise. Use tools when appropriate."
			}

			logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
			ctx := context.Background()

			logger.Info("dataset-builder build starting",
				"pattern_id", patternID,
				"observer_url", observerURL,
				"presidio_url", presidioURL,
				"output_dir", outputDir,
			)

			// 1. Fetch records from observer.
			fc := fetcher.NewClient(observerURL)
			records, err := fc.FetchAll(ctx, patternID)
			if err != nil {
				return fmt.Errorf("fetch records: %w", err)
			}
			logger.Info("fetched records", "count", len(records))
			originalCount := len(records)

			if len(records) == 0 {
				return fmt.Errorf("no records found for pattern %s", patternID)
			}

			// 2. PII redact.
			if presidioURL != "" {
				rc := redact.NewClient(presidioURL)
				for i := range records {
					redactedText, err := rc.Redact(ctx, records[i].InputText)
					if err != nil {
						logger.Warn("PII redaction failed for input, keeping original", "id", records[i].ID, "error", err)
					} else {
						records[i].InputText = redactedText
					}
					redactedOutput, err := rc.Redact(ctx, records[i].OutputText)
					if err != nil {
						logger.Warn("PII redaction failed for output, keeping original", "id", records[i].ID, "error", err)
					} else {
						records[i].OutputText = redactedOutput
					}
				}
				logger.Info("PII redacted", "count", len(records))
			}

			// 3. Quality filter (drop re-asks).
			records = quality.Filter(records)
			logger.Info("quality filtered", "count", len(records))

			// 4. Fuzzy dedup.
			records = dedup.Filter(records, dedup.Config{Threshold: 0.85})
			logger.Info("deduped", "count", len(records))

			// 5. Teacher augmentation for small datasets.
			augmentedCount := 0
			if apiKey != "" {
				augCfg := augment.Config{
					Model:   model,
					APIKey:  apiKey,
					MinTarget: 200,
				}
				a, err := augment.New(augCfg)
				if err != nil {
					logger.Warn("augmenter init failed, skipping", "error", err)
				} else if len(records) < a.MinTarget() {
					augmented, err := a.Augment(ctx, records)
					if err != nil {
						logger.Warn("augmentation failed, continuing", "error", err)
					} else {
						augmentedCount = len(augmented) - len(records)
						records = augmented
						logger.Info("augmented", "total", len(records), "added", augmentedCount)
					}
				}
			}

			// 6. Split 80/10/10.
			workDir, err := os.MkdirTemp("", "dataset-builder-*")
			if err != nil {
				return fmt.Errorf("temp dir: %w", err)
			}
			defer os.RemoveAll(workDir)

			trainPath, valPath, testPath, err := splitter.WriteSplits(
				workDir, sp, records, time.Now().UnixNano(),
			)
			if err != nil {
				return fmt.Errorf("write splits: %w", err)
			}
			logger.Info("splits written", "train", filepath.Base(trainPath), "val", filepath.Base(valPath), "test", filepath.Base(testPath))

			sr := splitter.Split(records, time.Now().UnixNano())
			trainN := len(sr.Train)
			valN := len(sr.Val)
			testN := len(sr.Test)

			// 7. Versioned save.
			v, verDir, err := versioned.Save(outputDir, patternID, workDir, originalCount, augmentedCount, trainN, valN, testN)
			if err != nil {
				return fmt.Errorf("versioned save: %w", err)
			}
			logger.Info("dataset saved", "version", v, "dir", verDir)

			// 8. Prune old versions.
			patternDir := filepath.Join(outputDir, patternID)
			if pruneKeep > 0 {
				if err := versioned.PruneKeep(patternDir, pruneKeep); err != nil {
					logger.Warn("prune-keep failed", "error", err)
				}
			}
			if pruneOlderThan != "" {
				dur, err := time.ParseDuration(pruneOlderThan)
				if err != nil {
					logger.Warn("invalid prune-older-than duration, skipping", "value", pruneOlderThan, "error", err)
				} else {
					if err := versioned.PruneOlderThan(patternDir, dur); err != nil {
						logger.Warn("prune-older-than failed", "error", err)
					}
				}
			}

			fmt.Printf("Dataset v%d built: %s\n  train: %d | val: %d | test: %d | total: %d\n",
				v, verDir, trainN, valN, testN, trainN+valN+testN)
			return nil
		},
	}
	cmd.Flags().StringVar(&patternID, "pattern-id", "", "Pattern UUID to build dataset for (required)")
	cmd.Flags().StringVar(&observerURL, "observer-url", "http://10.0.2.2:8080", "Observer base URL")
	cmd.Flags().StringVar(&presidioURL, "presidio-url", "http://localhost:5002", "Presidio analyzer base URL")
	cmd.Flags().StringVar(&systemPrompt, "system-prompt", "", "Override default Hermes system prompt")
	cmd.Flags().StringVar(&systemPromptFile, "system-prompt-file", "", "Read system prompt from file")
	cmd.Flags().StringVar(&outputDir, "output-dir", os.ExpandEnv("$HOME/.apprentice/datasets"), "Directory for versioned dataset output")
	cmd.Flags().StringVar(&model, "model", "deepseek-v4-pro", "OpenRouter model for teacher augmentation")
	cmd.Flags().StringVar(&apiKey, "api-key", os.Getenv("OPENROUTER_API_KEY"), "OpenRouter API key (defaults to OPENROUTER_API_KEY env)")
	cmd.Flags().IntVar(&pruneKeep, "prune-keep", 3, "Keep last N versions; prune older ones")
	cmd.Flags().StringVar(&pruneOlderThan, "prune-older-than", "", "Prune versions older than this duration (e.g. 720h, 30d)")
	return cmd
}
