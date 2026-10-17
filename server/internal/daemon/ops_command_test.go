// RUYI-645: daemon-side unit tests for the ops command executor and the
// service-inventory collectors (matrix groups 3/6). The executor is a pure
// argv runner so truncation, timeout and exit-code capture are asserted
// against controllable shell payloads instead of the whitelisted binaries.
package daemon

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/opskind"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRunOpsArgv_CapturesSuccessOutputAndExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell payload is unix-only")
	}

	receipt := runOpsArgv(context.Background(), []string{"/bin/sh", "-c", "printf hello; "}, 5*time.Second, 1)
	if receipt.Status != "succeeded" {
		t.Fatalf("status = %s (%s), want succeeded", receipt.Status, receipt.Error)
	}
	if receipt.Stdout != "hello" {
		t.Fatalf("stdout = %q, want hello", receipt.Stdout)
	}
	if receipt.ExitCode == nil || *receipt.ExitCode != 0 {
		t.Fatalf("exit_code = %v, want 0", receipt.ExitCode)
	}
	if receipt.Truncated {
		t.Fatal("truncated = true for a 5-byte output")
	}
}

func TestRunOpsArgv_NonZeroExitIsFailedWithStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell payload is unix-only")
	}

	receipt := runOpsArgv(context.Background(), []string{"/bin/sh", "-c", "printf boom >&2; exit 3"}, 5*time.Second, 1)
	if receipt.Status != "failed" {
		t.Fatalf("status = %s, want failed", receipt.Status)
	}
	if receipt.ExitCode == nil || *receipt.ExitCode != 3 {
		t.Fatalf("exit_code = %v, want 3", receipt.ExitCode)
	}
	if receipt.Stderr != "boom" {
		t.Fatalf("stderr = %q, want boom", receipt.Stderr)
	}
}

func TestRunOpsArgv_TruncatesOversizeOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell payload is unix-only")
	}

	// 64KB cap + 4KB overage; the stream must be capped and flagged.
	receipt := runOpsArgv(context.Background(), []string{"/bin/sh", "-c",
		"dd if=/dev/zero bs=1024 count=68 2>/dev/null | tr '\\0' 'x'"}, 10*time.Second, 1)
	if !receipt.Truncated {
		t.Fatal("truncated = false for a >64KB stream")
	}
	if len(receipt.Stdout) > opsStreamCapBytes {
		t.Fatalf("stdout = %d bytes, want <= %d cap", len(receipt.Stdout), opsStreamCapBytes)
	}
}

func TestRunOpsArgv_TimeoutFailsAndReturns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell payload is unix-only")
	}

	start := time.Now()
	receipt := runOpsArgv(context.Background(), []string{"/bin/sh", "-c", "sleep 30"}, 300*time.Millisecond, 1)
	elapsed := time.Since(start)

	if receipt.Status != "failed" {
		t.Fatalf("status = %s, want failed", receipt.Status)
	}
	if !strings.Contains(receipt.Error, "timed out") {
		t.Fatalf("error = %q, want a timeout explanation", receipt.Error)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("timeout drag: took %v, want ~300ms + kill grace", elapsed)
	}
}

func TestExecuteOpsCommand_RejectsUnknownKind(t *testing.T) {
	receipt := executeOpsCommand(context.Background(), protocol.DaemonOpsCommandDispatch{
		CommandID: "cmd-1", Kind: "curl_exfil", Args: []byte(`{}`), TimeoutS: 5, LeaseEpoch: 1,
	})
	if receipt.Status != "rejected" {
		t.Fatalf("status = %s, want rejected", receipt.Status)
	}
	if receipt.Error == "" {
		t.Fatal("rejected receipt must carry an error explanation")
	}
}

