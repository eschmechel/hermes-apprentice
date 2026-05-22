// Package runner executes plan.Commands, with --dry-run support.
//
// In dry-run mode the runner only prints what it WOULD execute. This is the
// CI-testable surface; live RunPod execution requires runpodctl/ssh/rsync on
// PATH and a real RunPod account, which the contest acceptance bullet
// ("Provisions RunPod pod via runpodctl") gates behind manual verification.
package runner

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"time"

	"github.com/eschmechel/hermes-apprentice/burst/internal/plan"
)

type Runner struct {
	DryRun bool
	Logger *slog.Logger
	Stdout io.Writer
}

// Run executes cmds in order. Returns the first command that errored and did
// NOT have AllowFailure set; AllowFailure commands log a warning and continue.
// In dry-run mode every command is printed and skipped.
func (r *Runner) Run(ctx context.Context, cmds []plan.Command) error {
	logger := r.Logger
	if logger == nil {
		logger = slog.Default()
	}
	for i, c := range cmds {
		if r.DryRun {
			fmt.Fprintf(r.Stdout, "[%d/%d] %s\n     %s\n", i+1, len(cmds), c.Label, c.String())
			continue
		}
		logger.Info("running step",
			"step", i+1, "of", len(cmds), "label", c.Label, "argv", c.Argv)
		t0 := time.Now()
		err := exec.CommandContext(ctx, c.Argv[0], c.Argv[1:]...).Run()
		dur := time.Since(t0).Round(time.Millisecond)
		if err != nil {
			if c.AllowFailure {
				logger.Warn("step failed (allowed)", "label", c.Label, "err", err, "duration", dur)
				continue
			}
			logger.Error("step failed", "label", c.Label, "err", err, "duration", dur)
			return fmt.Errorf("step %q: %w", c.Label, err)
		}
		logger.Info("step ok", "label", c.Label, "duration", dur)
	}
	return nil
}
