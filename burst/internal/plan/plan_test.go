package plan

import (
	"strings"
	"testing"
)

func TestDefaultPod_AppliesDefaults(t *testing.T) {
	p := DefaultPod(Options{PatternID: "abcdef0123456789-deadbeef"})
	if p.Name != "apprentice-abcdef01" {
		t.Errorf("Name = %q, want apprentice-abcdef01", p.Name)
	}
	if p.GPUType == "" || !strings.Contains(p.GPUType, "A100") {
		t.Errorf("GPUType = %q, want A100 default", p.GPUType)
	}
	if p.DiskGB != 50 {
		t.Errorf("DiskGB = %d, want 50", p.DiskGB)
	}
	if p.SSHUser != "root" {
		t.Errorf("SSHUser = %q, want root", p.SSHUser)
	}
	if p.WorkDir != "/workspace/apprentice" {
		t.Errorf("WorkDir = %q, want /workspace/apprentice", p.WorkDir)
	}
}

func TestDefaultPod_RespectsOverrides(t *testing.T) {
	p := DefaultPod(Options{
		PatternID:       "p",
		PodName:         "my-pod",
		GPUType:         "NVIDIA H100 80GB",
		Image:           "custom/image:tag",
		ContainerDiskGB: 200,
	})
	if p.Name != "my-pod" || p.GPUType != "NVIDIA H100 80GB" ||
		p.Image != "custom/image:tag" || p.DiskGB != 200 {
		t.Fatalf("override path wrong: %+v", p)
	}
}

func TestProvision_BuildsRunpodctlArgs(t *testing.T) {
	p := DefaultPod(Options{PatternID: "abc12345-uuid"})
	cmd := Provision(p)
	got := cmd.String()
	for _, want := range []string{
		"runpodctl", "create", "pod",
		"--name", p.Name,
		"--imageName", p.Image,
		"--gpuType", `"NVIDIA A100 80GB"`,
		"--containerDiskInGb", "50",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Provision missing %q in %q", want, got)
		}
	}
}

func TestRsyncTo_IncludesSSHPortFlag(t *testing.T) {
	p := DefaultPod(Options{PatternID: "p"})
	p.SSHHost = "1.2.3.4"
	p.SSHPort = 22000
	cmd := RsyncTo(p, "/local/dataset", "/workspace/dataset", "/home/me/.ssh/key")
	got := cmd.String()
	if !strings.Contains(got, "ssh -p 22000 -i /home/me/.ssh/key") {
		t.Errorf("rsync -e missing ssh port/identity: %s", got)
	}
	if !strings.HasSuffix(strings.Fields(got)[len(strings.Fields(got))-1], "/") {
		// trailing slash on destination makes rsync's "contents of" semantics work
		t.Errorf("rsync dst missing trailing slash: %s", got)
	}
}

func TestRsyncFrom_DirectionReversed(t *testing.T) {
	p := DefaultPod(Options{PatternID: "p"})
	p.SSHHost = "host"
	p.SSHPort = 2222
	cmd := RsyncFrom(p, "/workspace/merged", "/local/output", "")
	got := cmd.String()
	if !strings.Contains(got, "root@host:/workspace/merged/") {
		t.Errorf("RsyncFrom source not pod-prefixed: %s", got)
	}
	if !strings.Contains(got, "/local/output/") {
		t.Errorf("RsyncFrom local dst missing: %s", got)
	}
}

func TestSSHExec_QuotesScript(t *testing.T) {
	p := DefaultPod(Options{PatternID: "p"})
	p.SSHHost = "h"
	p.SSHPort = 2222
	cmd := SSHExec(p, "/k", "set -e && true", "test step")
	got := cmd.String()
	if !strings.Contains(got, "ssh") || !strings.Contains(got, "-p 2222") ||
		!strings.Contains(got, "-i /k") {
		t.Errorf("ssh exec missing flags: %s", got)
	}
	if !strings.Contains(got, `"set -e && true"`) {
		t.Errorf("script not quoted: %s", got)
	}
}

func TestBuild_OrdersStepsAndEndsWithTerminate(t *testing.T) {
	o := Options{
		PatternID:   "abc",
		DatasetDir:  "/data",
		OutputDir:   "/out",
		Profile:     "profile_a100",
		SSHIdentity: "/key",
		WheelSpec:   "unsloth[cu121-torch240] @ git+https://github.com/unslothai/unsloth.git",
	}
	p := DefaultPod(o)
	p.ID = "pod-id"
	p.SSHHost = "host"
	p.SSHPort = 22000

	cmds := Build(o, p)
	if len(cmds) != 5 {
		t.Fatalf("Build returned %d commands, want 5", len(cmds))
	}
	labels := []string{}
	for _, c := range cmds {
		labels = append(labels, c.Label)
	}
	wantOrder := []string{
		"rsync trainer -> pod:/workspace/apprentice/",
		"rsync dataset", // partial — we tolerate suffix differences
		"train + merge on pod",
		"rsync pod:/workspace/apprentice/merged",
		"terminate pod",
	}
	for i, want := range wantOrder {
		if !strings.HasPrefix(labels[i], strings.Split(want, " -> ")[0][:5]) &&
			!strings.Contains(labels[i], strings.TrimSpace(strings.Split(want, " ")[0])) {
			t.Errorf("step %d label = %q, want prefix containing %q", i, labels[i], want)
		}
	}
	if !cmds[len(cmds)-1].AllowFailure {
		t.Errorf("terminate step must be AllowFailure so a cleanup hiccup doesn't void training success")
	}
}

func TestBuild_ScriptIncludesProfileEnvAndCommands(t *testing.T) {
	o := Options{
		Profile:     "profile_a100",
		SSHIdentity: "/k",
		WheelSpec:   "unsloth",
		PatternID:   "p",
	}
	p := DefaultPod(o)
	p.SSHHost = "h"
	p.SSHPort = 1
	cmds := Build(o, p)
	exec := cmds[2] // train + merge
	script := exec.Argv[len(exec.Argv)-1]
	for _, want := range []string{
		"APPRENTICE_TRAINER_PROFILE=", "profile_a100.yaml",
		"apprentice-train --dataset-dir",
		"apprentice-merge --base-model",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("training script missing %q: %s", want, script)
		}
	}
}
