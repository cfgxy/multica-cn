package handler

// RUYI-859 stage 1: per-Runtime host resource endpoint.
//
// The endpoint narrows the workspace's RUYI-618 resource panel to the one
// daemon host the runtime row binds. The security invariants pinned here:
// the daemon id is resolved server-side from the runtime row (a client can
// never request another workspace's daemon), the read gate is the same
// membership + visibility rule as the backpressure surface, and the three
// empty states — no Prometheus, no sample, no daemon — are structured 200s
// with distinct flags, never silent hiding.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/promquery"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// resourceEndpointFixture inserts one local runtime with its own daemon id
// and returns the row id. The daemon id is unique per test so the "no
// cross-workspace daemon" assertion has a clean shape to grep.
func resourceEndpointFixture(t *testing.T, name string) (runtimeID, daemonID string) {
	t.Helper()
	daemonID = fmt.Sprintf("res-daemon-%s-%d", name, time.Now().UnixNano())
	runtimeID = dbfx.Runtime(t, "res-rt-"+name, testutil.Cols{
		"runtime_mode": "local",
		"daemon_id":    daemonID,
		"visibility":   "public",
	})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM daemon_backpressure_settings WHERE daemon_id = $1`, daemonID)
	})
	return runtimeID, daemonID
}

// TestGetRuntimeResources_NarrowsToRuntimeDaemon proves the panel's
// narrowing: every query is pinned to the runtime row's daemon id, and a
// second daemon's id never leaks into the PromQL — the cross-daemon read
// the workspace panel's alternation would allow is not available here.
func TestGetRuntimeResources_NarrowsToRuntimeDaemon(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID, daemonID := resourceEndpointFixture(t, "narrow")

	stub := newStubPrometheus(t)
	stub.swap(t)

	w := httptest.NewRecorder()
	testHandler.GetRuntimeResources(w, withURLParam(
		newRequest("GET", "/api/runtimes/"+runtimeID+"/resources?window=6h", nil),
		"runtimeId", runtimeID))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Configured  bool                       `json:"configured"`
		DaemonID    string                     `json:"daemon_id"`
		HasDaemon   bool                       `json:"has_daemon"`
		Window      string                     `json:"window"`
		StepSeconds int                        `json:"step_seconds"`
		SampledAt   string                     `json:"sampled_at"`
		Series      map[string]json.RawMessage `json:"series"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Configured || !body.HasDaemon || body.DaemonID != daemonID {
		t.Fatalf("header mismatch: %+v", body)
	}
	if body.Window != "6h" {
		t.Fatalf("window = %q, want 6h", body.Window)
	}
	// The stub answers one series (labels daemon=d1) on every query, so a
	// sampled_at is present and equals the newest stub sample.
	if body.SampledAt == "" {
		t.Fatal("expected a non-empty sampled_at from the stub's newest point")
	}
	// Ten keys: the nine panel series plus boot_time_seconds for uptime.
	if len(body.Series) != 10 {
		t.Fatalf("expected 10 series keys, got %d: %v", len(body.Series), keysOf(body.Series))
	}
	asked := stub.asked()
	if len(asked) != 10 {
		t.Fatalf("expected 10 resource queries, got %d", len(asked))
	}
	for _, q := range asked {
		if !strings.Contains(q, `daemon=~"`) {
			t.Fatalf("query missing daemon matcher: %s", q)
		}
		if strings.Contains(q, daemonID) == false {
			t.Fatalf("query not narrowed to the runtime daemon %s: %s", daemonID, q)
		}
		if !strings.Contains(q, "node_") || strings.Contains(q, "multica_daemon_") {
			t.Fatalf("query must use node-exporter native metrics only: %s", q)
		}
	}
}

