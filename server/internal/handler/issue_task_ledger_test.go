package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestExtractACFromMarkdown(t *testing.T) {
	cases := []struct {
		name     string
		desc     string
		expected []ParsedACItem
	}{
		{
			name: "Chinese heading and explicit AC keys",
			desc: `## 背景
项目背景描述
## 验收标准（AC）
AC1 既有结构化 Issue 的 AC 可可靠提取并持久化为可独立操作的条目；新旧 Issue 均有合理兼容路径，原始目标不丢失。
AC2 TODO 可动态增删改状态、关联 AC、附证据及下一步；跨 Run、Agent、模型仍可获取一致当前状态。
AC3 TODO 完成不直接导致 AC 验收通过；AC 验收/驳回有证据和可追溯记录。
## 技术设计
详细技术方案`,
			expected: []ParsedACItem{
				{Key: "AC1", Title: "既有结构化 Issue 的 AC 可可靠提取并持久化为可独立操作的条目；新旧 Issue 均有合理兼容路径，原始目标不丢失。", Verified: false},
				{Key: "AC2", Title: "TODO 可动态增删改状态、关联 AC、附证据及下一步；跨 Run、Agent、模型仍可获取一致当前状态。", Verified: false},
				{Key: "AC3", Title: "TODO 完成不直接导致 AC 验收通过；AC 验收/驳回有证据和可追溯记录。", Verified: false},
			},
		},
		{
			name: "Bold AC key with bullets and colons",
			desc: `## 验收标准
- **AC1**：第一条验收条目
- **AC2**：第二条验收条目`,
			expected: []ParsedACItem{
				{Key: "AC1", Title: "第一条验收条目", Verified: false},
				{Key: "AC2", Title: "第二条验收条目", Verified: false},
			},
		},
		{
			name: "Checkboxes in AC section",
			desc: `## Acceptance Criteria
- [ ] AC1: Initial criteria pending
- [x] AC2: Already verified item
- [ ] Regular item without key`,
			expected: []ParsedACItem{
				{Key: "AC1", Title: "Initial criteria pending", Verified: false},
				{Key: "AC2", Title: "Already verified item", Verified: true},
				{Key: "AC3", Title: "Regular item without key", Verified: false},
			},
		},
		{
			name: "Numbered list in AC section",
			desc: `## AC
1. 第一项标准
2. 第二项标准`,
			expected: []ParsedACItem{
				{Key: "AC1", Title: "第一项标准", Verified: false},
				{Key: "AC2", Title: "第二项标准", Verified: false},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractACFromMarkdown(tc.desc)
			if len(got) != len(tc.expected) {
				t.Fatalf("expected %d items, got %d: %+v", len(tc.expected), len(got), got)
			}
			for i := range got {
				if got[i].Key != tc.expected[i].Key || got[i].Title != tc.expected[i].Title || got[i].Verified != tc.expected[i].Verified {
					t.Errorf("item %d mismatch: got %+v, want %+v", i, got[i], tc.expected[i])
				}
			}
		})
	}
}

func createLedgerTestIssue(t *testing.T, title, desc string) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":       title,
		"description": desc,
		"status":      "todo",
		"priority":    "medium",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("createLedgerTestIssue: %d %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	return issue.ID
}

