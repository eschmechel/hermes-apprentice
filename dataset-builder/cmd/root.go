package cmd

import "github.com/spf13/cobra"

func Root() *cobra.Command {
	root := &cobra.Command{
		Use:   "dataset-builder",
		Short: "Dataset Builder: fetch records, redact PII, quality-filter, and produce Hermes JSONL datasets.",
		Long: `Dataset Builder pulls approved (input, output) records from the observer HTTP /records
endpoint for a given pattern. It redacts PII via Microsoft Presidio, filters low-quality
pairs (re-asks/corrections), fuzzy-deduplicates, optionally augments small datasets via
a teacher model (OpenRouter), then splits 80/10/10 into train.jsonl.gz, val.jsonl.gz,
and test.jsonl.gz in the Hermes chat template format. Output lands in a versioned
directory under ~/.apprentice/datasets/<pattern-id>/v<n>/.`,
		SilenceUsage: true,
	}
	root.AddCommand(buildCmd())
	root.AddCommand(decompressCmd())
	root.AddCommand(versionCmd())
	return root
}
