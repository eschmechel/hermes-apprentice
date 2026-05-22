package runner

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/eschmechel/hermes-apprentice/burst/internal/plan"
)

func TestRun_DryRunPrintsAllAndExecutesNone(t *testing.T) {
	var buf bytes.Buffer
	r := &Runner{DryRun: true, Stdout: &buf}
	cmds := []plan.Command{
		{Label: "step a", Argv: []string{"runpodctl", "create", "pod"}},
		{Label: "step b", Argv: []string{"rsync", "-avz", "/foo", "host:/bar"}},
		{Label: "step c", Argv: []string{"ssh", "host", "echo", "hi"}, AllowFailure: true},
	}
	if err := r.Run(context.Background(), cmds); err != nil {
		t.Fatalf("dry run should never fail: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"step a", "step b", "step c",
		"runpodctl create pod", "rsync -avz /foo host:/bar"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q: %s", want, out)
		}
	}
}

func TestRun_AllowFailureContinuesPastFailingStep(t *testing.T) {
	cmds := []plan.Command{
		// First step: succeeds (`true` is a real binary that exits 0).
		{Label: "ok", Argv: []string{"true"}},
		// Second step: fails (`false` exits 1) but is AllowFailure.
		{Label: "tolerated fail", Argv: []string{"false"}, AllowFailure: true},
		// Third step: succeeds again. Should run because the failure was tolerated.
		{Label: "ok again", Argv: []string{"true"}},
	}
	r := &Runner{DryRun: false}
	if err := r.Run(context.Background(), cmds); err != nil {
		t.Fatalf("Run returned %v, want nil (AllowFailure should swallow)", err)
	}
}

func TestRun_NonAllowedFailureAborts(t *testing.T) {
	cmds := []plan.Command{
		{Label: "ok", Argv: []string{"true"}},
		{Label: "fatal fail", Argv: []string{"false"}},
		{Label: "never reached", Argv: []string{"true"}},
	}
	r := &Runner{DryRun: false}
	err := r.Run(context.Background(), cmds)
	if err == nil {
		t.Fatalf("Run returned nil, want error from `false`")
	}
	if !strings.Contains(err.Error(), "fatal fail") {
		t.Errorf("error should name the failing step's label: %v", err)
	}
}