func TestIssueAC_ExtractAndLifecycle(t *testing.T) {
	desc := `## 背景说明
这是一个具有结构化 AC 的测试 Issue。

## 验收标准（AC）
AC1 既有结构化 Issue 的 AC 可可靠提取并持久化为可独立操作的条目。
AC2 TODO 可动态增删改状态、关联 AC。
AC3 TODO 完成不直接导致 AC 验收通过。

## 技术方案
方案细节...`

	issueID := createLedgerTestIssue(t, "AC Lifecycle Test", desc)

	// 1. Extract ACs
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+issueID+"/ac/extract", nil)
	req = withURLParam(req, "id", issueID)
	testHandler.ExtractIssueACs(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ExtractIssueACs: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var extractResp struct {
		ExtractedCount int               `json:"extracted_count"`
		ACs            []IssueACResponse `json:"acs"`
	}
	if err := json.NewDecoder(w.Body).Decode(&extractResp); err != nil {
		t.Fatalf("decode extract response: %v", err)
	}
	if extractResp.ExtractedCount != 3 {
		t.Fatalf("expected 3 extracted ACs, got %d", extractResp.ExtractedCount)
	}
	if len(extractResp.ACs) != 3 {
		t.Fatalf("expected 3 ACs in list, got %d", len(extractResp.ACs))
	}
	if extractResp.ACs[0].Key != "AC1" || extractResp.ACs[0].Status != ACStatusUncompleted {
		t.Errorf("AC1 mismatch: got %+v", extractResp.ACs[0])
	}

	// 2. Idempotency: re-extracting should not duplicate or overwrite
	w2 := httptest.NewRecorder()
	req2 := newRequest("POST", "/api/issues/"+issueID+"/ac/extract", nil)
	req2 = withURLParam(req2, "id", issueID)
	testHandler.ExtractIssueACs(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("ExtractIssueACs second time: expected 200, got %d", w2.Code)
	}
	var extractResp2 struct {
		ExtractedCount int               `json:"extracted_count"`
		ACs            []IssueACResponse `json:"acs"`
	}
	_ = json.NewDecoder(w2.Body).Decode(&extractResp2)
	if extractResp2.ExtractedCount != 0 {
		t.Errorf("second extract should extract 0 new items, got %d", extractResp2.ExtractedCount)
	}
	if len(extractResp2.ACs) != 3 {
		t.Errorf("total AC count should still be 3, got %d", len(extractResp2.ACs))
	}

	// 3. Manual Create AC
	wCreate := httptest.NewRecorder()
	reqCreate := newRequest("POST", "/api/issues/"+issueID+"/ac", map[string]any{
		"key":   "AC4",
		"title": "第四项手工新增验收条目",
	})
	reqCreate = withURLParam(reqCreate, "id", issueID)
	testHandler.CreateIssueAC(wCreate, reqCreate)
	if wCreate.Code != http.StatusCreated {
		t.Fatalf("CreateIssueAC: expected 201, got %d: %s", wCreate.Code, wCreate.Body.String())
	}
	var createdAC IssueACResponse
	_ = json.NewDecoder(wCreate.Body).Decode(&createdAC)
	if createdAC.Key != "AC4" || createdAC.Status != ACStatusUncompleted {
		t.Errorf("created AC mismatch: %+v", createdAC)
	}

	// 4. Verify AC with evidence
	ac1ID := extractResp.ACs[0].ID
	wVerify := httptest.NewRecorder()
	reqVerify := newRequest("POST", "/api/issues/"+issueID+"/ac/"+ac1ID+"/verify", map[string]any{
		"note": "QA passed automated tests",
		"evidence": []map[string]any{
			{"type": "test", "note": "All 42 test suites passed"},
			{"type": "run", "url": "run-12345"},
		},
	})
	reqVerify = withURLParams(reqVerify, "id", issueID, "acId", ac1ID)
	testHandler.VerifyIssueAC(wVerify, reqVerify)
	if wVerify.Code != http.StatusOK {
		t.Fatalf("VerifyIssueAC: expected 200, got %d: %s", wVerify.Code, wVerify.Body.String())
	}
	var verifiedAC IssueACResponse
	_ = json.NewDecoder(wVerify.Body).Decode(&verifiedAC)
	if verifiedAC.Status != ACStatusVerified {
		t.Errorf("expected status verified, got %s", verifiedAC.Status)
	}
	if verifiedAC.VerifiedAt == nil || *verifiedAC.VerifiedAt == "" {
		t.Errorf("expected verified_at to be populated")
	}
	if len(verifiedAC.Evidence) != 2 {
		t.Errorf("expected 2 evidence items, got %d", len(verifiedAC.Evidence))
	}

	// 5. Reject AC
	ac2ID := extractResp.ACs[1].ID
	wReject := httptest.NewRecorder()
	reqReject := newRequest("POST", "/api/issues/"+issueID+"/ac/"+ac2ID+"/reject", map[string]any{
		"reason": "Boundary case fails on empty input",
	})
	reqReject = withURLParams(reqReject, "id", issueID, "acId", ac2ID)
	testHandler.RejectIssueAC(wReject, reqReject)
	if wReject.Code != http.StatusOK {
		t.Fatalf("RejectIssueAC: expected 200, got %d: %s", wReject.Code, wReject.Body.String())
	}
	var rejectedAC IssueACResponse
	_ = json.NewDecoder(wReject.Body).Decode(&rejectedAC)
	if rejectedAC.Status != ACStatusRejected {
		t.Errorf("expected status rejected, got %s", rejectedAC.Status)
	}
	if rejectedAC.RejectionReason != "Boundary case fails on empty input" {
		t.Errorf("unexpected rejection reason: %s", rejectedAC.RejectionReason)
	}

	// 6. Delete AC
	wDel := httptest.NewRecorder()
	reqDel := newRequest("DELETE", "/api/issues/"+issueID+"/ac/"+createdAC.ID, nil)
	reqDel = withURLParams(reqDel, "id", issueID, "acId", createdAC.ID)
	testHandler.DeleteIssueAC(wDel, reqDel)
	if wDel.Code != http.StatusNoContent {
		t.Fatalf("DeleteIssueAC: expected 204, got %d: %s", wDel.Code, wDel.Body.String())
	}
}

