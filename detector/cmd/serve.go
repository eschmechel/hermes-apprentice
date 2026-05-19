package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

func serveCmd() *cobra.Command {
	var (
		listenAddr     string
		observerURL    string
		stateDir       string
		freshnessHours int
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the detector: pull observer records, embed, cluster, emit pattern candidates.",
		RunE: func(c *cobra.Command, _ []string) error {
			logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
			logger.Info("detector starting",
				"listen", listenAddr,
				"observer_url", observerURL,
				"state_dir", stateDir,
				"freshness_hours", freshnessHours,
				"version", Version,
			)
			_, stop := signal.NotifyContext(c.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			// Pipeline wiring lands in later subtasks. For now `serve` is a
			// no-op shell that proves the scaffold parses flags and exits clean.
			return fmt.Errorf("detector serve is not yet wired (see detector-01..06)")
		},
	}
	cmd.Flags().StringVar(&listenAddr, "listen", ":8081", "HTTP listen address")
	cmd.Flags().StringVar(&observerURL, "observer-url", "http://10.0.2.2:8080", "Observer base URL (records source)")
	cmd.Flags().StringVar(&stateDir, "state-dir", os.ExpandEnv("$HOME/.apprentice/detector"), "Detector state directory (dedup DB, patterns/, embeddings cache)")
	cmd.Flags().IntVar(&freshnessHours, "freshness-hours", 24, "Hours within which a repeated input is considered a duplicate (skipped before embedding)")
	return cmd
}
