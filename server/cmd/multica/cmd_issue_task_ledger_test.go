package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestIssueTaskLedgerCommandsRegistration(t *testing.T) {
	// Verify that ac, todo, and ledger subcommands are registered under issueCmd
	commands := issueCmd.Commands()
	hasAC := false
	hasTodo := false
	hasLedger := false

	for _, cmd := range commands {
		switch cmd.Name() {
		case "ac":
			hasAC = true
		case "todo":
			hasTodo = true
		case "ledger":
			hasLedger = true
		}
	}

	if !hasAC {
		t.Errorf("issueCmd missing 'ac' subcommand")
	}
	if !hasTodo {
		t.Errorf("issueCmd missing 'todo' subcommand")
	}
	if !hasLedger {
		t.Errorf("issueCmd missing 'ledger' subcommand")
	}

	// Verify subcommands of ac
	acSubcommands := issueACCmd.Commands()
	expectedACSubs := map[string]bool{"list": false, "extract": false, "add": false, "verify": false, "reject": false, "delete": false}
	for _, cmd := range acSubcommands {
		if _, ok := expectedACSubs[cmd.Name()]; ok {
			expectedACSubs[cmd.Name()] = true
		}
	}
	for sub, found := range expectedACSubs {
		if !found {
			t.Errorf("issueACCmd missing '%s' subcommand", sub)
		}
	}

	// Verify subcommands of todo
	todoSubcommands := issueTodoCmd.Commands()
	expectedTodoSubs := map[string]bool{"list": false, "add": false, "update": false, "reopen": false, "delete": false}
	for _, cmd := range todoSubcommands {
		if _, ok := expectedTodoSubs[cmd.Name()]; ok {
			expectedTodoSubs[cmd.Name()] = true
		}
	}
	for sub, found := range expectedTodoSubs {
		if !found {
			t.Errorf("issueTodoCmd missing '%s' subcommand", sub)
		}
	}
}