func TestIssueTodo_LifecycleAndACAssociation(t *testing.T) {
	issueID := createLedgerTestIssue(t, "TODO Lifecycle Test", "## 验收标准\n- **AC1**：支持动态 TODO\n- **AC2**：支持审计")

	// Extract ACs first
	wEx := httptest.NewRecorder()
	reqEx := newRequest("POST", "/api/issues/"+issueID+"/ac/extract", nil)
	reqEx = withURLParam(reqEx, "id", issueID)
	testHandler.ExtractIssueACs(wEx, reqEx)

	// 1. Create TODO associated with AC1
	wCreate := httptest.NewRecorder()
	reqCreate := newRequest("POST", "/api/issues/"+issueID+"/todos", map[string]any{
		"title":     "实现数据表迁移",
		"status":    TodoStatusPending,
		"next_step": "编写 up/down sql 文件",
		"ac_ids":    []string{"AC1"},
	})
	reqCreate = withURLParam(reqCreate, "id", issueID)
	testHandler.CreateIssueTodo(wCreate, reqCreate)
	if wCreate.Code != http.StatusCreated {
		t.Fatalf("CreateIssueTodo: expected 201, got %d: %s", wCreate.Code, wCreate.Body.String())
	}
	var createdTodo IssueTodoResponse
	_ = json.NewDecoder(wCreate.Body).Decode(&createdTodo)
	if createdTodo.Status != TodoStatusPending || createdTodo.NextStep != "编写 up/down sql 文件" {
		t.Errorf("created todo mismatch: %+v", createdTodo)
	}
	if !reflect.DeepEqual(createdTodo.ACIDs, []string{"AC1"}) {
		t.Errorf("ac_ids mismatch: got %v", createdTodo.ACIDs)
	}

	// 2. Update TODO to in_progress with next_step
	wUpdate := httptest.NewRecorder()
	nextStep := "执行 make sqlc 生成代码"
	inProgress := TodoStatusInProgress
	reqUpdate := newRequest("PATCH", "/api/issues/"+issueID+"/todos/"+createdTodo.ID, map[string]any{
		"status":    inProgress,
		"next_step": nextStep,
	})
	reqUpdate = withURLParams(reqUpdate, "id", issueID, "todoId", createdTodo.ID)
	testHandler.UpdateIssueTodo(wUpdate, reqUpdate)
	if wUpdate.Code != http.StatusOK {
		t.Fatalf("UpdateIssueTodo: expected 200, got %d: %s", wUpdate.Code, wUpdate.Body.String())
	}

	// 3. Update TODO to blocked with blocked_reason
	wBlock := httptest.NewRecorder()
	blocked := TodoStatusBlocked
	blockedReason := "依赖的底层库正在升级"
	reqBlock := newRequest("PATCH", "/api/issues/"+issueID+"/todos/"+createdTodo.ID, map[string]any{
		"status":         blocked,
		"blocked_reason": blockedReason,
	})
	reqBlock = withURLParams(reqBlock, "id", issueID, "todoId", createdTodo.ID)
	testHandler.UpdateIssueTodo(wBlock, reqBlock)
	if wBlock.Code != http.StatusOK {
		t.Fatalf("UpdateIssueTodo (blocked): expected 200, got %d", wBlock.Code)
	}
	var blockedTodo IssueTodoResponse
	_ = json.NewDecoder(wBlock.Body).Decode(&blockedTodo)
	if blockedTodo.Status != TodoStatusBlocked || blockedTodo.BlockedReason != blockedReason {
		t.Errorf("blocked todo mismatch: %+v", blockedTodo)
	}

	// 4. Update TODO to completed
	wComplete := httptest.NewRecorder()
	completed := TodoStatusCompleted
	reqComplete := newRequest("PATCH", "/api/issues/"+issueID+"/todos/"+createdTodo.ID, map[string]any{
		"status": completed,
		"evidence": []map[string]any{
			{"type": "commit", "url": "sha-abcdef123"},
		},
	})
	reqComplete = withURLParams(reqComplete, "id", issueID, "todoId", createdTodo.ID)
	testHandler.UpdateIssueTodo(wComplete, reqComplete)
	if wComplete.Code != http.StatusOK {
		t.Fatalf("UpdateIssueTodo (completed): expected 200, got %d", wComplete.Code)
	}

	// 5. RULE AC3 INVARIANT CHECK: Completing a TODO must NOT mark AC1 as verified!
	wACList := httptest.NewRecorder()
	reqACList := newRequest("GET", "/api/issues/"+issueID+"/ac", nil)
	reqACList = withURLParam(reqACList, "id", issueID)
	testHandler.ListIssueACs(wACList, reqACList)
	var acs []IssueACResponse
	_ = json.NewDecoder(wACList.Body).Decode(&acs)
	for _, ac := range acs {
		if ac.Key == "AC1" && ac.Status == ACStatusVerified {
			t.Fatalf("VIOLATION OF AC3: Completing a TODO must NOT automatically mark AC1 as verified! got status: %s", ac.Status)
		}
	}

	// 6. Reopen TODO (e.g. QA rejection or follow-up fix required)
	wReopen := httptest.NewRecorder()
	reqReopen := newRequest("POST", "/api/issues/"+issueID+"/todos/"+createdTodo.ID+"/reopen", map[string]any{
		"next_step": "修复 QA 提出的边界问题",
		"note":      "QA 驳回后重开",
	})
	reqReopen = withURLParams(reqReopen, "id", issueID, "todoId", createdTodo.ID)
	testHandler.ReopenIssueTodo(wReopen, reqReopen)
	if wReopen.Code != http.StatusOK {
		t.Fatalf("ReopenIssueTodo: expected 200, got %d: %s", wReopen.Code, wReopen.Body.String())
	}
	var reopenedTodo IssueTodoResponse
	_ = json.NewDecoder(wReopen.Body).Decode(&reopenedTodo)
	if reopenedTodo.Status != TodoStatusInProgress {
		t.Errorf("expected reopened todo status to be in_progress, got %s", reopenedTodo.Status)
	}
	if reopenedTodo.BlockedReason != "" {
		t.Errorf("expected blocked_reason to be cleared on reopen, got %s", reopenedTodo.BlockedReason)
	}
	if reopenedTodo.NextStep != "修复 QA 提出的边界问题" {
		t.Errorf("next_step mismatch: %s", reopenedTodo.NextStep)
	}
}

