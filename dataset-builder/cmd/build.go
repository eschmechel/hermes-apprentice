package cmd

import (
	"fmt"
	"log/slog"
	"os"

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
			logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
			logger.Info("dataset-builder build starting",
				"pattern_id", patternID,
				"observer_url", observerURL,
				"presidio_url", presidioURL,
				"output_dir", outputDir,
				"version", Version,
			)

			// TODO: pipeline wired in subsequent subtasks
			return fmt.Errorf("pipeline not yet wired — subtask 02-08 pending")
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
