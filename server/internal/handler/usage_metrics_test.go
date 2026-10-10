package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/promquery"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// stubPrometheus is a fake Prometheus /api/v1/query_range endpoint that
// records the queries it was asked and replies with one canned series.
type stubPrometheus struct {
	mu     sync.Mutex
	srv    *httptest.Server
	client *promquery.Client
}

func newStubPrometheus(t *testing.T) *stubPrometheus {
	t.Helper()
	s := &stubPrometheus{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{"daemon":"d1"},"values":[[1760000000,"1"],[1760000015,"2"]]}
		]}}`))
	}))
	t.Cleanup(s.srv.Close)
	s.client = promquery.NewClient(s.srv.URL)
	return s
}

// swapPrometheus installs the stub on the shared fixture handler for the
// duration of the test.
func (s *stubPrometheus) swap(t *testing.T) {
	t.Helper()
	prev := testHandler.Prometheus
	testHandler.Prometheus = s.client
	t.Cleanup(func() { testHandler.Prometheus = prev })
}

// TestDashboardUsagePanels_NotConfigured pins the contract deployments without
// PROMETHEUS_URL rely on: 200 with configured=false, which the frontend
// renders as a hidden section, never an error.
func TestDashboardUsagePanels_NotConfigured(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler fixture unavailable")
	}
	prev := testHandler.Prometheus
	testHandler.Prometheus = nil
	t.Cleanup(func() { testHandler.Prometheus = prev })

	for _, tc := range []struct {
		name    string
		handler func(w http.ResponseWriter, r *http.Request)
	}{
		{"resources", testHandler.GetDashboardUsageResources},
		{"traffic", testHandler.GetDashboardUsageTraffic},
	} {
		w := httptest.NewRecorder()
		tc.handler(w, newRequest("GET", "/api/dashboard/usage/"+tc.name, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", tc.name, w.Code, w.Body.String())
		}
		var body struct {
			Configured bool `json:"configured"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: decode: %v", tc.name, err)
		}
		if body.Configured {
			t.Fatalf("%s: expected configured=false", tc.name)
		}
	}
}

// TestDashboardUsageResources_ScopesToWorkspaceDaemons proves the panel
// queries Prometheus with a daemon matcher built from the workspace's own
// daemon ids, and decodes the proxied series.
func TestDashboardUsageResources_ScopesToWorkspaceDaemons(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	dbfx.Runtime(t, "UR panel rt", testutil.Cols{
		"daemon_id":   "usage-res-daemon",
		"device_info": "usage resources fixture",
	})

	stub := newStubPrometheus(t)
	stub.swap(t)

	w := httptest.NewRecorder()
	testHandler.GetDashboardUsageResources(w, newRequest("GET", "/api/dashboard/usage/resources?window=6h", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Configured  bool                       `json:"configured"`
		Window      string                     `json:"window"`
		StepSeconds int                        `json:"step_seconds"`
		Series      map[string]json.RawMessage `json:"series"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Configured || body.Window != "6h" {
		t.Fatalf("header mismatch: %+v", body)
	}
	// One matcher per resource query — nine queries — each restricted to the
	// workspace daemon. A cross-workspace daemon id must not appear.
	for key, raw := range body.Series {
		var series []promquery.Series
		if err := json.Unmarshal(raw, &series); err != nil {
			t.Fatalf("%s: decode series: %v", key, err)
		}
		if len(series) != 1 || series[0].Labels["daemon"] != "d1" || len(series[0].Points) != 2 {
			t.Fatalf("%s: unexpected series payload: %s", key, raw)
		}
	}
}

// TestDashboardUsageTraffic_DimensionAndWindow pins traffic panel parameter
// handling: the by dimension lands in the PromQL grouping, workspace scoping
// rides the label matcher, and an unknown dimension is a 400.
func TestDashboardUsageTraffic_DimensionAndWindow(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler fixture unavailable")
	}
	stub := newStubPrometheus(t)
	stub.swap(t)

	w := httptest.NewRecorder()
	testHandler.GetDashboardUsageTraffic(w, newRequest("GET", "/api/dashboard/usage/traffic?window=1h&by=model", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Configured bool   `json:"configured"`
		By         string `json:"by"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Configured || body.By != "model" {
		t.Fatalf("header mismatch: %+v", body)
	}

	w = httptest.NewRecorder()
	testHandler.GetDashboardUsageTraffic(w, newRequest("GET", "/api/dashboard/usage/traffic?by=bogus", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown by: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	testHandler.GetDashboardUsageTraffic(w, newRequest("GET", "/api/dashboard/usage/traffic?window=bogus", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown window: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestPlanUsageMetricsWindow pins the step/rate math so the PromQL the panels
// emit stays inside Prometheus's documented operating range at both window
// extremes.
func TestPlanUsageMetricsWindow(t *testing.T) {
	now := time.Unix(1_760_000_000, 0)
	for _, tc := range []struct {
		window   time.Duration
		wantStep time.Duration
		wantRate time.Duration
	}{
		{time.Hour, 15 * time.Second, time.Minute},
		{24 * time.Hour, 5 * time.Minute, 10 * time.Minute},
		{7 * 24 * time.Hour, 5 * time.Minute, 10 * time.Minute},
	} {
		plan := planUsageMetricsWindow(tc.window, now)
		if plan.Step != tc.wantStep {
			t.Fatalf("window %s: step %s, want %s", tc.window, plan.Step, tc.wantStep)
		}
		if plan.RateWindow != tc.wantRate {
			t.Fatalf("window %s: rate %s, want %s", tc.window, plan.RateWindow, tc.wantRate)
		}
		if plan.End != now || !plan.Start.Equal(now.Add(-tc.window)) {
			t.Fatalf("window %s: range mismatch: %+v", tc.window, plan)
		}
	}
}
