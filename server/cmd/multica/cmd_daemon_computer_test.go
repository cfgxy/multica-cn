package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestComputerHealthPortFromOwnConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MULTICA_TASK_CONFIG_ROOT", "")
	if err := os.Mkdir(filepath.Join(home, ".multica"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".multica/config.json"), []byte(`{"health_port":23001}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := healthPortForProfile(""); got != 23001 {
		t.Fatalf("port=%d", got)
	}
	if err := os.WriteFile(filepath.Join(home, ".multica/config.json"), []byte(`{"health_port":70000}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := healthPortForProfile(""); got != 19514 {
		t.Fatalf("invalid port not rejected: %d", got)
	}
}
