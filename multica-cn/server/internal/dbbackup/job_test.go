package dbbackup

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func waitForBackupFile(t *testing.T, dir string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(dir)
		if err == nil {
			for _, e := range entries {
				if filepath.Ext(e.Name()) == ".dump" {
					return e.Name()
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no backup file appeared in %s within %s", dir, timeout)
	return ""
}

func TestRunJobBacksUpOnScheduleAndExitsOnCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake pg_dump shell script requires a POSIX shell")
	}
	dir := t.TempDir()
	fake := filepath.Join(t.TempDir(), "fake-pg-dump.sh")
	script := `#!/bin/sh
out=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-f" ]; then out="$arg"; fi
  prev="$arg"
done
printf 'payload' > "$out"
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake pg_dump: %v", err)
	}

	cfg := Config{
		Enabled:       true,
		Dir:           dir,
		PGDumpPath:    fake,
		DatabaseURL:   "postgres://db",
		Timeout:       30 * time.Second,
		Interval:      50 * time.Millisecond,
		RetentionDays: 7,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunJob(ctx, cfg)
		close(done)
	}()

	first := waitForBackupFile(t, dir, 10*time.Second)
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RunJob did not return after context cancellation")
	}
	if first == "" {
		t.Fatal("scheduled job produced no backup")
	}
}

func TestRunJobDisabledIsNoop(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Enabled: false, Dir: dir, Interval: 10 * time.Millisecond}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunJob(ctx, cfg)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunJob with Enabled=false did not return immediately")
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("disabled job wrote to the backup dir (entries=%d err=%v)", len(entries), err)
	}
}
