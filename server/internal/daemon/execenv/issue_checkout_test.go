package execenv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIssueCheckoutStableAndSerialized(t *testing.T) {
	repo, workspace := newTestRepo(t), t.TempDir()
	params := LocalWorktreeParams{LocalPath: repo, IssueID: "issue", WorkspaceID: "workspace", AgentID: "agent", ConversationID: "issue", ConversationKey: "issue", RetainCheckout: true, TaskID: "first"}
	first, err := ClaimIssueCheckout(t.Context(), workspace, "issue", params, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	params.EnvRoot = first.Root
	wt, err := PrepareLocalWorktree(params, worktreeTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(wt.Path, "result.txt"), "retained\n")
	finalizeOK(t, wt)
	params.TaskID, params.AgentID = "second", "next-agent"
	wait, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := ClaimIssueCheckout(wait, workspace, "issue", params, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second Run acquired locked checkout: %v", err)
	}
	if err := first.MarkWorker(); err != nil {
		t.Fatal(err)
	}
	first.Release()
	if _, err := ClaimIssueCheckout(t.Context(), workspace, "issue", params, nil); err == nil {
		t.Fatal("unconfirmed worker was ignored")
	}
	if err := first.ClearWorker(); err != nil {
		t.Fatal(err)
	}
	second, err := ClaimIssueCheckout(t.Context(), workspace, "issue", params, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	if second.Root != first.Root {
		t.Fatalf("checkout moved: %s", second.Root)
	}
	previous, err := ReadRetainedLocalWorktree(second.Root)
	if err != nil {
		t.Fatal(err)
	}
	next, err := ReuseLocalWorktree(previous, params, worktreeTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	if next.Branch != wt.Branch || next.Path != wt.Path || !next.Continued {
		t.Fatal("same-scope agent handoff did not continue the original checkout")
	}
	if data, err := os.ReadFile(filepath.Join(next.Path, "result.txt")); err != nil || string(data) != "retained\n" {
		t.Fatalf("result not retained: %q, %v", data, err)
	}
	second.Release()
	params.RuntimeID = "different-runtime"
	if claim, err := ClaimIssueCheckout(t.Context(), workspace, "issue", params, nil); err == nil {
		claim.Release()
		t.Fatal("a different runtime acquired the existing Issue checkout")
	}
}

func TestIssueCheckoutAdoptsLegacyInPlace(t *testing.T) {
	repo, workspace := newTestRepo(t), t.TempDir()
	legacy := filepath.Join(workspace, "workspace", "legacy")
	if err := os.MkdirAll(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	legacy, err := filepath.EvalSymlinks(legacy)
	if err != nil {
		t.Fatal(err)
	}
	params := LocalWorktreeParams{EnvRoot: legacy, LocalPath: repo, IssueID: "issue", WorkspaceID: "workspace", AgentID: "agent", ConversationID: "issue", ConversationKey: "issue", RetainCheckout: true, TaskID: "first"}
	wt, err := PrepareLocalWorktree(params, worktreeTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	finalizeOK(t, wt)
	if err := WriteGCMeta(legacy, GCMeta{Kind: GCKindIssue, IssueID: "issue", WorkspaceID: "workspace"}, worktreeTestLogger()); err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimIssueCheckout(t.Context(), workspace, "issue", params, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	if claim.Root != legacy {
		t.Fatalf("expected adoption at %s, got %s", legacy, claim.Root)
	}
}

func TestIssueCheckoutArchiveRoundTrip(t *testing.T) {
	repo, workspace := newTestRepo(t), t.TempDir()
	params := LocalWorktreeParams{LocalPath: repo, IssueID: "issue", WorkspaceID: "workspace", AgentID: "agent", ConversationID: "issue", ConversationKey: "issue", RetainCheckout: true, TaskID: "first"}
	logger := worktreeTestLogger()
	c, err := ClaimIssueCheckout(t.Context(), workspace, "issue", params, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Release()
	params.EnvRoot = c.Root
	wt, err := PrepareLocalWorktree(params, logger)
	if err != nil {
		t.Fatal(err)
	}
	finalizeOK(t, wt)
	if err := c.MarkPrepared(logger); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(wt.Path, "pending.txt"), "staged\n")
	if _, err := runGit(wt.Path, "add", "pending.txt"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(wt.Path, "local.txt"), "untracked\n")
	c.Release()
	eligible := func(GCMeta) bool { return true }
	denied := errors.New("reservation denied")
	if err := recycleIssueCheckout(t.Context(), workspace, c.record, "test", "backend", eligible, func(_ context.Context, _, _, _ string, finish bool) error {
		if !finish {
			return denied
		}
		return nil
	}); !errors.Is(err, denied) {
		t.Fatalf("denied reclaim: %v", err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatal(err)
	}
	journal := c.record + ".reclaim"
	interrupted := errors.New("interrupted finish")
	var interruptedToken string
	if err := recycleIssueCheckout(t.Context(), workspace, c.record, "test", "backend", eligible, func(_ context.Context, _, _, token string, finish bool) error {
		if !finish {
			interruptedToken = token
			return denied
		}
		return interrupted
	}); !errors.Is(err, interrupted) {
		t.Fatalf("interrupted reservation: %v", err)
	}
	if _, err := os.Stat(journal); err != nil {
		t.Fatalf("journal was not preserved: %v", err)
	}
	if err := recycleIssueCheckout(t.Context(), workspace, c.record, "test", "other-backend", eligible, func(_ context.Context, _, _, _ string, _ bool) error {
		t.Fatal("different backend touched the reservation")
		return nil
	}); err == nil {
		t.Fatal("different backend accepted the journal")
	}
	if err := recycleIssueCheckout(t.Context(), workspace, c.record, "test", "backend", func(GCMeta) bool { return false }, func(_ context.Context, ws, issue, token string, finish bool) error {
		if !finish || ws != "workspace" || issue != "issue" || token != interruptedToken {
			t.Fatal("restart must only finish the journaled reservation")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(journal); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("finished journal remains: %v", err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("restart continued the old reclaim: %v", err)
	}
	active := false
	if err := recycleIssueCheckout(t.Context(), workspace, c.record, "test", "backend", eligible, func(_ context.Context, ws, issue, token string, finish bool) error {
		if ws != "workspace" || issue != "issue" || !validEnvironmentArchiveID(token) || finish != active {
			t.Fatal("invalid reservation flow")
		}
		active = !finish
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("reservation not released")
	}
	if _, err := os.Stat(c.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("root not reclaimed: %v", err)
	}
	if _, err := ClaimIssueCheckout(t.Context(), workspace, "issue", params, nil); err == nil {
		t.Fatal("established path was silently rebuilt")
	}
	if _, err := ClaimRestorableIssueCheckout(t.Context(), workspace, "issue", params, nil, "test", "different-backend", logger); err == nil {
		t.Fatal("different backend claimed archive")
	}
	restored, err := ClaimRestorableIssueCheckout(t.Context(), workspace, "issue", params, nil, "test", "backend", logger)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Release()
	if restored.Root != c.Root {
		t.Fatal("recovery relocated checkout")
	}
	previous, err := ReadRetainedLocalWorktree(restored.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReuseLocalWorktree(previous, params, logger); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(wt.Path, "local.txt")); err != nil || string(data) != "untracked\n" {
		t.Fatalf("untracked result: %q %v", data, err)
	}
	if staged, err := runGitTrimmed(wt.Path, "show", ":pending.txt"); err != nil || staged != "staged" {
		t.Fatalf("staged result: %q %v", staged, err)
	}
	if err := restored.MarkWorker(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(restored.record + ".archive"); err != nil || len(data) != 0 {
		t.Fatalf("worker must durably invalidate the old archive pointer: %q %v", data, err)
	}
	if err := restored.ClearWorker(); err != nil {
		t.Fatal(err)
	}
	restored.Release()
	if err := os.Rename(c.Root, filepath.Join(workspace, "missing-checkout")); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimRestorableIssueCheckout(t.Context(), workspace, "issue", params, nil, "test", "backend", logger); err == nil {
		t.Fatal("missing checkout silently reverted to older archive")
	}
}
