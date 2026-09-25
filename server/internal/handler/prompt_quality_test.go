package handler

// Tests for the prompt quality dashboard read path (RUYI-184, self-evolution
// phase 2). They drive the handler directly, and the permission test drives it
// through the real middleware.RequireWorkspaceMember chain from router.go, for
// the same reason prompt_version_test.go does: an assertion about a gate has to
// exercise the gate.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/promptquality"
)

// qualityAgent creates an agent and removes its rollup rows afterwards. The
// rollup tables have no id-based cleanup hook in the fixture because rows are
// written by scope, not by test.
func qualityAgent(t *testing.T, name string) string {
	t.Helper()
	agentID := dbfx.Agent(t, name, handlerTestRuntimeID(t), nil)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM prompt_quality_daily WHERE scope = 'agent' AND scope_id = $1`, agentID)
		testPool.Exec(context.Background(), `DELETE FROM prompt_perplexity_score WHERE scope = 'agent' AND scope_id = $1`, agentID)
	})
	return agentID
}

// qualityDay writes one rollup bucket. Only the columns a test cares about are
// passed; everything else keeps the table's default, which is the "measured
// nothing" state.
func qualityDay(t *testing.T, agentID string, version int, daysAgo int, over testutil.Cols) {
	t.Helper()
	cols := testutil.Cols{
		"workspace_id": testWorkspaceID,
		"scope":        "agent",
		"scope_id":     agentID,
		"version":      version,
		"day":          time.Now().UTC().AddDate(0, 0, -daysAgo).Format("2006-01-02"),
	}
	for k, v := range over {
		cols[k] = v
	}
	dbfx.Insert(t, "prompt_quality_daily", cols)
}

func getQualityDashboard(t *testing.T, agentID string, query string) (int, PromptQualityResponse) {
	t.Helper()
	path := "/api/prompt-governance/agent/" + agentID + "/quality" + query
	req := withURLParams(newRequest(http.MethodGet, path, nil), "scope", "agent", "scopeId", agentID)
	w := httptest.NewRecorder()
	testHandler.GetPromptQualityDashboard(w, req)

	var resp PromptQualityResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode dashboard: %v (%s)", err, w.Body.String())
		}
	}
	return w.Code, resp
}

// A version's days are folded together and the counted dimensions add up.
func TestPromptQualityDashboardFoldsDaysPerVersion(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := qualityAgent(t, "quality-fold")

	qualityDay(t, agentID, 3, 1, testutil.Cols{
		"finished_runs":         8,
		"tool_results_measured": 30,
		"tool_results_error":    3,
		"retried_runs":          2,
	})
	qualityDay(t, agentID, 3, 2, testutil.Cols{
		"finished_runs":         7,
		"tool_results_measured": 20,
		"tool_results_error":    1,
		"retried_runs":          1,
	})
	qualityDay(t, agentID, 2, 5, testutil.Cols{"finished_runs": 4})

	code, resp := getQualityDashboard(t, agentID, "?days=30")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(resp.Versions) != 2 {
		t.Fatalf("versions = %d, want 2 (v3 and v2)", len(resp.Versions))
	}
	if resp.Versions[0].Version != 3 {
		t.Errorf("first version = %d, want the newest (3)", resp.Versions[0].Version)
	}
	v3 := resp.Versions[0]
	if v3.Runs != 15 || v3.Days != 2 {
		t.Errorf("v3 runs/days = %d/%d, want 15/2", v3.Runs, v3.Days)
	}
	d4 := v3.Measures.ToolFailureRate
	if d4.State != promptquality.StateOK {
		t.Fatalf("v3 D4 state = %s, want ok", d4.State)
	}
	if d4.Denominator == nil || *d4.Denominator != 50 || d4.Numerator == nil || *d4.Numerator != 4 {
		t.Errorf("v3 D4 = %v/%v, want 4/50 summed across the two days", d4.Numerator, d4.Denominator)
	}
	if resp.Window.Runs != 19 {
		t.Errorf("window runs = %d, want 19 across both versions", resp.Window.Runs)
	}
}

// T5: a version whose runs predate the is_error column reads as "no data" on
// D4. Zero measured tool results is exactly that state, and it must not
// surface as a perfect 0% failure rate.
func TestPromptQualityDashboardReportsUninstrumentedRunsAsNoData(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := qualityAgent(t, "quality-uninstrumented")
	qualityDay(t, agentID, 1, 1, testutil.Cols{
		"finished_runs":         40,
		"tool_results_measured": 0,
		"tool_results_error":    0,
	})

	_, resp := getQualityDashboard(t, agentID, "")
	d4 := resp.Window.Measures.ToolFailureRate
	if d4.State != promptquality.StateNoData {
		t.Fatalf("D4 state = %s, want no_data for runs with no is_error reported", d4.State)
	}
	if d4.Value != nil {
		t.Errorf("D4 value = %v, want nil — an uninstrumented day must not render 0%%", *d4.Value)
	}
	if d4.Reason != promptquality.ReasonNotInstrumented {
		t.Errorf("D4 reason = %q, want %q", d4.Reason, promptquality.ReasonNotInstrumented)
	}
	// The other cards are unaffected: 40 runs is plenty for D5.
	if resp.Window.Measures.RetryRate.State != promptquality.StateOK {
		t.Errorf("D5 state = %s, want ok — one uninstrumented dimension must not blank the rest",
			resp.Window.Measures.RetryRate.State)
	}
}

// T7: below the declared floor the API returns a marker, not a number. The
// frontend has no path that renders a value in this state because there is no
// value to render.
func TestPromptQualityDashboardMarksInsufficientSample(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := qualityAgent(t, "quality-thin")
	qualityDay(t, agentID, 1, 1, testutil.Cols{
		"finished_runs": 3,
		"retried_runs":  1,
	})

	_, resp := getQualityDashboard(t, agentID, "")
	d5 := resp.Window.Measures.RetryRate
	if d5.State != promptquality.StateInsufficientSample {
		t.Fatalf("D5 state = %s, want insufficient_sample for 3 runs", d5.State)
	}
	if d5.Value != nil {
		t.Errorf("D5 value = %v, want nil below the floor", *d5.Value)
	}
	if d5.Sample != 3 || d5.Threshold != promptquality.MinSampleRuns {
		t.Errorf("D5 sample/threshold = %d/%d, want 3/%d so the badge can state the rule it applied",
			d5.Sample, d5.Threshold, promptquality.MinSampleRuns)
	}
}

// A scope that has never been rolled up returns the payload with every card in
// its "no data" state — not an error, and not an empty body the UI would have
// to guess at.
func TestPromptQualityDashboardOnAScopeWithNoRollup(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := qualityAgent(t, "quality-empty")

	code, resp := getQualityDashboard(t, agentID, "")
	if code != http.StatusOK {
		t.Fatalf("expected 200 for an unrolled scope, got %d", code)
	}
	if len(resp.Versions) != 0 {
		t.Errorf("versions = %d, want none", len(resp.Versions))
	}
	for key, m := range resp.Window.Measures.ByKey() {
		if m.State != promptquality.StateNoData {
			t.Errorf("%s state = %s, want no_data", key, m.State)
		}
		if m.Value != nil {
			t.Errorf("%s produced a value with nothing measured", key)
		}
	}
}

// T3: with the optional sources off, every card still answers and the only
// change is the footer flag.
func TestPromptQualityDashboardWorksWithOptionalSourcesOff(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	t.Setenv("LANGFUSE_PUBLIC_KEY", "")
	t.Setenv("LANGFUSE_SECRET_KEY", "")

	agentID := qualityAgent(t, "quality-degraded")
	qualityDay(t, agentID, 1, 1, testutil.Cols{
		"finished_runs":         20,
		"tool_results_measured": 100,
		"tool_results_error":    5,
		"retried_runs":          4,
	})

	code, resp := getQualityDashboard(t, agentID, "")
	if code != http.StatusOK {
		t.Fatalf("expected 200 with langfuse off, got %d", code)
	}
	if !resp.DataSources.Degraded {
		t.Error("degraded flag not set with langfuse unconfigured")
	}
	var sawLangfuse bool
	for _, item := range resp.DataSources.Items {
		if item.Kind == promptquality.SourceLangfuse {
			sawLangfuse = true
			if item.Available || item.Required {
				t.Errorf("langfuse = %+v, want unavailable and not required", item)
			}
		}
	}
	if !sawLangfuse {
		t.Error("langfuse missing from data_sources; the footer cannot report it")
	}
	if resp.Window.Measures.ToolFailureRate.State != promptquality.StateOK {
		t.Error("D4 degraded because an unrelated external source is off")
	}
	if resp.Window.Measures.RetryRate.State != promptquality.StateOK {
		t.Error("D5 degraded because an unrelated external source is off")
	}
}

// D3 scores come back one row per runtime profile, never merged.
func TestPromptQualityDashboardKeepsRuntimeProfilesApart(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := qualityAgent(t, "quality-perplexity")
	for _, p := range []struct {
		profile string
		band    string
		low     int
	}{{"member", "low", 10}, {"leader_task", "high", 70}} {
		dbfx.Insert(t, "prompt_perplexity_score", testutil.Cols{
			"workspace_id":    testWorkspaceID,
			"scope":           "agent",
			"scope_id":        agentID,
			"version":         1,
			"runtime_profile": p.profile,
			"band":            p.band,
			"percent_low":     p.low,
			"percent_high":    p.low + 10,
			"evidence":        testutil.Raw(`'[]'::jsonb`),
			"model":           "test-model",
		})
	}

	_, resp := getQualityDashboard(t, agentID, "")
	if len(resp.Perplexity) != 2 {
		t.Fatalf("perplexity rows = %d, want one per runtime profile", len(resp.Perplexity))
	}
	bands := map[string]string{}
	for _, s := range resp.Perplexity {
		bands[s.RuntimeProfile] = s.Band
		if s.PercentLow == nil || s.PercentHigh == nil {
			t.Errorf("%s returned a band with no percent range", s.RuntimeProfile)
		}
	}
	if bands["member"] != "low" || bands["leader_task"] != "high" {
		t.Errorf("bands = %v, want the two profiles reported separately", bands)
	}
}

// A scope belonging to another workspace is a 404 and leaks no rollup.
func TestPromptQualityDashboardIsolatesWorkspaces(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	otherWorkspace := dbfx.Workspace(t, "Quality Other WS", "quality-other-ws")
	otherAgent := dbfx.Agent(t, "quality-other-agent", "", testutil.Cols{"workspace_id": otherWorkspace})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM prompt_quality_daily WHERE scope = 'agent' AND scope_id = $1`, otherAgent)
	})
	dbfx.Insert(t, "prompt_quality_daily", testutil.Cols{
		"workspace_id":  otherWorkspace,
		"scope":         "agent",
		"scope_id":      otherAgent,
		"version":       1,
		"day":           time.Now().UTC().Format("2006-01-02"),
		"finished_runs": 25,
	})

	code, _ := getQualityDashboard(t, otherAgent, "")
	if code != http.StatusNotFound {
		t.Fatalf("cross-workspace read: expected 404, got %d", code)
	}
}

