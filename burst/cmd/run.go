package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/hermes-apprentice/burst/internal/plan"
	"github.com/hermes-apprentice/burst/internal/runner"
	"github.com/spf13/cobra"
)

func runCmd() *cobra.Command {
	o := plan.Options{}
	var dryRun bool
	var podID string
	var podHost string
	var podPort int
	var skipProvision bool

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Provision, train, fetch the merged model, terminate.",
		Long: `run wires the full lifecycle. With --dry-run it prints the steps
without executing anything; without --dry-run it requires runpodctl, ssh, and
rsync on PATH. If you've already provisioned a pod, pass --pod-id / --pod-host
/ --pod-port AND --skip-provision so the dispatcher reuses it instead of
spinning up another.`,
		RunE: func(c *cobra.Command, _ []string) error {
			logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
			ctx, stop := signal.NotifyContext(c.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			if !dryRun {
				if _, err := exec.LookPath("runpodctl"); err != nil && !skipProvision {
					return fmt.Errorf("runpodctl not on PATH and --skip-provision not set: " +
						"install runpodctl (https://github.com/runpod/runpodctl) or re-run with --dry-run")
				}
				if _, err := exec.LookPath("rsync"); err != nil {
					return fmt.Errorf("rsync not on PATH")
				}
				if _, err := exec.LookPath("ssh"); err != nil {
					return fmt.Errorf("ssh not on PATH")
				}
			}

			if o.PatternID == "" {
				return fmt.Errorf("--pattern-id is required")
			}
			if o.DatasetDir == "" {
				return fmt.Errorf("--dataset-dir is required")
			}
			if o.OutputDir == "" {
				return fmt.Errorf("--output-dir is required")
			}

			p := plan.DefaultPod(o)
			r := &runner.Runner{DryRun: dryRun, Logger: logger, Stdout: c.OutOrStdout()}

			// Provision step (skippable). Pod ID parsing from runpodctl create's
			// output is intentionally NOT implemented here -- runpodctl's text
			// format has drifted in the past and we don't want to silently
			// race against that. Operator pattern: provision in --dry-run mode
			// to see the runpodctl line, run it yourself, capture the pod ID,
			// then re-run with --skip-provision + --pod-id/host/port.
			if !skipProvision {
				if err := r.Run(ctx, []plan.Command{plan.Provision(p)}); err != nil {
					return err
				}
				logger.Info("provision returned -- re-run with --skip-provision " +
					"+ --pod-id/host/port to drive the rest of the plan")
				return nil
			}

			if podID == "" || podHost == "" || podPort == 0 {
				return fmt.Errorf("--skip-provision requires --pod-id, --pod-host, and --pod-port")
			}
			p.ID = podID
			p.SSHHost = podHost
			p.SSHPort = podPort

			cmds := plan.Build(o, p)
			if err := r.Run(ctx, cmds); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(),
				"\nMerged model fetched to: %s\n", o.OutputDir)
			return nil
		},
	}
	addOptionsFlags(cmd, &o)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print steps without executing.")
	cmd.Flags().BoolVar(&skipProvision, "skip-provision", false,
		"Reuse a pre-existing pod (requires --pod-id + --pod-host + --pod-port).")
	cmd.Flags().StringVar(&podID, "pod-id", "", "RunPod pod ID (with --skip-provision).")
	cmd.Flags().StringVar(&podHost, "pod-host", "", "Pod SSH host (with --skip-provision).")
	cmd.Flags().IntVar(&podPort, "pod-port", 0, "Pod SSH port (with --skip-provision).")
	return cmd
}
