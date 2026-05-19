package cmd

import (
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/hermes-apprentice/detector/internal/httpapi"
	"github.com/hermes-apprentice/detector/internal/patternstore"
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
			ctx, stop := signal.NotifyContext(c.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			ps, err := patternstore.Open(filepath.Join(stateDir, "patterns"))
			if err != nil {
				return err
			}

			srv := httpapi.New(httpapi.Config{
				Addr:         listenAddr,
				Logger:       logger,
				PatternStore: ps,
			})

			return srv.ListenAndServe(ctx)
		},
	}
	cmd.Flags().StringVar(&listenAddr, "listen", ":8081", "HTTP listen address")
	cmd.Flags().StringVar(&observerURL, "observer-url", "http://10.0.2.2:8080", "Observer base URL (records source)")
	cmd.Flags().StringVar(&stateDir, "state-dir", os.ExpandEnv("$HOME/.apprentice/detector"), "Detector state directory (dedup DB, patterns/, embeddings cache)")
	cmd.Flags().IntVar(&freshnessHours, "freshness-hours", 24, "Hours within which a repeated input is considered a duplicate (skipped before embedding)")
	return cmd
}
