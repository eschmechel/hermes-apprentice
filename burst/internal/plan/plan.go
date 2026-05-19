// Package plan turns burst's run options into the concrete shell commands the
// dispatcher will execute. Splitting this out lets tests assert "given these
// inputs we'd run exactly these commands" without spinning up a real pod.
package plan

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Options is the user-facing input to a burst run.
type Options struct {
	// PatternID is the dataset-builder pattern UUID. Used to namespace the
	// output dir on the pod and the merged model dir locally.
	PatternID string

	// DatasetDir is the local path containing train/val/test.jsonl.gz + manifest.json.
	DatasetDir string

	// OutputDir is the LOCAL path where the merged model will be rsynced back.
	OutputDir string

	// GPUType is a RunPod GPU type string. Defaults to "NVIDIA A100 80GB" if empty.
	GPUType string

	// Image is the RunPod container image to start. Defaults to a CUDA 12.1
	// PyTorch image that Unsloth's wheel matches.
	Image string

	// ContainerDiskGB is the ephemeral container disk size in GB. Default 50.
	ContainerDiskGB int

	// Profile is the apprentice-trainer profile name (without .yaml). The
	// dispatcher passes this as APPRENTICE_TRAINER_PROFILE on the pod.
	// Defaults to "profile_a100" because that's the only profile that targets
	// burst-class hardware.
	Profile string

	// PodName is a human-readable name passed to runpodctl. Defaults to
	// "apprentice-<PatternID-first-8>".
	PodName string

	// SSHIdentity is the local ssh key path used to talk to the pod
	// (RunPod injects the corresponding public key from your RunPod account).
	SSHIdentity string

	// WheelSpec is the pip install string for apprentice-trainer + unsloth.
	// Defaults to installing from the running git checkout via rsync; allows
	// overrides like a PyPI version pin.
	WheelSpec string
}

// Command is a single shell command in the run plan.
type Command struct {
	// Label is a short human description ("provision pod", "rsync dataset", …).
	Label string
	// Argv is the program + args, ready to pass to exec.Command.
	Argv []string
	// AllowFailure marks commands whose failure should be logged but not abort
	// the plan (e.g. final pod-terminate after a successful training run we
	// still want to print the model path).
	AllowFailure bool
}

