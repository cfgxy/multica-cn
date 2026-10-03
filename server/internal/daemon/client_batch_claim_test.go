package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// TestClient_ClaimTasks_PostsRuntimeSetAndParsesTasks verifies the machine-level
// batch claim (MUL-4257): the client POSTs to /api/daemon/tasks/claim with the full
// runtime_id set + max_tasks, and parses the {"tasks":[...]} envelope, keeping
// each task's runtime_id so the daemon can route it locally.
func TestClient_ClaimTasks_PostsRuntimeSetAndParsesTasks(t *testing.T) {
	var gotPath string
	var gotBody struct {
		DaemonID   string   `json:"daemon_id"`
		RuntimeIDs []string `json:"runtime_ids"`
		MaxTasks   int      `json:"max_tasks"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tasks":[
			{"id":"t1","runtime_id":"rt-a","agent":{"name":"a"}},
			{"id":"t2","runtime_id":"rt-b","agent":{"name":"b"}}
		]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.SetToken("tok")

	tasks, err := c.ClaimTasks(context.Background(), "daemon-x", []string{"rt-a", "rt-b", "rt-c"}, 3, nil)
	if err != nil {
		t.Fatalf("ClaimTasks: %v", err)
	}

	if gotPath != "/api/daemon/tasks/claim" {
		t.Errorf("path = %q, want /api/daemon/tasks/claim", gotPath)
	}
	if gotBody.DaemonID != "daemon-x" {
		t.Errorf("posted daemon_id = %q, want daemon-x", gotBody.DaemonID)
	}
	if len(gotBody.RuntimeIDs) != 3 || gotBody.RuntimeIDs[0] != "rt-a" || gotBody.MaxTasks != 3 {
		t.Errorf("posted body = %+v, want runtime_ids=[rt-a rt-b rt-c] max_tasks=3", gotBody)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(tasks))
	}
	if tasks[0].ID != "t1" || tasks[0].RuntimeID != "rt-a" {
		t.Errorf("task[0] = %+v, want id=t1 runtime_id=rt-a", tasks[0])
	}
	if tasks[1].ID != "t2" || tasks[1].RuntimeID != "rt-b" {
		t.Errorf("task[1] = %+v, want id=t2 runtime_id=rt-b", tasks[1])
	}
}

// TestClient_ClaimTasks_EmptyResult confirms an empty batch (idle daemon) is
// returned as a nil/empty slice, not an error.
func TestClient_ClaimTasks_EmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tasks":[]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.SetToken("tok")

	tasks, err := c.ClaimTasks(context.Background(), "daemon-x", []string{"rt-a"}, 1, nil)
	if err != nil {
		t.Fatalf("ClaimTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("got %d tasks, want 0", len(tasks))
	}
}

// TestClient_ClaimTasks_BackpressureReportSerialization covers the RUYI-393
// optional report: a non-nil report lands in the request body under
// "backpressure" with the ratio/count fields intact, and a nil report omits
// the key entirely so old servers (and byte-exact contract tests) see the
// legacy shape.
func TestClient_ClaimTasks_BackpressureReportSerialization(t *testing.T) {
	var gotRaw map[string]json.RawMessage

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotRaw)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tasks":[]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.SetToken("tok")

	// nil report: key omitted.
	if _, err := c.ClaimTasks(context.Background(), "daemon-x", []string{"rt-a"}, 1, nil); err != nil {
		t.Fatalf("ClaimTasks(nil): %v", err)
	}
	if _, ok := gotRaw["backpressure"]; ok {
		t.Fatal("nil report must omit the backpressure key")
	}

	// Non-nil report: full payload serialized.
	if _, err := c.ClaimTasks(context.Background(), "daemon-x", []string{"rt-a"}, 1, &protocol.DaemonBackpressureReport{
		Active:             true,
		Reason:             "mem",
		MemAvailablePct:    12.3,
		SwapUsedPct:        84.5,
		PSIMemorySomeAvg10: 7.8,
		PSIReadOK:          true,
		DeferredClaims:     9,
	}); err != nil {
		t.Fatalf("ClaimTasks(report): %v", err)
	}
	raw, ok := gotRaw["backpressure"]
	if !ok {
		t.Fatal("non-nil report must serialize under backpressure")
	}
	var rep protocol.DaemonBackpressureReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("unmarshal backpressure: %v", err)
	}
	if !rep.Active || rep.Reason != "mem" || rep.MemAvailablePct != 12.3 || rep.SwapUsedPct != 84.5 ||
		rep.PSIMemorySomeAvg10 != 7.8 || !rep.PSIReadOK || rep.DeferredClaims != 9 {
		t.Fatalf("report round-trip mismatch: %+v", rep)
	}
}
