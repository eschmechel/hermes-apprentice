package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hermes-apprentice/observer/internal/httpapi"
	"github.com/hermes-apprentice/observer/internal/poller"
	"github.com/spf13/cobra"
)

func serveCmd() *cobra.Command {
	var (
		listenAddr      string
		hermesDBPath    string
		observerDB      string
		pollInterval    time.Duration
		fromBeginning   bool
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the observer: poll Hermes DB, write normalized records, serve HTTP.",
		RunE: func(c *cobra.Command, _ []string) error {
			logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
			logger.Info("observer starting",
				"listen", listenAddr,
				"hermes_db", hermesDBPath,
				"observer_db", observerDB,
				"poll_interval", pollInterval,
				"version", Version,
			)

			ctx, stop := signal.NotifyContext(c.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			srv := httpapi.New(httpapi.Config{
				Addr:   listenAddr,
				Logger: logger,
			})

			poll := poller.New(poller.Config{
				HermesDBPath:       hermesDBPath,
				Interval:           pollInterval,
				Logger:             logger.With("component", "poller"),
				StartFromBeginning: fromBeginning,
			})

			httpErr := make(chan error, 1)
			pollErr := make(chan error, 1)
			go func() { httpErr <- srv.ListenAndServe(ctx) }()
			go func() { pollErr <- poll.Run(ctx) }()

			select {
			case <-ctx.Done():
				logger.Info("shutdown signal received")
				return srv.Shutdown(context.Background())
			case err := <-httpErr:
				if err != nil {
					return fmt.Errorf("http server: %w", err)
				}
				return nil
			case err := <-pollErr:
				// Cancelled by context = clean shutdown, not a failure.
				if err == nil || err == context.Canceled {
					return srv.Shutdown(context.Background())
				}
				return fmt.Errorf("poller: %w", err)
			}
		},
	}

	cmd.Flags().StringVar(&listenAddr, "listen", ":8080", "HTTP listen address")
	cmd.Flags().StringVar(&hermesDBPath, "hermes-db", "/root/.hermes/state.db", "Path to the Hermes session DB to tail")
	cmd.Flags().StringVar(&observerDB, "observer-db", "/root/.apprentice/observer.db", "Path to the observer's own SQLite store")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", time.Second, "How often to poll the Hermes DB for new messages")
	cmd.Flags().BoolVar(&fromBeginning, "from-beginning", false, "Replay every existing message in the Hermes DB instead of only streaming new ones (off by default)")
	return cmd
}
