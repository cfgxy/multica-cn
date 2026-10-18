package supervisor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorkerEvidenceUsesOwnedManifestAndOpaqueBoundedCursor(t *testing.T) {
	mgr, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const taskID, runID = "11111111-1111-4111-8111-111111111111", "11111111-1111-4111-8111-111111111111-1"
	if err := os.MkdirAll(mgr.Dir(runID), 0o700); err != nil {
		t.Fatal(err)
	}
	outPath, errPath := stdoutLogPath(mgr.Dir(runID)), stderrLogPath(mgr.Dir(runID))
	if err := os.WriteFile(outPath, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(errPath, []byte("failure"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mgr.WriteManifest(&Manifest{RunID: runID, TaskID: taskID, DaemonID: "daemon-a", State: StateExited, StdoutLog: outPath, StderrLog: errPath}); err != nil {
		t.Fatal(err)
	}
	const secondTask, secondRun = "22222222-2222-4222-8222-222222222222", "22222222-2222-4222-8222-222222222222-1"
	if err := os.MkdirAll(mgr.Dir(secondRun), 0o700); err != nil {
		t.Fatal(err)
	}
	secondOut, secondErr := stdoutLogPath(mgr.Dir(secondRun)), stderrLogPath(mgr.Dir(secondRun))
	if err := os.WriteFile(secondOut, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondErr, []byte("other error"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mgr.WriteManifest(&Manifest{RunID: secondRun, TaskID: secondTask, DaemonID: "daemon-a", State: StateExited, StdoutLog: secondOut, StderrLog: secondErr}); err != nil {
		t.Fatal(err)
	}
	s := &Supervisor{mgr: mgr, daemonID: "daemon-a", evidenceCursorKey: [32]byte{1}}
	got := s.WorkerEvidence(context.Background(), protocol.WorkerEvidenceRequest{RunID: taskID, Stream: "stdout", TailBytes: 4})
	if !got.Available || got.StdoutTail != "6789" || got.ProcessLiveness != "exited" || got.NextCursor == "" {
		t.Fatalf("unexpected evidence: %+v", got)
	}
	raw, err := base64.RawURLEncoding.DecodeString(got.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	var cursor evidenceCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		t.Fatal(err)
	}
	if cursor.Out != 10 {
		t.Fatalf("cursor offset=%d, want 10", cursor.Out)
	}
	next := s.WorkerEvidence(context.Background(), protocol.WorkerEvidenceRequest{RunID: taskID, Stream: "stdout", TailBytes: 4, Cursor: got.NextCursor})
	if next.StdoutTail != "" {
		t.Fatalf("incremental read repeated tail: %+v", next)
	}
	for name, tt := range map[string]struct {
		sup    *Supervisor
		run    string
		stream string
		cursor string
		want   string
	}{
		"cross daemon":   {&Supervisor{mgr: mgr, daemonID: "daemon-b", evidenceCursorKey: [32]byte{1}}, taskID, "stdout", got.NextCursor, "not_supervised"},
		"daemon restart": {&Supervisor{mgr: mgr, daemonID: "daemon-a", evidenceCursorKey: [32]byte{2}}, taskID, "stdout", got.NextCursor, "expired"},
		"cross stream":   {s, taskID, "both", got.NextCursor, "expired"},
		"cross run":      {s, secondTask, "stdout", got.NextCursor, "expired"},
		"tampered":       {s, taskID, "stdout", got.NextCursor + "x", "expired"},
	} {
		t.Run(name, func(t *testing.T) {
			denied := tt.sup.WorkerEvidence(context.Background(), protocol.WorkerEvidenceRequest{RunID: tt.run, Stream: tt.stream, TailBytes: 8, Cursor: tt.cursor})
			if denied.Available || denied.Unavailable != tt.want {
				t.Fatalf("cursor boundary response = %+v, want %s", denied, tt.want)
			}
		})
	}
	cur, reason := s.verifyEvidenceCursor(got.NextCursor, taskID, "stdout", time.Now().Add(evidenceCursorTTL+time.Second))
	if reason != "expired" || cur.Run != taskID {
		t.Fatalf("expired cursor verification = (%+v, %q)", cur, reason)
	}
	cur.At = time.Now().Add(-evidenceCursorTTL - time.Second).UTC()
	stale, ok := s.signEvidenceCursor(cur)
	if !ok {
		t.Fatal("sign expired cursor")
	}
	if denied := s.WorkerEvidence(context.Background(), protocol.WorkerEvidenceRequest{RunID: taskID, Stream: "stdout", TailBytes: 8, Cursor: stale}); denied.Available || denied.Unavailable != "expired" {
		t.Fatalf("expired cursor response = %+v", denied)
	}
	if liveness := (&Supervisor{mgr: mgr, daemonID: "daemon-a", evidenceCursorKey: [32]byte{2}}).WorkerEvidence(context.Background(), protocol.WorkerEvidenceRequest{RunID: "missing", Stream: "stdout", TailBytes: 8}); liveness.Unavailable != "not_supervised" {
		t.Fatalf("missing supervision classification = %+v", liveness)
	}
	runningManifest := &Manifest{RunID: "33333333-3333-4333-8333-333333333333-1", TaskID: "33333333-3333-4333-8333-333333333333", DaemonID: "daemon-a", State: StateRunning, Unit: "active"}
	if err := os.MkdirAll(mgr.Dir(runningManifest.RunID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stdoutLogPath(mgr.Dir(runningManifest.RunID)), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stderrLogPath(mgr.Dir(runningManifest.RunID)), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mgr.WriteManifest(runningManifest); err != nil {
		t.Fatal(err)
	}
	alive := *s
	alive.unitProbe = func(context.Context, string) (bool, error) { return true, nil }
	if gotAlive := alive.WorkerEvidence(context.Background(), protocol.WorkerEvidenceRequest{RunID: runningManifest.TaskID, Stream: "stdout", TailBytes: 1}); !gotAlive.Available || gotAlive.ProcessLiveness != "alive" {
		t.Fatalf("alive process classification = %+v", gotAlive)
	}
	if err := os.Remove(stdoutLogPath(mgr.Dir(runningManifest.RunID))); err != nil {
		t.Fatal(err)
	}
	if missingLog := alive.WorkerEvidence(context.Background(), protocol.WorkerEvidenceRequest{RunID: runningManifest.TaskID, Stream: "stdout", TailBytes: 1}); missingLog.Available || missingLog.Unavailable != "not_found" {
		t.Fatalf("missing log classification = %+v", missingLog)
	}
	if bad := s.WorkerEvidence(context.Background(), protocol.WorkerEvidenceRequest{RunID: taskID, Stream: "/tmp/x", TailBytes: 999999}); bad.Available {
		t.Fatalf("invalid request accepted: %+v", bad)
	}
}
