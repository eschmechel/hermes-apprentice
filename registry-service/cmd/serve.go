package cmd

import (
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/eschmechel/hermes-apprentice/registry-service/internal/httpapi"
	"github.com/spf13/cobra"
)

func serveCmd() *cobra.Command {
	var (
		listenAddr  string
		registryDir string
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the registry HTTP service.",
		RunE: func(c *cobra.Command, _ []string) error {
			logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
			logger.Info("registry-service starting",
				"listen", listenAddr,
				"registry_dir", registryDir,
				"version", Version,
			)

			ctx, stop := signal.NotifyContext(c.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			srv := httpapi.New(httpapi.Config{
				Addr:        listenAddr,
				Logger:      logger,
				RegistryDir: registryDir,
			})

			return srv.ListenAndServe(ctx)
		},
	}

	home, _ := os.UserHomeDir()
	defaultRegistry := filepath.Join(home, ".apprentice", "registry")

	cmd.Flags().StringVar(&listenAddr, "listen", ":8082", "HTTP listen address")
	cmd.Flags().StringVar(&registryDir, "registry-dir", defaultRegistry, "Registry root directory (~/.apprentice/registry)")
	return cmd
}
