package cmd

import (
	"fmt"

	"github.com/eschmechel/hermes-apprentice/burst/internal/plan"
	"github.com/spf13/cobra"
)

func planCmd() *cobra.Command {
	o := plan.Options{}
	var podHost string
	var podPort int
	var podID string

	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Print the exact shell commands burst would execute, without running anything.",
		Long: `plan is the CI-testable surface of the dispatcher: given the same flags
you'd pass to "burst run", it prints the runpodctl + rsync + ssh commands in
order without executing any of them. Use it to inspect what a real run would do,
or to capture the command list into a shell script you can audit + run manually.

For pod-specific flags (--pod-id, --pod-host, --pod-port), supply concrete
values if you have a pre-provisioned pod; otherwise the plan substitutes
placeholders so the printed plan stays useful for review.`,
		RunE: func(c *cobra.Command, _ []string) error {
			p := plan.DefaultPod(o)
			if podID != "" {
				p.ID = podID
			} else {
				p.ID = "<POD-ID>"
			}
			if podHost != "" {
				p.SSHHost = podHost
			} else {
				p.SSHHost = "<POD-HOST>"
			}
			if podPort > 0 {
				p.SSHPort = podPort
			} else {
				p.SSHPort = 22000
			}

			fmt.Fprintln(c.OutOrStdout(), "# Step 0 — provision (skipped if --pod-id supplied):")
			fmt.Fprintln(c.OutOrStdout(), "    "+plan.Provision(p).String())
			fmt.Fprintln(c.OutOrStdout(), "")
			fmt.Fprintln(c.OutOrStdout(), "# Steps 1..5 — main run plan:")
			for i, cmd := range plan.Build(o, p) {
				fmt.Fprintf(c.OutOrStdout(), "[%d/%d] %s\n     %s\n\n",
					i+1, 5, cmd.Label, cmd.String())
			}
			return nil
		},
	}
	addOptionsFlags(cmd, &o)
	cmd.Flags().StringVar(&podID, "pod-id", "", "Pre-existing pod ID (if you already provisioned one).")
	cmd.Flags().StringVar(&podHost, "pod-host", "", "Pre-existing pod SSH host.")
	cmd.Flags().IntVar(&podPort, "pod-port", 0, "Pre-existing pod SSH port.")
	return cmd
}

// addOptionsFlags wires plan.Options fields onto a cobra command. Shared with
// run.go so the two subcommands accept identical flags.
func addOptionsFlags(cmd *cobra.Command, o *plan.Options) {
	cmd.Flags().StringVar(&o.PatternID, "pattern-id", "", "Dataset-builder pattern UUID.")
	cmd.Flags().StringVar(&o.DatasetDir, "dataset-dir", "", "Local versioned dataset directory.")
	cmd.Flags().StringVar(&o.OutputDir, "output-dir", "", "Local destination for the merged model.")
	cmd.Flags().StringVar(&o.GPUType, "gpu-type", "", "RunPod GPU type (default: NVIDIA A100 80GB).")
	cmd.Flags().StringVar(&o.Image, "image", "", "Container image (default: runpod/pytorch CUDA 12.1 + torch 2.4).")
	cmd.Flags().IntVar(&o.ContainerDiskGB, "container-disk-gb", 0, "Pod ephemeral disk (default 50).")
	cmd.Flags().StringVar(&o.Profile, "profile", "profile_a100", "apprentice-trainer profile (without .yaml).")
	cmd.Flags().StringVar(&o.PodName, "pod-name", "", "Pod name (default: apprentice-<first-8-of-pattern-id>).")
	cmd.Flags().StringVar(&o.SSHIdentity, "ssh-identity", "~/.ssh/id_ed25519", "Local ssh key to use against the pod.")
	cmd.Flags().StringVar(&o.WheelSpec, "wheel", "unsloth[cu121-torch240] @ git+https://github.com/unslothai/unsloth.git",
		"Pip install spec for unsloth on the pod (encodes torch + cuda version).")
}
