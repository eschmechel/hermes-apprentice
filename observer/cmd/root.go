package cmd

import (
	"github.com/spf13/cobra"
)

func Root() *cobra.Command {
	root := &cobra.Command{
		Use:   "observer",
		Short: "Observer: tails the Hermes session DB and exposes extracted records over HTTP.",
		Long: `Observer watches /root/.hermes/state.db for new messages produced by Hermes,
normalizes (user, assistant) pairs into a local SQLite store, and serves them at
GET /records for downstream apprentice services (detector, dataset-builder).`,
		SilenceUsage: true,
	}
	root.AddCommand(serveCmd())
	root.AddCommand(versionCmd())
	return root
}