func TestExecuteOpsCommand_BuildsWhitelistedArgv(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("asserts the linux argv wiring")
	}

	// /bin/ps exists in the test environment; the receipt proves argv was
	// built from the shared registry and actually executed.
	receipt := executeOpsCommand(context.Background(), protocol.DaemonOpsCommandDispatch{
		CommandID: "cmd-2", Kind: string(opskind.KindPsSnapshot), Args: []byte(`{}`), TimeoutS: 10, LeaseEpoch: 1,
	})
	if receipt.Status != "succeeded" && receipt.Status != "failed" {
		t.Fatalf("status = %s, want a real execution outcome", receipt.Status)
	}
	if receipt.Status == "rejected" {
		t.Fatalf("ps_snapshot rejected: %s", receipt.Error)
	}
}

func TestParseSystemctlUnits(t *testing.T) {
	out := strings.Join([]string{
		"multica-server.service loaded active running  Multica server",
		"nginx.service           loaded failed failed    A high performance web server",
		"sshd.service            loaded inactive dead      OpenBSD Secure Shell server",
		"", // trailing newline
	}, "\n")

	units := parseSystemctlUnits(out)
	if len(units) != 3 {
		t.Fatalf("units = %d, want 3", len(units))
	}
	if units[0].Name != "multica-server.service" || units[0].Status != "running" {
		t.Fatalf("units[0] = %+v", units[0])
	}
	if units[0].Type != "systemd" {
		t.Fatalf("type = %s, want systemd", units[0].Type)
	}
	if units[1].Status != "failed" {
		t.Fatalf("units[1].status = %s, want failed", units[1].Status)
	}
	if units[2].Status != "inactive" {
		t.Fatalf("units[2].status = %s, want inactive", units[2].Status)
	}
	if units[0].Detail == "" {
		t.Fatal("description must survive into detail")
	}
}

func TestParseLaunchctlList(t *testing.T) {
	out := strings.Join([]string{
		"PID\tStatus\tLabel",
		"81231\t0\tcom.multica.daemon",
		"-\t78\tcom.apple.failing.agent",
		"4567\t0\tcom.apple.Dock.agent",
	}, "\n")

	units := parseLaunchctlList(out)
	if len(units) != 3 {
		t.Fatalf("units = %d, want 3 (header dropped)", len(units))
	}
	if units[0].Name != "com.multica.daemon" || units[0].Status != "running" {
		t.Fatalf("units[0] = %+v", units[0])
	}
	if units[0].Type != "launchd" {
		t.Fatalf("type = %s, want launchd", units[0].Type)
	}
	if units[1].Status != "exited" {
		t.Fatalf("units[1].status = %s, want exited for a non-zero last status", units[1].Status)
	}
}

func TestParseWindowsServices(t *testing.T) {
	out := strings.Join([]string{
		"SERVICE_NAME: MulticaDaemon",
		"DISPLAY_NAME: Multica Daemon",
		"        STATE              : 4  RUNNING",
		"",
		"SERVICE_NAME: Spooler",
		"        STATE              : 1  STOPPED",
	}, "\n")

	units := parseWindowsServices(out)
	if len(units) != 2 {
		t.Fatalf("units = %d, want 2", len(units))
	}
	if units[0].Name != "MulticaDaemon" || units[0].Status != "running" {
		t.Fatalf("units[0] = %+v", units[0])
	}
	if units[1].Status != "stopped" {
		t.Fatalf("units[1].status = %s, want stopped", units[1].Status)
	}
}

func TestProbeOpsCaps_StaysInVocabulary(t *testing.T) {
	caps := probeOpsCaps()
	allowed := map[string]bool{"systemd": true, "launchd": true, "docker": true}
	seen := map[string]bool{}
	for _, c := range caps {
		if !allowed[c] {
			t.Fatalf("cap %q outside the vocabulary", c)
		}
		if seen[c] {
			t.Fatalf("cap %q reported twice", c)
		}
		seen[c] = true
	}
	if runtime.GOOS == "linux" && seen["launchd"] {
		t.Fatal("launchd cap on linux")
	}
	if runtime.GOOS == "darwin" && seen["systemd"] {
		t.Fatal("systemd cap on darwin")
	}
}
