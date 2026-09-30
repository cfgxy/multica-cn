package handler

// Tests for the self-evolution workspace overview (RUYI-284).
//
// The overview is an aggregate, so its tests are exact-count tests over a
// private workspace: the shared fixture workspace carries rows from every
// other test in the package, and an overview that folded those in would pass
// by accident. Each test builds its own workspace and addresses it through
// the workspace_slug query, the same resolution path the router's member
// middleware uses.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/promptquality"
	"github.com/multica-ai/multica/server/pkg/promptquiz"
)

// overviewWorkspace builds a private workspace with a unique slug and returns
// (id, slug).
func overviewWorkspace(t *testing.T) (string, string) {
	t.Helper()
	slug := fmt.Sprintf("ov-%d", time.Now().UnixNano())
	wsID := dbfx.Workspace(t, "overview test workspace", slug)
	return wsID, slug
}

// overviewDetailGet drives a detail-endpoint handler with the request bound to
// the private workspace (the workspace resolver reads X-Workspace-ID), so the
// consistency assertions below compare like with like. Extra params land in
// the chi route context the handlers read scope/scopeId from.
func overviewDetailGet(t *testing.T, userID, wsID, path string, h http.HandlerFunc, params ...string) (int, []byte) {
	t.Helper()
	req := newRequestAs(userID, http.MethodGet, path, nil)
	req.Header.Set("X-Workspace-ID", wsID)
	if len(params) > 0 {
		req = withURLParams(req, params...)
	}
	w := httptest.NewRecorder()
	h(w, req)
	return w.Code, w.Body.Bytes()
}

func getOverview(t *testing.T, slug, query string) (int, SelfEvolutionOverviewResponse) {
	t.Helper()
	path := "/api/self-evolution/overview?workspace_slug=" + slug + query
	w := httptest.NewRecorder()
	testHandler.GetSelfEvolutionOverview(w, newRequest(http.MethodGet, path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("overview %s: got %d: %s", slug, w.Code, w.Body.String())
	}
	var resp SelfEvolutionOverviewResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode overview: %v (%s)", err, w.Body.String())
		}
	}
	return w.Code, resp
}

func overviewVersion(t *testing.T, wsID, scope, scopeID string, version int, over testutil.Cols) {
	t.Helper()
	cols := testutil.Cols{
		"workspace_id":   wsID,
		"scope":          scope,
		"scope_id":       scopeID,
		"version":        version,
		"content":        "overview fixture prompt",
		"content_sha256": fmt.Sprintf("%064d", version),
		"source":         "edit",
	}
	for k, v := range over {
		cols[k] = v
	}
	dbfx.Insert(t, "prompt_version", cols)
}

