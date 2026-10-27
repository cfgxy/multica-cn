package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// TestSkillEffectComparesUseGroupWithControl covers the §S.5 four-metric
// comparison: the use group counts only runs with an explicit invocation, the
// control group only same-role same-project runs without one, first-pass
// outcomes follow the coarse D7 rule (never-reviewed issues count in neither
// column), and the D3 row stays "not scored" instead of inventing a band.
func TestSkillEffectComparesUseGroupWithControl(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	var skill SkillWithFilesResponse
	testutil.Call(t, testHandler.CreateSkill, newRequest(http.MethodPost, "/api/skills", map[string]any{
		"name": "skill-effect-test", "content": "initial",
	})).Want(http.StatusCreated).JSON(&skill)
	dbfx.Cleanup(t, `DELETE FROM skill_version WHERE skill_id = $1`, skill.ID)
	dbfx.Cleanup(t, `DELETE FROM skill WHERE id = $1`, skill.ID)

	agentID := dbfx.Agent(t, "skill effect agent", handlerTestRuntimeID(t), testutil.Cols{})
	otherAgentID := dbfx.Agent(t, "skill effect bystander", handlerTestRuntimeID(t), testutil.Cols{})

	useIssue := dbfx.Issue(t, "skill effect use", testutil.Cols{})
	controlIssue := dbfx.Issue(t, "skill effect control", testutil.Cols{})
	bystanderIssue := dbfx.Issue(t, "skill effect bystander", testutil.Cols{})

	// Use-group run: one invocation, first attempt, measured cost 100.
	useTask := dbfx.Task(t, agentID, testutil.Cols{
		"issue_id": useIssue, "runtime_id": handlerTestRuntimeID(t),
		"status": "completed", "started_at": testutil.Raw("now()"),
		"completed_at": testutil.Raw("now()"), "attempt": 1,
	})
	// Control run: same role, same (absent) project, no invocation, retried once,
	// higher cost — the counterfactual the card has to surface.
	controlTask := dbfx.Task(t, agentID, testutil.Cols{
		"issue_id": controlIssue, "runtime_id": handlerTestRuntimeID(t),
		"status": "completed", "started_at": testutil.Raw("now()"),
		"completed_at": testutil.Raw("now()"), "attempt": 2,
	})
	// Bystander run: a role that never used the skill cannot be the
	// counterfactual for choosing not to use it — excluded from the control,
	// and its never-reviewed issue must not move the review counters either.
	dbfx.Task(t, otherAgentID, testutil.Cols{
		"issue_id": bystanderIssue, "runtime_id": handlerTestRuntimeID(t),
		"status": "completed", "started_at": testutil.Raw("now()"),
		"completed_at": testutil.Raw("now()"),
	})
	testutil.Call(t, testHandler.ReportTaskMessages, batchMessagesRequest(t, useTask, []any{
		map[string]any{"seq": 1, "type": "tool_use", "tool": "Skill", "input": map[string]any{"skill": "skill-effect-test"}},
	})).Want(http.StatusOK)
	dbfx.Exec(t, `INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens)
		VALUES ($1, 'test', 'model', 80, 20), ($2, 'test', 'model', 250, 50)`, useTask, controlTask)
	dbfx.Cleanup(t, `DELETE FROM task_usage WHERE task_id = ANY($1::uuid[])`, []string{useTask, controlTask})

	// D7 outcomes: the use issue passed review first try; the control issue was
	// sent back once. The bystander issue never reached review and must count
	// in neither column.
	dbfx.Exec(t, `INSERT INTO activity_log (workspace_id, issue_id, actor_type, actor_id, action, details)
		VALUES ($1, $2, 'member', $3, 'status_changed', '{"from":"in_progress","to":"in_review"}'),
		       ($1, $4, 'member', $3, 'status_changed', '{"from":"in_progress","to":"in_review"}'),
		       ($1, $4, 'member', $3, 'status_changed', '{"from":"in_review","to":"in_progress"}')`,
		testWorkspaceID, useIssue, testUserID, controlIssue)
	dbfx.Cleanup(t, `DELETE FROM activity_log WHERE issue_id = ANY($1::uuid[])`, []string{useIssue, controlIssue})

	var effect SkillEffectResponse
	testutil.Call(t, testHandler.GetSkillEffect,
		withURLParam(newRequest(http.MethodGet, "/api/skills/"+skill.ID+"/effect", nil), "id", skill.ID),
	).Want(http.StatusOK).JSON(&effect)
	if effect.Since == "" {
		t.Fatal("instrumentation window start must be explicit")
	}
	if effect.UseGroup.Runs != 1 || effect.UseGroup.TokenSamples != 1 ||
		effect.UseGroup.MedianTotalTokens == nil || *effect.UseGroup.MedianTotalTokens != 100 ||
		effect.UseGroup.RetriedRuns != 0 || effect.UseGroup.ReviewedIssues != 1 || effect.UseGroup.FirstPassIssues != 1 {
		t.Fatalf("use group metrics: %+v", effect.UseGroup)
	}
	if effect.ControlGroup.Runs != 1 || effect.ControlGroup.TokenSamples != 1 ||
		effect.ControlGroup.MedianTotalTokens == nil || *effect.ControlGroup.MedianTotalTokens != 300 ||
		effect.ControlGroup.RetriedRuns != 1 || effect.ControlGroup.ReviewedIssues != 1 || effect.ControlGroup.FirstPassIssues != 0 {
		t.Fatalf("control group metrics: %+v", effect.ControlGroup)
	}
	if effect.Perplexity.Scored {
		t.Fatalf("D3 must stay not-scored without prompt scores: %+v", effect.Perplexity)
	}
	if len(effect.VersionEvents) != 0 {
		t.Fatalf("single version must produce no events: %+v", effect.VersionEvents)
	}
}

