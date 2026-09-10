//go:build agentintegration

package agent

import (
	"context"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestZcodeRealACPSmoke drives the real `zcode-acp` bridge end-to-end through
// this package's own backend. It is the only check that can settle the claims
// the unit suite has to take on faith from the bridge's source: that usage
// arrives on PromptResponse.usage (so no disk-scan fallback is needed), that
// session/set_model is registered under its snake_case name, and that a turn
// reaches a terminal stopReason at all.
//
// It also pins the point of this whole change: the runtime must identify as
// zcode, not as the kimi family it used to be shelled onto.
func TestZcodeRealACPSmoke(t *testing.T) {
	requireRealAgentSmoke(t)
	if testing.Short() {
		t.Skip("skipping real-binary smoke test in -short mode")
	}
	path, err := exec.LookPath("zcode-acp")
	if err != nil {
		t.Skip("zcode-acp not on PATH; skipping real-binary smoke test")
	}
	if version, err := exec.Command(path, "--version").CombinedOutput(); err == nil {
		t.Logf("zcode-acp version: %s", strings.TrimSpace(string(version)))
	}

	backend, err := New("zcode", Config{ExecutablePath: path, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new zcode backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	session, err := backend.Execute(ctx, "Reply with exactly one word: ZCODE_SMOKE_OK", ExecOptions{
		Timeout: 150 * time.Second,
		Cwd:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()

	result := <-session.Result
	t.Logf("status=%q error=%q sessionID=%q output=%q", result.Status, result.Error, result.SessionID, result.Output)
	if result.Status != "completed" {
		t.Fatalf("status = %q, want completed (error=%q)", result.Status, result.Error)
	}
	if !strings.Contains(result.Output, "ZCODE_SMOKE_OK") {
		t.Fatalf("unexpected output: %q", result.Output)
	}
	if result.SessionID == "" {
		t.Error("no session id returned; resume and cancel both address the session by id")
	}
	// The bridge bills from PromptResponse.usage. If this is empty the
	// non-supervised path is silently free, which is worse than failing.
	if len(result.Usage) == 0 {
		t.Fatal("no usage reported: PromptResponse.usage did not reach the backend")
	}
	var total TokenUsage
	for model, u := range result.Usage {
		t.Logf("usage[%s] = input:%d output:%d", model, u.InputTokens, u.OutputTokens)
		total.InputTokens += u.InputTokens
		total.OutputTokens += u.OutputTokens
	}
	if total.InputTokens <= 0 || total.OutputTokens <= 0 {
		t.Errorf("usage totals = input:%d output:%d, want both > 0", total.InputTokens, total.OutputTokens)
	}
}

// TestZcodeRealModelDiscoverySmoke checks the catalog the profile edit UI
// renders. zcode returns its models as session/new configOptions rather than
// in a models block, and third-party entries are keyed `provider\model` — a
// backslash, not the colon every other runtime uses — so this is where a
// discovery path that only handles the common shape shows up as an empty
// dropdown.
func TestZcodeRealModelDiscoverySmoke(t *testing.T) {
	requireRealAgentSmoke(t)
	path, err := exec.LookPath("zcode-acp")
	if err != nil {
		t.Skip("zcode-acp not on PATH; skipping real-binary smoke test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	catalog, err := ListModels(ctx, "zcode", Command{Path: path})
	if err != nil {
		t.Fatalf("list zcode models: %v", err)
	}
	if len(catalog.Models) == 0 {
		t.Fatal("no models discovered: the configOptions catalog shape was not parsed")
	}
	var defaults int
	for _, m := range catalog.Models {
		t.Logf("model id=%q label=%q provider=%q default=%v", m.ID, m.Label, m.Provider, m.Default)
		if m.Default {
			defaults++
		}
	}
	if defaults != 1 {
		t.Errorf("models flagged default = %d, want exactly 1 (currentValue)", defaults)
	}
}
