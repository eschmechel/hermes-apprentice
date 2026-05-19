package cmd

import "github.com/spf13/cobra"

func Root() *cobra.Command {
	root := &cobra.Command{
		Use:   "registry-service",
		Short: "Registry service: serves latest promoted specialist models via HTTP.",
		Long: `Registry service exposes a read-only HTTP API over the Apprentice model registry
at ~/.apprentice/registry/. Endpoints return the latest promoted version of each
specialist skill so downstream consumers (e.g. the Hermes skill runner) can
discover the best available model for a given pattern.`,
		SilenceUsage: true,
	}
	root.AddCommand(serveCmd())
	root.AddCommand(versionCmd())
	return root
}
