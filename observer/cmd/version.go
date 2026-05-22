package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is the observer build version. Set at link time via:
//
//	go build -ldflags "-X github.com/eschmechel/hermes-apprentice/observer/cmd.Version=<sha>"
var Version = "dev"

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print observer version and exit.",
		Run: func(c *cobra.Command, _ []string) {
			fmt.Fprintln(c.OutOrStdout(), Version)
		},
	}
}
