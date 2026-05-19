package cmd

import "github.com/spf13/cobra"

func Root() *cobra.Command {
	root := &cobra.Command{
		Use:   "detector",
		Short: "Detector: clusters observer records and emits candidate task patterns.",
		Long: `Detector pulls (input, output) records from the observer's HTTP /records endpoint,
embeds the input text, clusters recent embeddings (7-day window), and emits a candidate
pattern manifest when a cluster crosses size + cohesion thresholds. Operator approves
patterns via POST /patterns/:id/approve to gate downstream dataset-builder/trainer work.`,
		SilenceUsage: true,
	}
	root.AddCommand(serveCmd())
	root.AddCommand(versionCmd())
	return root
}
