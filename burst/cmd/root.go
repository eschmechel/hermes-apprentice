package cmd

import "github.com/spf13/cobra"

func Root() *cobra.Command {
	root := &cobra.Command{
		Use:   "burst",
		Short: "Burst dispatcher: provision a RunPod GPU pod, train, fetch the merged model, terminate.",
		Long: `burst is the host-side orchestrator for cloud training runs of the apprentice
trainer. Given a local dataset directory and an output directory, it:
  1. Provisions a RunPod GPU pod via runpodctl with the chosen image + GPU type.
  2. Installs (or pip-installs) the apprentice-trainer wheel onto the pod.
  3. rsyncs the dataset to the pod.
  4. Triggers training (apprentice-train) + merge (apprentice-merge) via ssh.
  5. rsyncs the merged model back.
  6. Terminates the pod.

Each step has a corresponding subcommand for granular operation, plus a top-level
run subcommand that wires them in order. --dry-run prints the exact runpodctl/
rsync/ssh commands without executing them, which is how the CI tests cover the
dispatcher without a live RunPod account.`,
		SilenceUsage: true,
	}
	root.AddCommand(runCmd())
	root.AddCommand(planCmd())
	root.AddCommand(versionCmd())
	return root
}
