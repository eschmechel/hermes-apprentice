package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/eschmechel/hermes-apprentice/observer/internal/dedup"
	"github.com/eschmechel/hermes-apprentice/observer/internal/httpapi"
	"github.com/eschmechel/hermes-apprentice/observer/internal/normalizer"
	"github.com/eschmechel/hermes-apprentice/observer/internal/pairing"
	"github.com/eschmechel/hermes-apprentice/observer/internal/poller"
	"github.com/eschmechel/hermes-apprentice/observer/internal/state"
	"github.com/eschmechel/hermes-apprentice/observer/internal/store"
	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"
)

func serveCmd() *cobra.Command {
	var (
		listenAddr    string
		hermesDBPath  string
		observerDB    string
		pollInterval  time.Duration
		fromBeginning bool
		dedupWindow   time.Duration
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
				"dedup_window", dedupWindow,
				"version", Version,
			)

			ctx, stop := signal.NotifyContext(c.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			// Persistent high-water mark lives next to observerDB so a restart
			// resumes from the last fully-processed messages.id.
			hwm, err := state.Load(filepath.Dir(observerDB))
			if err != nil {
				return fmt.Errorf("load hwm: %w", err)
			}
			logger.Info("hwm loaded", "last_processed_id", hwm.Get(), "path", filepath.Join(filepath.Dir(observerDB), "observer.state.json"))

			window := dedup.New(dedupWindow, nil)

			// Observer's own SQLite store for (input, output) records.
			obsStore, err := store.Open(observerDB)
			if err != nil {
				return fmt.Errorf("open observer store: %w", err)
			}
			defer obsStore.Close()

			// Read-only connection to Hermes DB for sessions enrichment (model,
			// system_prompt). Separate from the poller's connection so query
			// contention is bounded by SQLite WAL, not driver mutex.
			hermesEnrichDSN := fmt.Sprintf("file:%s?mode=ro&_journal_mode=WAL", hermesDBPath)
			hermesEnrich, err := sql.Open("sqlite", hermesEnrichDSN)
			if err != nil {
				return fmt.Errorf("open hermes for enrichment: %w", err)
			}
			defer hermesEnrich.Close()

			pairer := pairing.New(obsStore, hermesEnrich, logger.With("component", "pairer"))

			srv := httpapi.New(httpapi.Config{
				Addr:   listenAddr,
				Logger: logger,
				Store:  obsStore,
			})

			handler := func(ctx context.Context, m poller.Message) error {
				n := normalizer.Normalize(m)
				if window.Seen(n.SessionID, n.ContentHash) {
					logger.Debug("dedup drop",
						"id", n.ID,
						"session_id", n.SessionID,
						"role", n.Role,
						"content_hash", n.ContentHash[:12],
					)
					return hwm.Set(m.ID)
				}
				logger.Info("hermes message",
					"id", n.ID,
					"session_id", n.SessionID,
					"role", n.Role,
					"content_len", len(n.Content),
					"tool_calls_len", len(n.ToolCalls),
					"content_hash", n.ContentHash[:12],
					"ts", n.Timestamp,
				)
				if err := pairer.Observe(ctx, n); err != nil {
					return fmt.Errorf("pairer: %w", err)
				}
				return hwm.Set(m.ID)
			}

			poll := poller.New(poller.Config{
				HermesDBPath:       hermesDBPath,
				Interval:           pollInterval,
				Logger:             logger.With("component", "poller"),
				Handler:            handler,
				StartFromBeginning: fromBeginning,
				StartFromID:        hwm.Get(),
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
	cmd.Flags().DurationVar(&dedupWindow, "dedup-window", 60*time.Second, "Rolling window in which (session_id, content_hash) duplicates are dropped")
	return cmd
}
