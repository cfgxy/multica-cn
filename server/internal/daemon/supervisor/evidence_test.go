package supervisor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

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
	s := &Supervisor{mgr: mgr, daemonID: "daemon-a"}
	got := s.WorkerEvidence(context.Background(), protocol.WorkerEvidenceRequest{RunID: taskID, Stream: "stdout", TailBytes: 4})
	if !got.Available || got.StdoutTail != "6789" || got.ProcessLiveness != "dead" || got.NextCursor == "" {
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
	wrongOwner := &Supervisor{mgr: mgr, daemonID: "daemon-b"}
	if denied := wrongOwner.WorkerEvidence(context.Background(), protocol.WorkerEvidenceRequest{RunID: taskID, Stream: "both", TailBytes: 8}); denied.Available || denied.Unavailable != "daemon_unreachable" {
		t.Fatalf("foreign run evidence was not denied: %+v", denied)
	}
	if bad := s.WorkerEvidence(context.Background(), protocol.WorkerEvidenceRequest{RunID: taskID, Stream: "/tmp/x", TailBytes: 999999}); bad.Available {
		t.Fatalf("invalid request accepted: %+v", bad)
	}
}