// The read gate is membership. Driving the real chain proves the assertion is
// not vacuous: a non-member is rejected and a member goes through.
func TestPromptQualityDashboardRequiresWorkspaceMembership(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := qualityAgent(t, "quality-permission")
	outsiderID := dbfx.User(t, "Quality Outsider", "quality-outsider@multica.ai")

	chain := middleware.RequireWorkspaceMember(testHandler.Queries)(
		http.HandlerFunc(testHandler.GetPromptQualityDashboard))
	path := "/api/prompt-governance/agent/" + agentID + "/quality"

	// A non-member gets 404, not 403: RequireWorkspaceMember answers a
	// non-member the same way it answers a workspace that does not exist, so
	// membership cannot be probed from the outside.
	req := withURLParams(newRequestAs(outsiderID, http.MethodGet, path, nil), "scope", "agent", "scopeId", agentID)
	w := httptest.NewRecorder()
	chain.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("non-member read: expected 404, got %d: %s", w.Code, w.Body.String())
	}

	memberUserID := dbfx.User(t, "Quality Member", "quality-member@multica.ai")
	dbfx.Member(t, testWorkspaceID, memberUserID, "member")
	req = withURLParams(newRequestAs(memberUserID, http.MethodGet, path, nil), "scope", "agent", "scopeId", agentID)
	w = httptest.NewRecorder()
	chain.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("member read: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}
