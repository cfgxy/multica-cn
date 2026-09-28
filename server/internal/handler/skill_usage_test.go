package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestSkillUsageCountsExplicitInvocationOnly(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	var skill SkillWithFilesResponse
	testutil.Call(t, testHandler.CreateSkill, newRequest(http.MethodPost, "/api/skills", map[string]any{
		"name": "skill-usage-test", "content": "initial",
	})).Want(http.StatusCreated).JSON(&skill)
	dbfx.Cleanup(t, `DELETE FROM skill_version WHERE skill_id = $1`, skill.ID)
	dbfx.Cleanup(t, `DELETE FROM skill WHERE id = $1`, skill.ID)
	agentID := dbfx.Agent(t, "skill usage agent", handlerTestRuntimeID(t), testutil.Cols{})
	dbfx.Exec(t, `INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1, $2)`, agentID, skill.ID)
	dbfx.Cleanup(t, `DELETE FROM agent_skill WHERE agent_id = $1`, agentID)
	issueID := dbfx.Issue(t, "skill invocation", testutil.Cols{})
	taskID := dbfx.Task(t, agentID, testutil.Cols{
		"issue_id": issueID, "runtime_id": handlerTestRuntimeID(t),
		"status": "running", "started_at": testutil.Raw("now()"),
	})

	var usage SkillUsageResponse
	getUsage := func() SkillUsageResponse {
		t.Helper()
		var result SkillUsageResponse
		testutil.Call(t, testHandler.GetSkillUsage,
			withURLParam(newRequest(http.MethodGet, "/api/skills/"+skill.ID+"/usage", nil), "id", skill.ID),
		).Want(http.StatusOK).JSON(&result)
		return result
	}
	usage = getUsage()
	if usage.Total != 0 || len(usage.Recent) != 0 || usage.AssignedAgents != 1 {
		t.Fatalf("assigned but not invoked must remain unmeasured: %+v", usage)
	}
	testutil.Call(t, testHandler.ReportTaskMessages, batchMessagesRequest(t, taskID, []any{
		map[string]any{"seq": 1, "type": "text", "content": "skill-usage-test"},
		map[string]any{"seq": 2, "type": "tool_use", "tool": "Skill", "input": map[string]any{"skill": "another-skill"}},
		map[string]any{"seq": 3, "type": "tool_use", "tool": "Skill", "input": map[string]any{"skill": "skill-usage-test"}},
	})).Want(http.StatusOK)
	usage = getUsage()
	if usage.Total != 1 || len(usage.Recent) != 1 || usage.Recent[0].Version != 1 || usage.Recent[0].TaskID != taskID {
		t.Fatalf("expected one explicit v1 invocation: %+v", usage)
	}
	if usage.Recent[0].IssueID == nil || *usage.Recent[0].IssueID != issueID {
		t.Fatalf("usage has no issue provenance: %+v", usage.Recent[0])
	}
	if usage.Since == "" {
		t.Fatal("instrumentation start must be explicit")
	}
}