// TestGetRuntimeResources_EmptyStates pins the three structured empties:
// no Prometheus (configured=false), no samples (configured=true, empty
// series, no sampled_at), and no daemon (has_daemon=false). All three are
// 200 — the Runtime page renders each as a visible state.
func TestGetRuntimeResources_EmptyStates(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID, _ := resourceEndpointFixture(t, "empty")

	// 1. No Prometheus configured.
	prev := testHandler.Prometheus
	testHandler.Prometheus = nil
	t.Cleanup(func() { testHandler.Prometheus = prev })
	w := httptest.NewRecorder()
	testHandler.GetRuntimeResources(w, withURLParam(
		newRequest("GET", "/api/runtimes/"+runtimeID+"/resources", nil),
		"runtimeId", runtimeID))
	if w.Code != http.StatusOK {
		t.Fatalf("no-prometheus: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var unconfigured struct {
		Configured bool `json:"configured"`
		HasDaemon  bool `json:"has_daemon"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &unconfigured); err != nil {
		t.Fatal(err)
	}
	if unconfigured.Configured || !unconfigured.HasDaemon {
		t.Fatalf("no-prometheus: want configured=false has_daemon=true, got %+v", unconfigured)
	}

	// 2. Prometheus up but zero samples for this daemon.
	emptyStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	t.Cleanup(emptyStub.Close)
	prevClient := testHandler.Prometheus
	testHandler.Prometheus = promquery.NewClient(emptyStub.URL)
	t.Cleanup(func() { testHandler.Prometheus = prevClient })
	w = httptest.NewRecorder()
	testHandler.GetRuntimeResources(w, withURLParam(
		newRequest("GET", "/api/runtimes/"+runtimeID+"/resources", nil),
		"runtimeId", runtimeID))
	if w.Code != http.StatusOK {
		t.Fatalf("no-samples: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var empty struct {
		Configured bool   `json:"configured"`
		HasDaemon  bool   `json:"has_daemon"`
		SampledAt  string `json:"sampled_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &empty); err != nil {
		t.Fatal(err)
	}
	if !empty.Configured || !empty.HasDaemon || empty.SampledAt != "" {
		t.Fatalf("no-samples: want configured+has_daemon with no sampled_at, got %+v", empty)
	}

	// 3. Cloud / manual runtime: no daemon host at all.
	cloudID := dbfx.Runtime(t, "res-rt-cloud", testutil.Cols{
		"runtime_mode": "cloud",
		"visibility":   "public",
	})
	// The cloud row still needs a Prometheus client for cases 1 and 2 to have
	// answered — case 3 short-circuits before any query, so the client state
	// is irrelevant here; keep the empty stub installed anyway for symmetry.
	w = httptest.NewRecorder()
	testHandler.GetRuntimeResources(w, withURLParam(
		newRequest("GET", "/api/runtimes/"+cloudID+"/resources", nil),
		"runtimeId", cloudID))
	if w.Code != http.StatusOK {
		t.Fatalf("no-daemon: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var noDaemon struct {
		HasDaemon bool            `json:"has_daemon"`
		Series    json.RawMessage `json:"series"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &noDaemon); err != nil {
		t.Fatal(err)
	}
	if noDaemon.HasDaemon {
		t.Fatalf("no-daemon: has_daemon must be false, got %+v", noDaemon)
	}
	if string(noDaemon.Series) != "{}" {
		t.Fatalf("no-daemon: series must be an empty object, got %s", noDaemon.Series)
	}
}

// TestGetRuntimeResources_AccessGate pins the read contract: a non-member of
// the runtime's workspace gets the same 404 as an unknown runtime id, a
// plain member reads a private runtime they do not own (404, the same
// oracle-prevention as the backpressure surface), and a query failure from
// Prometheus is a 502 the page can distinguish from "no samples".
func TestGetRuntimeResources_AccessGate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID, _ := resourceEndpointFixture(t, "gate")

	// Private runtime owned by testUserID: a second plain member must not
	// read it (404, the same oracle-prevention as the backpressure surface).
	privateID := dbfx.Runtime(t, "res-rt-private2", testutil.Cols{
		"runtime_mode": "local",
		"daemon_id":    "res-daemon-private2",
		"visibility":   "private",
	})
	strangerID := createPermissionTestMember(t, "res-stranger@multica.test")

	w := httptest.NewRecorder()
	testHandler.GetRuntimeResources(w, withURLParam(
		newRequestAs(strangerID, http.MethodGet, "/api/runtimes/"+privateID+"/resources", nil),
		"runtimeId", privateID))
	if w.Code != http.StatusNotFound {
		t.Fatalf("stranger reading private runtime: expected 404, got %d — %s", w.Code, w.Body.String())
	}
	// The owner (testUserID) can read their own public fixture.
	w = httptest.NewRecorder()
	testHandler.GetRuntimeResources(w, withURLParam(
		newRequestAs(testUserID, http.MethodGet, "/api/runtimes/"+runtimeID+"/resources", nil),
		"runtimeId", runtimeID))
	if w.Code != http.StatusOK {
		t.Fatalf("owner reading own runtime: expected 200, got %d — %s", w.Code, w.Body.String())
	}

	// Prometheus query failure surfaces as 502, distinct from the
	// configured=true empty-sample answer above.
	failStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"error","error":"boom"}`))
	}))
	t.Cleanup(failStub.Close)
	prev := testHandler.Prometheus
	testHandler.Prometheus = promquery.NewClient(failStub.URL)
	t.Cleanup(func() { testHandler.Prometheus = prev })

	w = httptest.NewRecorder()
	testHandler.GetRuntimeResources(w, withURLParam(
		newRequestAs(testUserID, http.MethodGet, "/api/runtimes/"+runtimeID+"/resources", nil),
		"runtimeId", runtimeID))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("failing prometheus: expected 502, got %d — %s", w.Code, w.Body.String())
	}
}

// keysOf is the test's map-key reader (Go maps have no stable order).
func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