// TestSkillEffectZeroVersionSkillServesEmptyView pins the pre-941 legacy
// shape: a skill with no version rows has no instrumentation window, so the
// effect card serves an explicit empty view (200) instead of a NULL-scan 500.
func TestSkillEffectZeroVersionSkillServesEmptyView(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	var skill SkillWithFilesResponse
	testutil.Call(t, testHandler.CreateSkill, newRequest(http.MethodPost, "/api/skills", map[string]any{
		"name": "skill-effect-zero-version", "content": "initial",
	})).Want(http.StatusCreated).JSON(&skill)
	dbfx.Cleanup(t, `DELETE FROM skill_version WHERE skill_id = $1`, skill.ID)
	dbfx.Cleanup(t, `DELETE FROM skill WHERE id = $1`, skill.ID)
	// Strip the auto-created first version: skills created before migration
	// 941 and never edited since have no rows here.
	dbfx.Exec(t, `DELETE FROM skill_version WHERE skill_id = $1`, skill.ID)

	var effect SkillEffectResponse
	testutil.Call(t, testHandler.GetSkillEffect,
		withURLParam(newRequest(http.MethodGet, "/api/skills/"+skill.ID+"/effect", nil), "id", skill.ID),
	).Want(http.StatusOK).JSON(&effect)
	if effect.Since != "" {
		t.Fatalf("zero-version skill must have no window start, got since=%q", effect.Since)
	}
	if effect.UseGroup.Runs != 0 || effect.ControlGroup.Runs != 0 || len(effect.VersionEvents) != 0 {
		t.Fatalf("zero-version effect must be an empty view: %+v", effect)
	}
}

