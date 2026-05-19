package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestScaffold_Help(t *testing.T) {
	out, err := exec.Command("go", "run", ".", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("--help: %v\n%s", err, out)
	}
	s := string(out)
	for _, want := range []string{"dataset-builder", "build", "decompress", "version"} {
		if !strings.Contains(s, want) {
			t.Errorf("--help missing %q", want)
		}
	}
}

func TestScaffold_Version(t *testing.T) {
	out, err := exec.Command("go", "run", ".", "version").CombinedOutput()
	if err != nil {
		t.Fatalf("version: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "dev") {
		t.Errorf("version = %q, want 'dev'", strings.TrimSpace(string(out)))
	}
}

func TestScaffold_BuildNeedsPatternID(t *testing.T) {
	out, err := exec.Command("go", "run", ".", "build").CombinedOutput()
	if err == nil {
		t.Fatal("expected error when --pattern-id is missing")
	}
	s := string(out)
	if !strings.Contains(s, "--pattern-id is required") {
		t.Errorf("error missing pattern-id hint: %s", s)
	}
}

func TestScaffold_DecompressHelp(t *testing.T) {
	out, err := exec.Command("go", "run", ".", "decompress", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("decompress --help: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--input") {
		t.Errorf("decompress --help missing --input flag")
	}
}
