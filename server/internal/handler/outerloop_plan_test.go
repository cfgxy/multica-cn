package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/pkg/outerloop"
)

func TestOuterLoopPlanReadAndFeatureGate(t *testing.T) {
	issueID, _, _ := seedOuterLoopPlan(t, outerloop.StateWaitingHuman, 2, 5)
	setOuterLoopFlagForTest(t, true)
	testPool.Exec(context.Background(), `UPDATE issue SET metadata = metadata || '{"outerloop_wait_reason":"等待审批"}'::jsonb WHERE id = $1`, issueID)

	w := httptest.NewRecorder()
	r := withURLParam(newRequest(http.MethodGet, "/api/issues/"+issueID+"/outerloop", nil), "id", issueID)
	testHandler.GetOuterLoopPlan(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("get plan: got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		HasPlan bool `json:"has_plan"`
		Plan    struct {
			State      string `json:"state"`
			Goal       string `json:"goal"`
			MaxRounds  int    `json:"max_rounds"`
			Round      int    `json:"round"`
			WaitReason string `json:"wait_reason"`
		} `json:"plan"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.HasPlan || got.Plan.State != outerloop.StateWaitingHuman || got.Plan.Round != 2 || got.Plan.MaxRounds != 5 || got.Plan.WaitReason != "等待审批" {
		t.Fatalf("unexpected response: %+v", got)
	}

	setOuterLoopFlagForTest(t, false)
	w = httptest.NewRecorder()
	r = withURLParam(newRequest(http.MethodGet, "/api/issues/"+issueID+"/outerloop", nil), "id", issueID)
	testHandler.GetOuterLoopPlan(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("flag-off get: got %d: %s", w.Code, w.Body.String())
	}
}

func TestOuterLoopPlanWriteEndpointsRejectWhenFeatureDisabled(t *testing.T) {
	issueID, _, _ := seedOuterLoopPlan(t, outerloop.StateRunning, 1, 5)
	setOuterLoopFlagForTest(t, false)

	requests := []struct {
		name    string
		method  string
		path    string
		body    any
		handler func(http.ResponseWriter, *http.Request)
	}{
		{"update", http.MethodPut, "/outerloop", map[string]any{"goal": "disabled"}, testHandler.PutOuterLoopPlan},
		{"fire", http.MethodPost, "/outerloop/fire", nil, testHandler.FireOuterLoopPlan},
		{"stop", http.MethodPost, "/outerloop/stop", nil, testHandler.StopOuterLoopPlan},
	}
	for _, tc := range requests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := withURLParam(newRequest(tc.method, "/api/issues/"+issueID+tc.path, tc.body), "id", issueID)
			tc.handler(w, r)
			if w.Code != http.StatusNotFound {
				t.Fatalf("flag-off %s: got %d: %s", tc.name, w.Code, w.Body.String())
			}
		})
	}
}

func TestOuterLoopPlanMemberUpdateMonotonicAndAgentForbidden(t *testing.T) {
	issueID, agentID, taskID := seedOuterLoopPlan(t, outerloop.StateRunning, 1, 5)
	setOuterLoopFlagForTest(t, true)

	w := httptest.NewRecorder()
	r := withURLParam(agentIssueRequest(http.MethodPut, "/api/issues/"+issueID+"/outerloop", map[string]any{"goal": "agent cannot set control plane"}, agentID, taskID), "id", issueID)
	testHandler.PutOuterLoopPlan(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("agent update: got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	r = withURLParam(newRequest(http.MethodPut, "/api/issues/"+issueID+"/outerloop", map[string]any{"max_rounds": 4}), "id", issueID)
	testHandler.PutOuterLoopPlan(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("lower max: got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	r = withURLParam(newRequest(http.MethodPut, "/api/issues/"+issueID+"/outerloop", map[string]any{"max_rounds": 7, "goal": "完成 M4"}), "id", issueID)
	testHandler.PutOuterLoopPlan(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("raise max: got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Plan struct {
			MaxRounds int    `json:"max_rounds"`
			Goal      string `json:"goal"`
		} `json:"plan"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if got.Plan.MaxRounds != 7 || got.Plan.Goal != "完成 M4" {
		t.Fatalf("update response = %+v", got)
	}
}

func TestOuterLoopPlanRejectsInvalidControlRequests(t *testing.T) {
	issueID, agentID, taskID := seedOuterLoopPlan(t, outerloop.StateRunning, 1, 5)
	setOuterLoopFlagForTest(t, true)

	w := httptest.NewRecorder()
	r := withURLParam(newRequest(http.MethodPut, "/api/issues/"+issueID+"/outerloop", map[string]any{}), "id", issueID)
	testHandler.PutOuterLoopPlan(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty update: got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	r = newRequest(http.MethodPut, "/api/issues/"+issueID+"/outerloop", nil)
	r.Body = io.NopCloser(bytes.NewBufferString("{"))
	r = withURLParam(r, "id", issueID)
	testHandler.PutOuterLoopPlan(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON: got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	r = withURLParam(agentIssueRequest(http.MethodPost, "/api/issues/"+issueID+"/outerloop/stop", nil, agentID, taskID), "id", issueID)
	testHandler.StopOuterLoopPlan(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("agent stop: got %d: %s", w.Code, w.Body.String())
	}
}

func TestOuterLoopPlanStopPreservesGoalAndLimit(t *testing.T) {
	issueID, _, _ := seedOuterLoopPlan(t, outerloop.StateRunning, 2, 8)
	setOuterLoopFlagForTest(t, true)
	w := httptest.NewRecorder()
	r := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/outerloop/stop", nil), "id", issueID)
	testHandler.StopOuterLoopPlan(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("stop: got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Plan struct {
			State     string `json:"state"`
			Goal      string `json:"goal"`
			MaxRounds int    `json:"max_rounds"`
		} `json:"plan"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Plan.State != outerloop.StateStopped || got.Plan.Goal != "完成 RUYI-660 验收" || got.Plan.MaxRounds != 8 {
		t.Fatalf("stop response = %+v", got)
	}
}

func TestOuterLoopPlanFireEnqueuesAndAdvancesRound(t *testing.T) {
	issueID, agentID, _ := seedOuterLoopPlan(t, outerloop.StateRunning, 1, 4)
	setOuterLoopFlagForTest(t, true)
	w := httptest.NewRecorder()
	r := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/outerloop/fire", nil), "id", issueID)
	testHandler.FireOuterLoopPlan(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("fire: got %d: %s", w.Code, w.Body.String())
	}
	if got := queuedTaskCount(t, issueID, agentID); got != 1 {
		t.Fatalf("queued tasks = %d, want 1", got)
	}
	if got := outerLoopRound(t, issueID); got != 2 {
		t.Fatalf("round = %d, want 2", got)
	}
}

func TestOuterLoopPlanFireReturnsPendingTaskWithoutAdvancingRound(t *testing.T) {
	issueID, agentID, _ := seedOuterLoopPlan(t, outerloop.StateRunning, 1, 4)
	setOuterLoopFlagForTest(t, true)

	first := httptest.NewRecorder()
	testHandler.FireOuterLoopPlan(first, withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/outerloop/fire", nil), "id", issueID))
	if first.Code != http.StatusOK {
		t.Fatalf("first fire: got %d: %s", first.Code, first.Body.String())
	}

	w := httptest.NewRecorder()
	testHandler.FireOuterLoopPlan(w, withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/outerloop/fire", nil), "id", issueID))
	if w.Code != http.StatusOK {
		t.Fatalf("second fire: got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Dispatched bool   `json:"dispatched"`
		Reason     string `json:"reason"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode pending response: %v", err)
	}
	if got.Dispatched || got.Reason != "pending_task" {
		t.Fatalf("pending response = %+v", got)
	}
	if got := queuedTaskCount(t, issueID, agentID); got != 1 {
		t.Fatalf("queued tasks = %d, want 1", got)
	}
	if got := outerLoopRound(t, issueID); got != 2 {
		t.Fatalf("round = %d, want 2", got)
	}
}

func TestOuterLoopPlanFireRollsBackTaskWhenRoundUpdateFails(t *testing.T) {
	issueID, agentID, _ := seedOuterLoopPlan(t, outerloop.StateRunning, 1, 4)
	setOuterLoopFlagForTest(t, true)
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `
		CREATE FUNCTION fail_outerloop_round_increment() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.metadata->>'outerloop_round' = '2' THEN
				RAISE EXCEPTION 'injected outer loop round update failure';
			END IF;
			RETURN NEW;
		END;
		$$;
		CREATE TRIGGER fail_outerloop_round_increment
		BEFORE UPDATE ON issue FOR EACH ROW
		EXECUTE FUNCTION fail_outerloop_round_increment();
	`); err != nil {
		t.Fatalf("install round failure trigger: %v", err)
	}
	t.Cleanup(func() {
		if _, err := testPool.Exec(ctx, `DROP TRIGGER IF EXISTS fail_outerloop_round_increment ON issue; DROP FUNCTION IF EXISTS fail_outerloop_round_increment();`); err != nil {
			t.Errorf("remove round failure trigger: %v", err)
		}
	})

	w := httptest.NewRecorder()
	r := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/outerloop/fire", nil), "id", issueID)
	testHandler.FireOuterLoopPlan(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("fire with round failure: got %d: %s", w.Code, w.Body.String())
	}
	if got := queuedTaskCount(t, issueID, agentID); got != 0 {
		t.Fatalf("queued tasks = %d, want 0 after rollback", got)
	}
	if got := outerLoopRound(t, issueID); got != 1 {
		t.Fatalf("round = %d, want 1 after rollback", got)
	}
}

func TestOuterLoopPlanFireUsesPinnedAgentAfterIssueReassignment(t *testing.T) {
	issueID, pinnedAgentID, _ := seedOuterLoopPlan(t, outerloop.StateRunning, 1, 4)
	setOuterLoopFlagForTest(t, true)

	replacementRuntimeID := dbfx.Runtime(t, "outerloop-reassigned-runtime")
	replacementAgentID := dbfx.Agent(t, "outerloop-reassigned-agent", replacementRuntimeID)
	if _, err := testPool.Exec(context.Background(), `UPDATE issue SET assignee_id = $1 WHERE id = $2`, replacementAgentID, issueID); err != nil {
		t.Fatalf("reassign issue: %v", err)
	}

	w := httptest.NewRecorder()
	r := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/outerloop/fire", nil), "id", issueID)
	testHandler.FireOuterLoopPlan(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("fire after reassignment: got %d: %s", w.Code, w.Body.String())
	}
	if got := queuedTaskCount(t, issueID, pinnedAgentID); got != 1 {
		t.Fatalf("pinned agent queued tasks = %d, want 1", got)
	}
	if got := queuedTaskCount(t, issueID, replacementAgentID); got != 0 {
		t.Fatalf("replacement agent queued tasks = %d, want 0", got)
	}
}

func TestOuterLoopPlanFireRejectsStoppedPlanAndAgentActor(t *testing.T) {
	issueID, agentID, taskID := seedOuterLoopPlan(t, outerloop.StateStopped, 1, 4)
	setOuterLoopFlagForTest(t, true)
	w := httptest.NewRecorder()
	r := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/outerloop/fire", nil), "id", issueID)
	testHandler.FireOuterLoopPlan(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("stopped fire: got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	r = withURLParam(agentIssueRequest(http.MethodPost, "/api/issues/"+issueID+"/outerloop/fire", nil, agentID, taskID), "id", issueID)
	testHandler.FireOuterLoopPlan(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("agent fire: got %d: %s", w.Code, w.Body.String())
	}
}