func (c Command) String() string {
	parts := make([]string, len(c.Argv))
	for i, a := range c.Argv {
		if strings.ContainsAny(a, " \t\"'$") {
			parts[i] = fmt.Sprintf("%q", a)
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}

// Pod is the bag of pod-side state the plan resolves before building commands.
type Pod struct {
	ID       string // Filled in after runpodctl create.
	Name     string
	GPUType  string
	Image    string
	DiskGB   int
	SSHUser  string // Defaults to "root" on RunPod's official images.
	SSHHost  string // Pod public IP / DNS, filled by `runpodctl get pod`.
	SSHPort  int    // RunPod exposes SSH on a non-22 port; filled by query.
	WorkDir  string // Remote workspace, defaults to /workspace/apprentice.
}

// DefaultPod returns a Pod populated from Options + sensible RunPod defaults.
// The ID/SSH fields are still empty — those get filled in at runtime by the
// podctl package.
func DefaultPod(o Options) Pod {
	name := o.PodName
	if name == "" && o.PatternID != "" {
		shortID := o.PatternID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		name = "apprentice-" + shortID
	}
	gpu := o.GPUType
	if gpu == "" {
		gpu = "NVIDIA A100 80GB"
	}
	img := o.Image
	if img == "" {
		// CUDA 12.1 + torch 2.4 — matches Unsloth's cu121-torch240 wheel.
		img = "runpod/pytorch:2.4.0-py3.11-cuda12.1.0-devel-ubuntu22.04"
	}
	disk := o.ContainerDiskGB
	if disk <= 0 {
		disk = 50
	}
	return Pod{
		Name:    name,
		GPUType: gpu,
		Image:   img,
		DiskGB:  disk,
		SSHUser: "root",
		WorkDir: "/workspace/apprentice",
	}
}

// Provision returns the runpodctl create command for o + p.
func Provision(p Pod) Command {
	return Command{
		Label: "provision pod",
		Argv: []string{
			"runpodctl", "create", "pod",
			"--name", p.Name,
			"--imageName", p.Image,
			"--gpuType", p.GPUType,
			"--containerDiskInGb", fmt.Sprintf("%d", p.DiskGB),
			"--volumeInGb", "0",
			"--ports", "22/tcp",
		},
	}
}

// QueryPod returns the runpodctl command that, when parsed, yields the pod's
// SSH host/port. podctl.Provision calls this in a poll loop until ready.
func QueryPod(p Pod) Command {
	return Command{
		Label: "query pod state",
		Argv:  []string{"runpodctl", "get", "pod", p.ID, "-a"},
	}
}

// Terminate returns the runpodctl terminate command. Marked AllowFailure so a
// post-run cleanup error doesn't mask the actual training result.
func Terminate(p Pod) Command {
	return Command{
		Label:        "terminate pod",
		Argv:         []string{"runpodctl", "remove", "pod", p.ID},
		AllowFailure: true,
	}
}

// RsyncTo builds `rsync -avzP -e "ssh -p <port>" <localSrc>/ <user>@<host>:<remoteDst>/`.
func RsyncTo(p Pod, localSrc, remoteDst, identity string) Command {
	src := ensureTrailingSlash(localSrc)
	dst := fmt.Sprintf("%s@%s:%s", p.SSHUser, p.SSHHost, ensureTrailingSlash(remoteDst))
	return Command{
		Label: fmt.Sprintf("rsync %s -> pod:%s", filepath.Base(localSrc), remoteDst),
		Argv: []string{
			"rsync", "-avzP",
			"-e", sshFlag(p, identity),
			src, dst,
		},
	}
}

// RsyncFrom builds the inverse of RsyncTo.
func RsyncFrom(p Pod, remoteSrc, localDst, identity string) Command {
	src := fmt.Sprintf("%s@%s:%s", p.SSHUser, p.SSHHost, ensureTrailingSlash(remoteSrc))
	dst := ensureTrailingSlash(localDst)
	return Command{
		Label: fmt.Sprintf("rsync pod:%s -> %s", remoteSrc, filepath.Base(localDst)),
		Argv: []string{
			"rsync", "-avzP",
			"-e", sshFlag(p, identity),
			src, dst,
		},
	}
}

// SSHExec returns a Command that runs `bash -lc <script>` on the pod over ssh.
// The script is sent as a single argument; the caller's responsibility to escape.
func SSHExec(p Pod, identity, script string, label string) Command {
	return Command{
		Label: label,
		Argv: []string{
			"ssh", "-o", "StrictHostKeyChecking=accept-new",
			"-o", "ConnectTimeout=10",
			"-p", fmt.Sprintf("%d", p.SSHPort),
			"-i", identity,
			fmt.Sprintf("%s@%s", p.SSHUser, p.SSHHost),
			"bash", "-lc", script,
		},
	}
}

// Build returns the full ordered command list for a successful end-to-end run.
// It assumes a pod has already been provisioned (p.ID etc. are populated).
// Terminate is appended at the end with AllowFailure so plan-level retry logic
// can reason about cleanup separately from training success.
func Build(o Options, p Pod) []Command {
	training := strings.Join([]string{
		"set -euo pipefail",
		fmt.Sprintf("export APPRENTICE_TRAINER_PROFILE=%s/trainer/profiles/%s.yaml", p.WorkDir, o.Profile),
		fmt.Sprintf("cd %s/trainer", p.WorkDir),
		"uv sync --no-install-project",
		fmt.Sprintf("uv pip install %q", o.WheelSpec),
		fmt.Sprintf("apprentice-train --dataset-dir %s/dataset --output-dir %s/checkpoint", p.WorkDir, p.WorkDir),
		fmt.Sprintf("apprentice-merge --base-model unsloth/Qwen2.5-1.5B-Instruct --adapter-dir %s/checkpoint/lora-adapter --output-dir %s/merged", p.WorkDir, p.WorkDir),
	}, " && ")

	return []Command{
		// 1. Code rsync (host -> pod). We send the trainer/ subtree; the pod
		//    uses `uv pip install -e .` to build it locally there.
		RsyncTo(p, hostTrainerDir(), p.WorkDir+"/", o.SSHIdentity),
		// 2. Dataset rsync.
		RsyncTo(p, o.DatasetDir, p.WorkDir+"/dataset", o.SSHIdentity),
		// 3. Training + merge on the pod (one ssh exec; the failure of any
		//    step aborts via `set -e`).
		SSHExec(p, o.SSHIdentity, training, "train + merge on pod"),
		// 4. Merged model rsync (pod -> host).
		RsyncFrom(p, p.WorkDir+"/merged", o.OutputDir, o.SSHIdentity),
		// 5. Terminate. AllowFailure so a terminate hiccup doesn't void the
		//    successful run; the operator gets a warning instead.
		Terminate(p),
	}
}

// hostTrainerDir is the rsync source for the trainer code itself. Resolved
// relative to the binary's working directory by default; overridable via the
// BURST_TRAINER_DIR env var so contributors can point at their checkout.
func hostTrainerDir() string {
	// Indirection through an env var keeps tests deterministic AND lets the
	// CLI default to a value the test suite never resolves on its own.
	return envOrDefault("BURST_TRAINER_DIR", "./trainer")
}

func envOrDefault(name, def string) string {
	if v := getenv(name); v != "" {
		return v
	}
	return def
}

// getenv is a tiny seam so tests can shim os.Getenv if they ever need to.
// Today it's just a passthrough.
var getenv = func(string) string { return "" }

func ensureTrailingSlash(p string) string {
	if strings.HasSuffix(p, "/") {
		return p
	}
	return p + "/"
}

func sshFlag(p Pod, identity string) string {
	if identity == "" {
		return fmt.Sprintf("ssh -p %d -o StrictHostKeyChecking=accept-new", p.SSHPort)
	}
	return fmt.Sprintf("ssh -p %d -i %s -o StrictHostKeyChecking=accept-new", p.SSHPort, identity)
}
