package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/supervisor"
)

var judgeNow = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func qualifyingFacts() *runFacts {
	started := judgeNow.Add(-2 * time.Hour)
	lastMsg := judgeNow.Add(-time.Hour)
	exitAt := judgeNow.Add(-55 * time.Minute)
	text := "final answer"
	return &runFacts{
		TaskID:      "run-1",
		StartedAt:   &started,
		TextCount:   3,
		MaxSeq:      38,
		LastMsgAt:   &lastMsg,
		LastText:    &text,
		AttemptDirs: []string{"/runs/run-1"},
		ExitState:   supervisor.StateExited,
		Exit:        &supervisor.ExitRecord{Code: 0, At: exitAt, Source: supervisor.ExitSourceLauncher},
	}
}

func TestJudgeQualifyingRunFlips(t *testing.T) {
	v := judge(qualifyingFacts(), 30*time.Minute, false, judgeNow)
	if !v.Flip {
		t.Fatalf("qualifying run must flip: %s", v.Reason)
	}
}

func TestJudgeDecisionTable(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*runFacts)
		dirtyOK bool
		want    bool
	}{
		{"never started", func(f *runFacts) { f.StartedAt = nil }, false, false},
		{"no text message", func(f *runFacts) { f.TextCount = 0 }, false, false},
		{"recently streaming", func(f *runFacts) {
			recent := judgeNow.Add(-time.Minute)
			f.LastMsgAt = &recent
		}, false, false},
		{"no manifest at all", func(f *runFacts) { f.AttemptDirs = nil }, false, false},
		{"worker still alive", func(f *runFacts) {
			f.ExitState = supervisor.StateRunning
			f.Exit = nil
		}, false, false},
		{"unreadable manifest", func(f *runFacts) {
			f.ExitProblem = "manifest run-1 unreadable: boom"
		}, false, false},
		{"dirty exit refused by default", func(f *runFacts) {
			f.Exit = &supervisor.ExitRecord{Code: 143, Signal: "terminated", At: judgeNow.Add(-time.Hour), Source: supervisor.ExitSourceLauncher}
		}, false, false},
		{"dirty exit allowed by flag", func(f *runFacts) {
			f.Exit = &supervisor.ExitRecord{Code: 143, Signal: "terminated", At: judgeNow.Add(-time.Hour), Source: supervisor.ExitSourceLauncher}
		}, true, true},
	}
	for _, tc := range cases {
		f := qualifyingFacts()
		tc.mutate(f)
		v := judge(f, 30*time.Minute, tc.dirtyOK, judgeNow)
		if v.Flip != tc.want {
			t.Errorf("%s: flip = %v, want %v (%s)", tc.name, v.Flip, tc.want, v.Reason)
		}
		if !tc.want && v.Flip == tc.want && v.Reason == "" {
			t.Errorf("%s: skip must carry a reason", tc.name)
		}
	}
}

// TestGatherManifestEvidenceRequiresEveryGeneration pins the conservative
// core of the tool: one live (exit-less) generation poisons the whole run —
// a worker that may still be alive is never a flip candidate.
func TestGatherManifestEvidenceRequiresEveryGeneration(t *testing.T) {
	mgr := newTestRunsDir(t)
	writeManifest(t, mgr, "a1a2a3a4-0000-0000-0000-00000000000a", supervisor.StateExited, &supervisor.ExitRecord{Code: 0, At: judgeNow, Source: supervisor.ExitSourceLauncher})

	f, err := gatherManifestEvidence(mgr, "a1a2a3a4-0000-0000-0000-00000000000a")
	if err != nil {
		t.Fatal(err)
	}
	if f.Exit == nil || f.Exit.Code != 0 {
		t.Fatalf("single exited generation must yield its exit record: %+v", f)
	}

	// A retry generation still running poisons the run.
	writeManifest(t, mgr, "a1a2a3a4-0000-0000-0000-00000000000a-1", supervisor.StateRunning, nil)
	f, err = gatherManifestEvidence(mgr, "a1a2a3a4-0000-0000-0000-00000000000a")
	if err != nil {
		t.Fatal(err)
	}
	if f.Exit != nil || f.ExitState != supervisor.StateRunning {
		t.Fatalf("live generation must poison evidence: exit=%+v state=%s", f.Exit, f.ExitState)
	}

	// Unreadable manifest poisons the run too: a base whose only attempt
	// dir carries a corrupt manifest.
	broken := filepath.Join(mgr.Dir("b1b2b3b4-0000-0000-0000-00000000000b-1"), "manifest.json")
	if err := os.MkdirAll(filepath.Dir(broken), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err = gatherManifestEvidence(mgr, "b1b2b3b4-0000-0000-0000-00000000000b")
	if err != nil {
		t.Fatal(err)
	}
	if f.ExitProblem == "" {
		t.Fatal("unreadable manifest must set ExitProblem")
	}
}

func newTestRunsDir(t *testing.T) *supervisor.Manager {
	t.Helper()
	mgr, err := supervisor.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return mgr
}

func writeManifest(t *testing.T, mgr *supervisor.Manager, runID, state string, exit *supervisor.ExitRecord) {
	t.Helper()
	if err := mgr.WriteManifest(&supervisor.Manifest{
		Version: 1,
		RunID:   runID,
		TaskID:  runID,
		State:   state,
		Exit:    exit,
	}); err != nil {
		t.Fatal(err)
	}
}