func overviewQualityDay(t *testing.T, wsID, agentID string, version, daysAgo int, over testutil.Cols) {
	t.Helper()
	cols := testutil.Cols{
		"workspace_id": wsID,
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

// overviewQuizItem seeds one bank item and returns its id. The body is the
// cohort anchor: the seeded measurements carry its digest, which is what makes
// them comparable in the baseline reader.
func overviewQuizItem(t *testing.T, wsID, slug string) string {
	t.Helper()
	return dbfx.Insert(t, "prompt_quiz_item", testutil.Cols{
		"workspace_id":    wsID,
		"slug":            slug,
		"title":           "overview question",
		"body":            "anchor",
		"runtime_profile": "member",
	})
}

// overviewQuizResult seeds one measurement. Each result needs its own task row
// (prompt_quiz_result.task_id is UNIQUE), shaped like the collector leaves it.
// Tokens are the compared dimension.
func overviewQuizResult(t *testing.T, wsID, agentID, itemID string, version, tokens int, measuredAt time.Time) {
	t.Helper()
	taskID := dbfx.Task(t, agentID, testutil.Cols{
		"status":            "completed",
		"originator_source": promptquiz.OriginatorSource,
		"completed_at":      testutil.Raw("now()"),
	})
	dbfx.Insert(t, "prompt_quiz_result", testutil.Cols{
		"workspace_id":     wsID,
		"scope":            "agent",
		"scope_id":         agentID,
		"version":          version,
		"item_id":          itemID,
		"item_revision":    1,
		"item_body_sha256": promptquiz.BodyDigest("anchor"),
		"batch_id":         newQuizUUID(t),
		"task_id":          taskID,
		"outcome":          promptquiz.OutcomeAnswered,
		"run_tokens":       tokens,
		"measured_at":      measuredAt,
	})
}

// An empty workspace answers with honest empties in every section: no tier
// rows, an unmeasured window, an unmeasured quiz, an empty pool, no mirror,
// no skills. None of these may arrive as a zero pretending to be a reading.
func TestSelfEvolutionOverviewEmptyWorkspace(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, slug := overviewWorkspace(t)

	code, resp := getOverview(t, slug, "")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(resp.Versions) != 0 {
		t.Errorf("versions = %d, want no tier rows", len(resp.Versions))
	}
	if resp.Quality.Runs != 0 || resp.Quality.SubjectsMeasured != 0 {
		t.Errorf("quality runs/subjects = %d/%d, want an unmeasured window", resp.Quality.Runs, resp.Quality.SubjectsMeasured)
	}
	for key, m := range resp.Quality.Measures.ByKey() {
		if m.State != promptquality.StateNoData {
			t.Errorf("quality %s state = %s, want no_data", key, m.State)
		}
		if m.Value != nil {
			t.Errorf("quality %s produced a value with nothing measured", key)
		}
	}
	if resp.Quiz.Verdict != string(promptquiz.VerdictInsufficient) {
		t.Errorf("quiz verdict = %q, want insufficient", resp.Quiz.Verdict)
	}
	if resp.Quiz.ScopeID != "" || resp.Quiz.Measured {
		t.Errorf("quiz scope/measured = %q/%v, want no scope and unmeasured", resp.Quiz.ScopeID, resp.Quiz.Measured)
	}
	if resp.Proposals.Total != 0 || resp.Proposals.Latest != nil || len(resp.Proposals.ByStatus) != 0 {
		t.Errorf("proposals = %+v, want an empty pool", resp.Proposals)
	}
	if resp.Knowledge.Dirs != 0 || resp.Knowledge.Entries != 0 || resp.Knowledge.LastScan != nil {
		t.Errorf("knowledge = %+v, want an empty mirror", resp.Knowledge)
	}
	if resp.Skills.Count != 0 || resp.Skills.Invocations != 0 {
		t.Errorf("skills = %+v, want none", resp.Skills)
	}
}

// The fold must match what the detail tabs serve for the same rows: quality
// totals equal the sum of the per-agent dashboard windows, the quiz section is
// the latest measured scope's own baseline reading, and version counts are the
// per-tier exact counts with attribution.
func TestSelfEvolutionOverviewAggregatesWorkspaceActivity(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	wsID, slug := overviewWorkspace(t)
	actor := dbfx.User(t, "Overview Actor", fmt.Sprintf("ov-actor-%d@multica.ai", time.Now().UnixNano()))
	memberID := dbfx.User(t, "Overview Member", fmt.Sprintf("ov-mem-%d@multica.ai", time.Now().UnixNano()))
	dbfx.Member(t, wsID, memberID, "member")
	now := time.Now().UTC()

	// ── versions: three tiers, two subjects in the agent tier ──
	overviewVersion(t, wsID, "workspace", wsID, 2, testutil.Cols{"created_at": now.Add(-48 * time.Hour)})
	overviewVersion(t, wsID, "workspace", wsID, 3, testutil.Cols{
		"created_at": now.Add(-1 * time.Hour), "author_user_id": actor,
	})
	agentA := dbfx.Agent(t, fmt.Sprintf("ov-a-%d", time.Now().UnixNano()), handlerTestRuntimeID(t), testutil.Cols{"workspace_id": wsID})
	agentB := dbfx.Agent(t, fmt.Sprintf("ov-b-%d", time.Now().UnixNano()), handlerTestRuntimeID(t), testutil.Cols{"workspace_id": wsID})
	runtimeID := handlerTestRuntimeID(t)
	overviewVersion(t, wsID, "agent", agentA, 1, testutil.Cols{"created_at": now.Add(-72 * time.Hour)})
	overviewVersion(t, wsID, "agent", agentA, 2, testutil.Cols{"created_at": now.Add(-30 * time.Hour)})
	overviewVersion(t, wsID, "agent", agentB, 1, testutil.Cols{"created_at": now.Add(-24 * time.Hour)})
	projectID := dbfx.Project(t, "overview project", testutil.Cols{"workspace_id": wsID})
	overviewVersion(t, wsID, "project", projectID, 1, testutil.Cols{"created_at": now.Add(-6 * time.Hour)})

	// ── quality: two agents in the window, the same rows the detail
	// dashboard reads ──
	overviewQualityDay(t, wsID, agentA, 2, 1, testutil.Cols{
		"finished_runs":         8,
		"tool_results_measured": 30,
		"tool_results_error":    3,
		"retried_runs":          2,
	})
	overviewQualityDay(t, wsID, agentA, 2, 2, testutil.Cols{
		"finished_runs":         7,
		"tool_results_measured": 20,
		"tool_results_error":    1,
		"retried_runs":          1,
	})
	overviewQualityDay(t, wsID, agentB, 1, 1, testutil.Cols{"finished_runs": 4})
	// Outside the window: must not enter the fold.
	overviewQualityDay(t, wsID, agentA, 2, 60, testutil.Cols{"finished_runs": 999})

	// ── quiz: agentA carries the only measurements; samples are below the
	// verdict floors, so the honest state is insufficient, not a grade ──
	itemID := overviewQuizItem(t, wsID, fmt.Sprintf("ov-quiz-%d", time.Now().UnixNano()))
	overviewQuizResult(t, wsID, agentA, itemID, 1, 100, now.Add(-2*time.Hour))
	overviewQuizResult(t, wsID, agentA, itemID, 1, 120, now.Add(-90*time.Minute))
	overviewQuizResult(t, wsID, agentA, itemID, 2, 90, now.Add(-30*time.Minute))

	// ── proposals: one adopted (older), one draft (newest overall) ──
	dbfx.Insert(t, "proposal", testutil.Cols{
		"workspace_id": wsID, "type": "lesson", "status": "adopted",
		"title":      "older adopted",
		"prophecy":   testutil.Raw(`'{"outcome_text":"x","falsify_condition":"y"}'::jsonb`),
		"created_at": now.Add(-48 * time.Hour),
	})
	dbfx.Insert(t, "proposal", testutil.Cols{
		"workspace_id": wsID, "type": "pitfall", "status": "draft",
		"title":      "newest draft",
		"prophecy":   testutil.Raw(`'{"outcome_text":"x","falsify_condition":"y"}'::jsonb`),
		"created_at": now.Add(-10 * time.Minute),
	})

	// ── knowledge: one live dir, one removed; entries and scans on the live one ──
	liveDir := dbfx.Insert(t, "knowledge_dir", testutil.Cols{
		"workspace_id": wsID, "kind": "candidate_cli", "path": "/tmp/ov-live",
	})
	dbfx.Insert(t, "knowledge_dir", testutil.Cols{
		"workspace_id": wsID, "kind": "candidate_cli", "path": "/tmp/ov-removed", "removed": true,
	})
	dbfx.Insert(t, "knowledge_entry", testutil.Cols{
		"workspace_id": wsID, "dir_id": liveDir, "key": "ov-k1", "content": "c", "content_sha256": "s1",
	})
	dbfx.Insert(t, "knowledge_entry", testutil.Cols{
		"workspace_id": wsID, "dir_id": liveDir, "key": "ov-k2", "content": "c", "content_sha256": "s2",
	})
	dbfx.Insert(t, "knowledge_scan_batch", testutil.Cols{
		"workspace_id": wsID, "dir_id": liveDir, "trigger_source": "manual",
		"result": "noop", "started_at": now.Add(-2 * time.Hour),
	})
	dbfx.Insert(t, "knowledge_scan_batch", testutil.Cols{
		"workspace_id": wsID, "dir_id": liveDir, "trigger_source": "manual",
		"result": "changed", "added": 2, "started_at": now.Add(-1 * time.Hour),
	})

	// ── skills: two skills, one invoked explicitly, one merely present ──
	skillAlpha := dbfx.Insert(t, "skill", testutil.Cols{
		"workspace_id": wsID, "name": fmt.Sprintf("ov-alpha-%d", time.Now().UnixNano()),
		"description": "", "content": "x",
	})
	dbfx.Insert(t, "skill", testutil.Cols{
		"workspace_id": wsID, "name": fmt.Sprintf("ov-beta-%d", time.Now().UnixNano()),
		"description": "", "content": "x",
	})
	var alphaName string
	dbfx.QueryRow(t, "SELECT name FROM skill WHERE id = $1", skillAlpha).Scan(&alphaName)
	dbfx.Insert(t, "skill_version", testutil.Cols{
		"workspace_id": wsID, "skill_id": skillAlpha, "version": 1,
		"name": alphaName, "source": "create", "created_at": now.Add(-2 * time.Hour),
	})
	// Queued rows need the task's own runtime_id (active_requires_runtime).
	taskID := dbfx.Task(t, agentA, testutil.Cols{"runtime_id": runtimeID})
	dbfx.Insert(t, "task_message", testutil.Cols{
		"task_id": taskID, "seq": 1, "type": "tool_use", "tool": "Skill",
		"input": testutil.Raw(fmt.Sprintf(`'{"skill":"%s"}'::jsonb`, alphaName)),
	})
	// A name that matches no workspace skill version is not evidence of use.
	ghostTask := dbfx.Task(t, agentA, testutil.Cols{"runtime_id": runtimeID})
	dbfx.Insert(t, "task_message", testutil.Cols{
		"task_id": ghostTask, "seq": 1, "type": "tool_use", "tool": "Skill",
		"input": testutil.Raw(`'{"skill":"no-such-skill"}'::jsonb`),
	})
	// A non-Skill tool call is not a skill invocation.
	otherTask := dbfx.Task(t, agentA, testutil.Cols{"runtime_id": runtimeID})
	dbfx.Insert(t, "task_message", testutil.Cols{
		"task_id": otherTask, "seq": 1, "type": "tool_use", "tool": "Bash",
		"input": testutil.Raw(fmt.Sprintf(`'{"skill":"%s"}'::jsonb`, alphaName)),
	})

	code, resp := getOverview(t, slug, "")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}

	// versions
	tiers := map[string]SelfEvolutionOverviewTier{}
	for _, tier := range resp.Versions {
		tiers[tier.Scope] = tier
	}
	wsTier, ok := tiers["workspace"]
	if !ok {
		t.Fatalf("no workspace tier row: %+v", resp.Versions)
	}
	if wsTier.VersionCount != 2 || wsTier.SubjectCount != 1 {
		t.Errorf("workspace tier = %+v, want 2 versions over 1 subject", wsTier)
	}
	if wsTier.CurrentVersion == nil || *wsTier.CurrentVersion != 3 {
		t.Errorf("workspace current version = %v, want 3", wsTier.CurrentVersion)
	}
	if wsTier.LastActor != "Overview Actor" {
		t.Errorf("workspace last actor = %q, want the author", wsTier.LastActor)
	}
	agentTier, ok := tiers["agent"]
	if !ok {
		t.Fatalf("no agent tier row: %+v", resp.Versions)
	}
	if agentTier.VersionCount != 3 || agentTier.SubjectCount != 2 {
		t.Errorf("agent tier = %+v, want 3 versions over 2 subjects", agentTier)
	}
	if agentTier.CurrentVersion != nil {
		t.Errorf("agent current version = %v, want unset across 2 subjects", *agentTier.CurrentVersion)
	}
	if agentTier.LastActor != "" {
		t.Errorf("agent last actor = %q, want empty for unattributed snapshots", agentTier.LastActor)
	}
	projectTier, ok := tiers["project"]
	if !ok || projectTier.VersionCount != 1 || projectTier.CurrentVersion == nil || *projectTier.CurrentVersion != 1 {
		t.Errorf("project tier = %+v, want 1 version, current 1", projectTier)
	}
	if _, ok := tiers["squad"]; ok {
		t.Errorf("squad tier row present with no squad versions: %+v", tiers["squad"])
	}

	// quality: the overview fold equals the sum of the detail windows, and a
	// day outside the window stays outside.
	code, agentABody := overviewDetailGet(t, memberID, wsID,
		"/api/prompt-governance/agent/"+agentA+"/quality?days=30",
		testHandler.GetPromptQualityDashboard, "scope", "agent", "scopeId", agentA)
	if code != http.StatusOK {
		t.Fatalf("detail dashboard A: %d (%s)", code, agentABody)
	}
	var dashA PromptQualityResponse
	if err := json.Unmarshal(agentABody, &dashA); err != nil {
		t.Fatalf("decode dashboard A: %v", err)
	}
	code, agentBBody := overviewDetailGet(t, memberID, wsID,
		"/api/prompt-governance/agent/"+agentB+"/quality?days=30",
		testHandler.GetPromptQualityDashboard, "scope", "agent", "scopeId", agentB)
	if code != http.StatusOK {
		t.Fatalf("detail dashboard B: %d (%s)", code, agentBBody)
	}
	var dashB PromptQualityResponse
	if err := json.Unmarshal(agentBBody, &dashB); err != nil {
		t.Fatalf("decode dashboard B: %v", err)
	}
	wantRuns := dashA.Window.Runs + dashB.Window.Runs
	if resp.Quality.Runs != wantRuns {
		t.Errorf("overview runs = %d, want %d (sum of detail windows)", resp.Quality.Runs, wantRuns)
	}
	if resp.Quality.Runs != 19 {
		t.Errorf("overview runs = %d, want 19 from the seeded days", resp.Quality.Runs)
	}
	if resp.Quality.SubjectsMeasured != 2 {
		t.Errorf("subjects measured = %d, want 2", resp.Quality.SubjectsMeasured)
	}
	d4 := resp.Quality.Measures.ToolFailureRate
	if d4.State != promptquality.StateOK || d4.Numerator == nil || d4.Denominator == nil ||
		*d4.Numerator != 4 || *d4.Denominator != 50 {
		t.Errorf("folded D4 = %+v, want 4/50 across both agents", d4)
	}

	// quiz: latest measured scope wins, and the reading equals what the
	// per-scope endpoint serves for that scope (shared reader).
	if resp.Quiz.ScopeID != agentA {
		t.Errorf("quiz scope = %q, want agent A (the only measured one)", resp.Quiz.ScopeID)
	}
	if resp.Quiz.CurrentVersion != 2 || !resp.Quiz.Measured {
		t.Errorf("quiz current/measured = %d/%v, want v2 with a recorded sample", resp.Quiz.CurrentVersion, resp.Quiz.Measured)
	}
	if resp.Quiz.Verdict != string(promptquiz.VerdictInsufficient) {
		t.Errorf("quiz verdict = %q, want insufficient below the sample floors", resp.Quiz.Verdict)
	}
	code, quizBody := overviewDetailGet(t, memberID, wsID,
		"/api/prompt-governance/agent/"+agentA+"/quiz?scope=agent&scopeId="+agentA,
		testHandler.GetPromptQuizBaseline, "scope", "agent", "scopeId", agentA)
	if code != http.StatusOK {
		t.Fatalf("quiz baseline read: %d (%s)", code, quizBody)
	}
	var baseline PromptQuizBaselineResponse
	if err := json.Unmarshal(quizBody, &baseline); err != nil {
		t.Fatalf("decode baseline: %v", err)
	}
	if baseline.ScopeID != resp.Quiz.ScopeID || baseline.Measured != resp.Quiz.Measured ||
		baseline.CurrentVersion != resp.Quiz.CurrentVersion {
		t.Errorf("overview quiz %+v disagrees with the per-scope endpoint %+v", resp.Quiz, baseline)
	}

	// proposals
	if resp.Proposals.Total != 2 || resp.Proposals.Pending != 1 || resp.Proposals.Adopted != 1 {
		t.Errorf("proposals = %+v, want total 2, pending 1, adopted 1", resp.Proposals)
	}
	if resp.Proposals.Latest == nil || resp.Proposals.Latest.Title != "newest draft" ||
		resp.Proposals.Latest.Status != "draft" {
		t.Errorf("latest proposal = %+v, want the newest draft", resp.Proposals.Latest)
	}

	// knowledge
	if resp.Knowledge.Dirs != 1 || resp.Knowledge.Entries != 2 {
		t.Errorf("knowledge dirs/entries = %d/%d, want 1/2 (removed dir excluded)",
			resp.Knowledge.Dirs, resp.Knowledge.Entries)
	}
	if resp.Knowledge.LastScan == nil || resp.Knowledge.LastScan.Result != "changed" {
		t.Errorf("last scan = %+v, want the newest batch (changed)", resp.Knowledge.LastScan)
	}

	// skills
	if resp.Skills.Count != 2 {
		t.Errorf("skill count = %d, want 2", resp.Skills.Count)
	}
	if resp.Skills.Invocations != 1 {
		t.Errorf("invocations = %d, want 1 (ghost name and non-Skill tool excluded)", resp.Skills.Invocations)
	}
}

// The read gate is membership, driven through the real middleware chain the
// router mounts, so the assertion is about the gate and not the handler.
func TestSelfEvolutionOverviewRequiresWorkspaceMembership(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	wsID, slug := overviewWorkspace(t)
	outsiderID := dbfx.User(t, "Overview Outsider", fmt.Sprintf("ov-out-%d@multica.ai", time.Now().UnixNano()))

	chain := middleware.RequireWorkspaceMember(testHandler.Queries)(
		http.HandlerFunc(testHandler.GetSelfEvolutionOverview))
	path := "/api/self-evolution/overview?workspace_slug=" + slug

	req := newRequestAs(outsiderID, http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	chain.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("non-member read: expected 404, got %d: %s", w.Code, w.Body.String())
	}

	memberID := dbfx.User(t, "Overview Member", fmt.Sprintf("ov-mem2-%d@multica.ai", time.Now().UnixNano()))
	dbfx.Member(t, wsID, memberID, "member")
	req = newRequestAs(memberID, http.MethodGet, path, nil)
	w = httptest.NewRecorder()
	chain.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("member read: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// The overview is a lens: the route the router mounts answers GET and nothing
// else, so there is no write entry to reach through it.
func TestSelfEvolutionOverviewExposesNoWriteMethod(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	r := chi.NewRouter()
	r.Route("/api/self-evolution", func(r chi.Router) {
		r.Get("/overview", testHandler.GetSelfEvolutionOverview)
	})
	for _, method := range []string{
		http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, newRequest(method, "/api/self-evolution/overview", nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s /overview: expected 405, got %d", method, w.Code)
		}
	}
}
