package cmd

import "github.com/spf13/cobra"

var Version = "dev"

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print registry-service version and exit.",
		Run: func(c *cobra.Command, _ []string) {
			c.OutOrStdout().Write([]byte(Version + "\n"))
		},
	}
}
