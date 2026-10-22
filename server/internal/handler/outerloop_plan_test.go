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
