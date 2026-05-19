package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hermes-apprentice/observer/internal/httpapi"
	"github.com/spf13/cobra"
)

func serveCmd() *cobra.Command {
	var (
		listenAddr    string
		hermesDBPath  string
		observerDB    string
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
				"version", Version,
			)

			ctx, stop := signal.NotifyContext(c.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			srv := httpapi.New(httpapi.Config{
				Addr:   listenAddr,
				Logger: logger,
			})

			errCh := make(chan error, 1)
			go func() { errCh <- srv.ListenAndServe(ctx) }()

			select {
			case <-ctx.Done():
				logger.Info("shutdown signal received")
				return srv.Shutdown(context.Background())
			case err := <-errCh:
				if err != nil {
					return fmt.Errorf("http server: %w", err)
				}
				return nil
			}
		},
	}

	cmd.Flags().StringVar(&listenAddr, "listen", ":8080", "HTTP listen address")
	cmd.Flags().StringVar(&hermesDBPath, "hermes-db", "/root/.hermes/state.db", "Path to the Hermes session DB to tail")
	cmd.Flags().StringVar(&observerDB, "observer-db", "/root/.apprentice/observer.db", "Path to the observer's own SQLite store")
	return cmd
}
