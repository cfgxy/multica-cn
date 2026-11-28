package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOrcaBuildLaunchArgs(t *testing.T) {
	opts := ExecOptions{
		Cwd:   "/workspace/project",
		Model: "pi",
	}
	args := buildOrcaLaunchArgs("test prompt", opts)

	expectedPrefix := []string{"agent", "launch", "--agent", "pi", "--prompt", "test prompt"}
	for i, p := range expectedPrefix {
		if args[i] != p {
			t.Errorf("args[%d] = %q, want %q", i, args[i], p)
		}
	}

	foundWorktree := false
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--worktree" && args[i+1] == "path:/workspace/project" {
			foundWorktree = true
			break
		}
	}
	if !foundWorktree {
		t.Errorf("expected --worktree path:/workspace/project in %v", args)
	}

	if args[len(args)-1] != "--json" {
		t.Errorf("expected --json at end, got %q", args[len(args)-1])
	}
}

func TestOrcaBuildLaunchArgsCustomModel(t *testing.T) {
	opts := ExecOptions{
		Model: "gpt-5.6-terra",
	}
	args := buildOrcaLaunchArgs("prompt", opts)

	foundModel := false
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--model" && args[i+1] == "gpt-5.6-terra" {
			foundModel = true
			break
		}
	}
	if !foundModel {
		t.Errorf("expected --model gpt-5.6-terra in %v", args)
	}
}

func TestOrcaGNOMEScreenReaderDetection(t *testing.T) {
	// Directly check /usr/bin/orca
	if !IsGnomeScreenReader("/usr/bin/orca") {
		t.Errorf("expected IsGnomeScreenReader(\"/usr/bin/orca\") = true")
	}

	// Create a temp file containing GNOME script content
	tmpDir := t.TempDir()
	fakeGnome := filepath.Join(tmpDir, "orca")
	if err := os.WriteFile(fakeGnome, []byte("#!/usr/bin/python3\nimport orca.orca\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if !IsGnomeScreenReader(fakeGnome) {
		t.Errorf("expected IsGnomeScreenReader on gnome python script = true")
	}

	// Create a non-GNOME CLI file
	fakeStably := filepath.Join(tmpDir, "stably-orca")
	if err := os.WriteFile(fakeStably, []byte("#!/bin/sh\nexec node orcad.js \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if IsGnomeScreenReader(fakeStably) {
		t.Errorf("expected IsGnomeScreenReader on stably script = false")
	}
}

func TestOrcaResolveExecutablePathRejectsGNOME(t *testing.T) {
	cfg := Config{
		ExecutablePath: "/usr/bin/orca",
	}
	_, err := resolveOrcaExecutablePath(cfg)
	if err == nil {
		t.Fatalf("expected error resolving /usr/bin/orca, got nil")
	}
	if !strings.Contains(err.Error(), "GNOME screen reader") {
		t.Errorf("expected error to mention GNOME screen reader, got: %v", err)
	}
}

func TestOrcaLaunchOutputParsing(t *testing.T) {
	rawJSON := `{
		"id": "123",
		"ok": true,
		"result": {
			"outcome": {
				"kind": "structured",
				"sessionId": "4e08f884-df71-4767-98a0-34c89948713d",
				"handle": "session-handle"
			},
			"worktreeId": "folder:123"
		}
	}`
	var out orcaLaunchOutput
	if err := json.Unmarshal([]byte(rawJSON), &out); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !out.OK {
		t.Errorf("expected ok = true")
	}
	if out.Result.Outcome.SessionID != "4e08f884-df71-4767-98a0-34c89948713d" {
		t.Errorf("unexpected sessionId: %q", out.Result.Outcome.SessionID)
	}
}

func TestOrcaReadOutputParsing(t *testing.T) {
	rawJSON := `{
		"id": "read-123",
		"ok": true,
		"result": {
			"orcaSessionId": "4e08f884-df71-4767-98a0-34c89948713d",
			"state": "done",
			"entries": [
				{"kind": "message", "role": "user", "text": "hello"},
				{"kind": "message", "role": "assistant", "text": "Hello, world!"}
			]
		}
	}`
	var out orcaReadOutput
	if err := json.Unmarshal([]byte(rawJSON), &out); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !out.OK {
		t.Errorf("expected ok = true")
	}
	if out.Result.State != "done" {
		t.Errorf("expected state = done, got %q", out.Result.State)
	}
	var assistantText string
	for _, entry := range out.Result.Entries {
		if entry.Role == "assistant" {
			assistantText = entry.Text
		}
	}
	if assistantText != "Hello, world!" {
		t.Errorf("expected assistant text 'Hello, world!', got %q", assistantText)
	}
}

func TestOrcaResumeRejectionUndetectable(t *testing.T) {
	if !ResumeRejectionUndetectable("orca") {
		t.Errorf("expected ResumeRejectionUndetectable(\"orca\") = true")
	}
}
