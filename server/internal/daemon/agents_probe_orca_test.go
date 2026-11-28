package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProbeAgentCLIsDiscoversOrcaDefaultInstallPath(t *testing.T) {
	home := t.TempDir()
	installDir := filepath.Join(home, ".orca", "cli", "bin")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(installDir, "orca")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	origResolve := resolveAgentsViaLoginShell
	t.Cleanup(func() { resolveAgentsViaLoginShell = origResolve })
	resolveAgentsViaLoginShell = func([]string) map[string]string { return map[string]string{} }
	resetShellResolveCacheForTest(t)

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("MULTICA_ORCA_PATH", "")
	t.Setenv("MULTICA_ORCA_MODEL", "gpt-5.6-terra")

	entry, ok := probeAgentCLIs()["orca"]
	if !ok {
		t.Fatal("Orca was not discovered from its default user install path")
	}
	if filepath.Clean(entry.Path) != filepath.Clean(executable) {
		t.Fatalf("Orca path = %q, want %q", entry.Path, executable)
	}
	if entry.Command != "orca" || entry.Model != "gpt-5.6-terra" {
		t.Fatalf("unexpected Orca entry: %+v", entry)
	}
}

func TestProbeAgentCLIsRejectsGnomeScreenReader(t *testing.T) {
	fakeBinDir := t.TempDir()
	fakeGnome := filepath.Join(fakeBinDir, "orca")
	if err := os.WriteFile(fakeGnome, []byte("#!/usr/bin/python3\nimport orca.orca\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	origResolve := resolveAgentsViaLoginShell
	t.Cleanup(func() { resolveAgentsViaLoginShell = origResolve })
	resolveAgentsViaLoginShell = func([]string) map[string]string { return map[string]string{} }
	resetShellResolveCacheForTest(t)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", fakeBinDir)
	t.Setenv("MULTICA_ORCA_PATH", "")

	_, ok := probeAgentCLIs()["orca"]
	if ok {
		t.Fatal("probeAgentCLIs should reject GNOME screen reader binary")
	}
}