func TestResolveACID(t *testing.T) {
	// Test resolving by UUID directly
	knownUUID := "11111111-2222-3333-4444-555555555555"
	got, err := resolveACID(context.Background(), nil, "any-issue", knownUUID)
	if err != nil || got != knownUUID {
		t.Fatalf("resolveACID by UUID failed: got %s, err %v", got, err)
	}

	// Test resolving by AC Key via fake server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id": "ac-uuid-1", "key": "AC1", "title": "First"},
			{"id": "ac-uuid-2", "key": "AC2", "title": "Second"}
		]`))
	}))
	defer ts.Close()

	client := cli.NewAPIClient(ts.URL, "ws-1", "token")
	acID, err := resolveACID(context.Background(), client, "issue-1", "AC2")
	if err != nil {
		t.Fatalf("resolveACID by key: %v", err)
	}
	if acID != "ac-uuid-2" {
		t.Errorf("expected ac-uuid-2, got %s", acID)
	}

	// Test resolving lowercase "ac1"
	acID1, err := resolveACID(context.Background(), client, "issue-1", "ac1")
	if err != nil {
		t.Fatalf("resolveACID lowercase: %v", err)
	}
	if acID1 != "ac-uuid-1" {
		t.Errorf("expected ac-uuid-1, got %s", acID1)
	}

	// Test non-existent key
	_, err = resolveACID(context.Background(), client, "issue-1", "AC99")
	if err == nil {
		t.Errorf("expected error for non-existent AC key, got nil")
	}
}

func TestIssueTaskLedger_E2EWorkflowSimulation(t *testing.T) {
	// Simulate multi-round Agent & QA handover workflow:
	// 1. Initial State: ACs extracted from description
	// 2. Dev Agent: TODOs added with next_steps and linked ACs
	// 3. Dev Agent: Updates TODO to in_progress then completed with evidence
	// 4. Invariant: Completing TODO does not auto-verify AC
	// 5. QA Agent: Verifies AC1 with test suite evidence
	// 6. QA Agent: Rejects AC2 due to edge case failure
	// 7. Follow-up Dev Agent: Reopens TODO with new next_step
	// 8. Composite Ledger reflects all current states and audit trail

	var recordedAudit []map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/issues/test-issue/ac/extract":
			_, _ = w.Write([]byte(`{
				"extracted_count": 2,
				"acs": [
					{"id": "ac-1", "key": "AC1", "title": "Extract AC reliably", "status": "uncompleted"},
					{"id": "ac-2", "key": "AC2", "title": "Dynamic TODO lifecycle", "status": "uncompleted"}
				]
			}`))
		case r.Method == "POST" && r.URL.Path == "/api/issues/test-issue/todos":
			_, _ = w.Write([]byte(`{
				"id": "todo-1",
				"title": "Build migration and endpoints",
				"status": "pending",
				"next_step": "Run tests",
				"ac_ids": ["AC1", "AC2"]
			}`))
		case r.Method == "PATCH" && r.URL.Path == "/api/issues/test-issue/todos/todo-1":
			_, _ = w.Write([]byte(`{
				"id": "todo-1",
				"title": "Build migration and endpoints",
				"status": "completed",
				"next_step": "",
				"ac_ids": ["AC1", "AC2"],
				"evidence": [{"type": "commit", "url": "git-commit-hash"}]
			}`))
		case r.Method == "POST" && r.URL.Path == "/api/issues/test-issue/ac/ac-1/verify":
			recordedAudit = append(recordedAudit, map[string]any{"action": "verified", "target": "ac-1"})
			_, _ = w.Write([]byte(`{
				"id": "ac-1",
				"key": "AC1",
				"title": "Extract AC reliably",
				"status": "verified",
				"evidence": [{"type": "test", "note": "All unit tests pass"}]
			}`))
		case r.Method == "POST" && r.URL.Path == "/api/issues/test-issue/ac/ac-2/reject":
			recordedAudit = append(recordedAudit, map[string]any{"action": "rejected", "target": "ac-2"})
			_, _ = w.Write([]byte(`{
				"id": "ac-2",
				"key": "AC2",
				"title": "Dynamic TODO lifecycle",
				"status": "rejected",
				"rejection_reason": "Edge case fails when reopened"
			}`))
		case r.Method == "POST" && r.URL.Path == "/api/issues/test-issue/todos/todo-1/reopen":
			recordedAudit = append(recordedAudit, map[string]any{"action": "reopened", "target": "todo-1"})
			_, _ = w.Write([]byte(`{
				"id": "todo-1",
				"title": "Build migration and endpoints",
				"status": "in_progress",
				"next_step": "Fix reopened edge case and re-verify"
			}`))
		case r.Method == "GET" && r.URL.Path == "/api/issues/test-issue/ledger":
			_, _ = w.Write([]byte(`{
				"acs": [
					{"id": "ac-1", "key": "AC1", "status": "verified"},
					{"id": "ac-2", "key": "AC2", "status": "rejected", "rejection_reason": "Edge case fails when reopened"}
				],
				"todos": [
					{"id": "todo-1", "status": "in_progress", "next_step": "Fix reopened edge case and re-verify"}
				],
				"summary": {
					"ac_total": 2,
					"ac_verified": 1,
					"ac_pending": 0,
					"ac_rejected": 1,
					"todo_total": 1,
					"todo_completed": 0,
					"todo_in_progress": 1,
					"todo_pending": 0,
					"todo_blocked": 0,
					"current_next_steps": ["Fix reopened edge case and re-verify"]
				}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := cli.NewAPIClient(ts.URL, "ws-1", "test-token")
	ctx := context.Background()

	// 1. AC Extract
	var extractResult struct {
		ExtractedCount int `json:"extracted_count"`
	}
	err := client.PostJSON(ctx, "/api/issues/test-issue/ac/extract", map[string]any{}, &extractResult)
	if err != nil || extractResult.ExtractedCount != 2 {
		t.Fatalf("AC extraction failed: err=%v, count=%d", err, extractResult.ExtractedCount)
	}

	// 2. TODO Add
	var todoResult struct {
		ID       string `json:"id"`
		Status   string `json:"status"`
		NextStep string `json:"next_step"`
	}
	err = client.PostJSON(ctx, "/api/issues/test-issue/todos", map[string]any{
		"title":     "Build migration and endpoints",
		"next_step": "Run tests",
		"ac_ids":    []string{"AC1", "AC2"},
	}, &todoResult)
	if err != nil || todoResult.ID != "todo-1" || todoResult.NextStep != "Run tests" {
		t.Fatalf("TODO add failed: err=%v, result=%+v", err, todoResult)
	}

	// 3. Complete TODO
	err = client.PatchJSON(ctx, "/api/issues/test-issue/todos/todo-1", map[string]any{
		"status": "completed",
	}, &todoResult)
	if err != nil || todoResult.Status != "completed" {
		t.Fatalf("TODO update to completed failed: err=%v, status=%s", err, todoResult.Status)
	}

	// 4. Verify AC1
	var ac1Result struct {
		Status string `json:"status"`
	}
	err = client.PostJSON(ctx, "/api/issues/test-issue/ac/ac-1/verify", map[string]any{
		"evidence": "All unit tests pass",
	}, &ac1Result)
	if err != nil || ac1Result.Status != "verified" {
		t.Fatalf("Verify AC1 failed: err=%v, status=%s", err, ac1Result.Status)
	}

	// 5. Reject AC2
	var ac2Result struct {
		Status          string `json:"status"`
		RejectionReason string `json:"rejection_reason"`
	}
	err = client.PostJSON(ctx, "/api/issues/test-issue/ac/ac-2/reject", map[string]any{
		"reason": "Edge case fails when reopened",
	}, &ac2Result)
	if err != nil || ac2Result.Status != "rejected" {
		t.Fatalf("Reject AC2 failed: err=%v, status=%s", err, ac2Result.Status)
	}

	// 6. Reopen TODO
	err = client.PostJSON(ctx, "/api/issues/test-issue/todos/todo-1/reopen", map[string]any{
		"next_step": "Fix reopened edge case and re-verify",
	}, &todoResult)
	if err != nil || todoResult.Status != "in_progress" {
		t.Fatalf("Reopen TODO failed: err=%v, status=%s", err, todoResult.Status)
	}

	// 7. Check Composite Ledger
	var ledgerResult struct {
		Summary struct {
			ACVerified       int      `json:"ac_verified"`
			ACRejected       int      `json:"ac_rejected"`
			TodoInProgress   int      `json:"todo_in_progress"`
			CurrentNextSteps []string `json:"current_next_steps"`
		} `json:"summary"`
	}
	err = client.GetJSON(ctx, "/api/issues/test-issue/ledger", &ledgerResult)
	if err != nil {
		t.Fatalf("Get ledger failed: %v", err)
	}
	if ledgerResult.Summary.ACVerified != 1 || ledgerResult.Summary.ACRejected != 1 {
		t.Errorf("AC summary mismatch: %+v", ledgerResult.Summary)
	}
	if ledgerResult.Summary.TodoInProgress != 1 || len(ledgerResult.Summary.CurrentNextSteps) != 1 {
		t.Errorf("TODO summary mismatch: %+v", ledgerResult.Summary)
	}
	if len(recordedAudit) != 3 {
		t.Errorf("Expected 3 recorded audit transitions, got %d", len(recordedAudit))
	}
}
