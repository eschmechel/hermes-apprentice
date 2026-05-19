package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var Version = "dev"

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print dataset-builder version and exit.",
		Run: func(c *cobra.Command, _ []string) {
			fmt.Fprintln(c.OutOrStdout(), Version)
		},
	}
}