// TestSkillEffectVersionEventWindows covers the §S.6 timeline: a version bump
// produces one event whose before window covers the outgoing version's uses
// and whose after window covers the incoming version's uses, with the window
// mode named per side.
func TestSkillEffectVersionEventWindows(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	var skill SkillWithFilesResponse
	testutil.Call(t, testHandler.CreateSkill, newRequest(http.MethodPost, "/api/skills", map[string]any{
		"name": "skill-event-test", "content": "v1 body",
	})).Want(http.StatusCreated).JSON(&skill)
	dbfx.Cleanup(t, `DELETE FROM skill_version WHERE skill_id = $1`, skill.ID)
	dbfx.Cleanup(t, `DELETE FROM skill WHERE id = $1`, skill.ID)

	agentID := dbfx.Agent(t, "skill event agent", handlerTestRuntimeID(t), testutil.Cols{})
	issueID := dbfx.Issue(t, "skill event", testutil.Cols{})
	beforeTask := dbfx.Task(t, agentID, testutil.Cols{
		"issue_id": issueID, "runtime_id": handlerTestRuntimeID(t),
		"status": "completed", "started_at": testutil.Raw("now()"),
		"completed_at": testutil.Raw("now()"),
	})
	testutil.Call(t, testHandler.ReportTaskMessages, batchMessagesRequest(t, beforeTask, []any{
		map[string]any{"seq": 1, "type": "tool_use", "tool": "Skill", "input": map[string]any{"skill": "skill-event-test"}},
	})).Want(http.StatusOK)

	testutil.Call(t, testHandler.UpdateSkill,
		withURLParam(newRequest(http.MethodPut, "/api/skills/"+skill.ID, map[string]any{
			"content": "v2 body",
		}), "id", skill.ID),
	).Want(http.StatusOK)

	// The post-event run starts explicitly after v2 exists so the version
	// LATERAL resolves it to v2 without racing the update timestamp.
	afterTask := dbfx.Task(t, agentID, testutil.Cols{
		"issue_id": issueID, "runtime_id": handlerTestRuntimeID(t),
		"status": "completed", "started_at": testutil.Raw("now() + interval '5 seconds'"),
		"completed_at": testutil.Raw("now() + interval '6 seconds'"),
	})
	testutil.Call(t, testHandler.ReportTaskMessages, batchMessagesRequest(t, afterTask, []any{
		map[string]any{"seq": 1, "type": "tool_use", "tool": "Skill", "input": map[string]any{"skill": "skill-event-test"}},
	})).Want(http.StatusOK)
	dbfx.Exec(t, `INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens)
		VALUES ($1, 'test', 'model', 10, 10), ($2, 'test', 'model', 20, 20)`, beforeTask, afterTask)
	dbfx.Cleanup(t, `DELETE FROM task_usage WHERE task_id = ANY($1::uuid[])`, []string{beforeTask, afterTask})

	var effect SkillEffectResponse
	testutil.Call(t, testHandler.GetSkillEffect,
		withURLParam(newRequest(http.MethodGet, "/api/skills/"+skill.ID+"/effect", nil), "id", skill.ID),
	).Want(http.StatusOK).JSON(&effect)
	if len(effect.VersionEvents) != 1 {
		t.Fatalf("one version bump must produce one event: %+v", effect.VersionEvents)
	}
	event := effect.VersionEvents[0]
	if event.Version != 2 || event.FromVersion != 1 || event.Source != "edit" {
		t.Fatalf("event metadata: %+v", event)
	}
	if event.Before == nil || event.Before.Runs != 1 || event.Before.MedianTotalTokens == nil || *event.Before.MedianTotalTokens != 20 {
		t.Fatalf("before window must cover the outgoing version's run: %+v", event.Before)
	}
	if event.After == nil || event.After.Runs != 1 || event.After.MedianTotalTokens == nil || *event.After.MedianTotalTokens != 40 {
		t.Fatalf("after window must cover the incoming version's run: %+v", event.After)
	}
	if event.BeforeMode != "days" || event.AfterMode != "days" {
		t.Fatalf("windows closed by the day range with one use each: before=%s after=%s", event.BeforeMode, event.AfterMode)
	}
}
