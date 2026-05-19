package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func decompressCmd() *cobra.Command {
	var (
		input  string
		output string
	)

	cmd := &cobra.Command{
		Use:   "decompress",
		Short: "Decompress gzipped JSONL dataset files for inspection.",
		Long:  "Reads train.jsonl.gz, val.jsonl.gz, test.jsonl.gz from a versioned dataset directory and decompresses them to plain JSONL files in the output directory.",
		RunE: func(c *cobra.Command, _ []string) error {
			if input == "" {
				return fmt.Errorf("--input is required")
			}
			// TODO: decompress logic in subtask 08
			return fmt.Errorf("decompress not yet wired — subtask 08 pending")
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "Input directory or .gz file (required)")
	cmd.Flags().StringVar(&output, "output", "", "Output directory (default: input directory)")
	return cmd
}
