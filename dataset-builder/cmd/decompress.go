package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hermes-apprentice/dataset-builder/internal/versioned"
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
			if output == "" {
				output = input
			}

			info, err := os.Stat(input)
			if err != nil {
				return fmt.Errorf("stat input: %w", err)
			}

			if info.IsDir() {
				entries, err := os.ReadDir(input)
				if err != nil {
					return fmt.Errorf("read dir: %w", err)
				}
				var decompressed int
				for _, e := range entries {
					if !e.IsDir() && strings.HasSuffix(e.Name(), ".gz") {
						src := filepath.Join(input, e.Name())
						dst := filepath.Join(output, strings.TrimSuffix(e.Name(), ".gz"))
						if err := versioned.Decompress(src, dst); err != nil {
							return fmt.Errorf("decompress %s: %w", e.Name(), err)
						}
						decompressed++
					}
				}
				if decompressed == 0 {
					return fmt.Errorf("no .gz files found in %s", input)
				}
			} else {
				if !strings.HasSuffix(input, ".gz") {
					return fmt.Errorf("input must be a .gz file or directory: %s", input)
				}
				if err := versioned.Decompress(input, ""); err != nil {
					return fmt.Errorf("decompress: %w", err)
				}
			}

			fmt.Println("decompression complete")
			return nil
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "Input directory or .gz file (required)")
	cmd.Flags().StringVar(&output, "output", "", "Output directory (default: input directory)")
	return cmd
}