func TestIssueLedger_CompositeSummary(t *testing.T) {
	issueID := createLedgerTestIssue(t, "Composite Ledger Test", `## 验收标准
- **AC1**：第一项
- **AC2**：第二项`)

	// Extract ACs
	wEx := httptest.NewRecorder()
	reqEx := newRequest("POST", "/api/issues/"+issueID+"/ac/extract", nil)
	reqEx = withURLParam(reqEx, "id", issueID)
	testHandler.ExtractIssueACs(wEx, reqEx)

	// Create TODO 1 (in_progress)
	wT1 := httptest.NewRecorder()
	reqT1 := newRequest("POST", "/api/issues/"+issueID+"/todos", map[string]any{
		"title":     "任务一",
		"status":    TodoStatusInProgress,
		"next_step": "进行中的具体步骤",
		"ac_ids":    []string{"AC1"},
	})
	reqT1 = withURLParam(reqT1, "id", issueID)
	testHandler.CreateIssueTodo(wT1, reqT1)

	// Create TODO 2 (blocked)
	wT2 := httptest.NewRecorder()
	reqT2 := newRequest("POST", "/api/issues/"+issueID+"/todos", map[string]any{
		"title":          "任务二",
		"status":         TodoStatusBlocked,
		"blocked_reason": "缺少测试数据",
		"ac_ids":         []string{"AC2"},
	})
	reqT2 = withURLParam(reqT2, "id", issueID)
	testHandler.CreateIssueTodo(wT2, reqT2)

	// Fetch Composite Ledger
	wLedger := httptest.NewRecorder()
	reqLedger := newRequest("GET", "/api/issues/"+issueID+"/ledger", nil)
	reqLedger = withURLParam(reqLedger, "id", issueID)
	testHandler.GetIssueLedger(wLedger, reqLedger)
	if wLedger.Code != http.StatusOK {
		t.Fatalf("GetIssueLedger: expected 200, got %d: %s", wLedger.Code, wLedger.Body.String())
	}

	var ledger IssueLedgerResponse
	if err := json.NewDecoder(wLedger.Body).Decode(&ledger); err != nil {
		t.Fatalf("decode ledger: %v", err)
	}

	if ledger.Summary.ACTotal != 2 {
		t.Errorf("expected 2 ACs, got %d", ledger.Summary.ACTotal)
	}
	if ledger.Summary.TodoTotal != 2 {
		t.Errorf("expected 2 TODOs, got %d", ledger.Summary.TodoTotal)
	}
	if ledger.Summary.TodoInProgress != 1 || ledger.Summary.TodoBlocked != 1 {
		t.Errorf("todo summary status mismatch: %+v", ledger.Summary)
	}
	if len(ledger.Summary.CurrentNextSteps) < 2 {
		t.Errorf("expected at least 2 next steps, got %d: %v", len(ledger.Summary.CurrentNextSteps), ledger.Summary.CurrentNextSteps)
	}
	if len(ledger.Audit) < 4 { // 2 AC extracted audits + 2 TODO created audits
		t.Errorf("expected at least 4 audit events, got %d", len(ledger.Audit))
	}

	// Verify cross reference: AC1 should have linked_todo_ids containing TODO 1
	var ac1 IssueACResponse
	for _, ac := range ledger.ACs {
		if ac.Key == "AC1" {
			ac1 = ac
			break
		}
	}
	if len(ac1.LinkedTodoIDs) != 1 {
		t.Errorf("expected AC1 to link to 1 TODO, got %d: %v", len(ac1.LinkedTodoIDs), ac1.LinkedTodoIDs)
	}
}
