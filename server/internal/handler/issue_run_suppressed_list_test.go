package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// seedSuppressedBadgeListFixture creates a dedicated project holding exactly
// two issues — one on hold (run_suppressed=true via the real assign write)
// and one ordinary — so both list read paths can be asserted against SQL
// truth without shared-workspace noise.
func seedSuppressedBadgeListFixture(t *testing.T) (projectID, heldIssueID, plainIssueID string) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO project (workspace_id, title)
		VALUES ($1, $2)
		RETURNING id
	`, testWorkspaceID, fmt.Sprintf("run-suppressed badge %d", time.Now().UnixNano())).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE project_id = $1`, projectID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID)
	})

	held := createIssueForTest(t, map[string]any{
		"title": "badge-held", "status": "todo", "project_id": projectID,
	})
	placeHold(t, held.ID, seededReadyAgentID(t))

	plain := createIssueForTest(t, map[string]any{
		"title": "badge-plain", "status": "todo", "project_id": projectID,
	})
	return projectID, held.ID, plain.ID
}

// The GET /api/issues read path must feed the run_suppressed snapshot from
// the SQL row into the response: true + timestamp for the held issue, false +
// null for the plain one. Scanning the response constructor is not enough —
// this locks the SELECT column list itself (RUYI-275 QA FAIL).
func TestIssueListReturnsRunSuppressedSnapshot(t *testing.T) {
	projectID, heldID, plainID := seedSuppressedBadgeListFixture(t)

	w := httptest.NewRecorder()
	testHandler.ListIssues(w, newRequest("GET", fmt.Sprintf(
		"/api/issues?workspace_id=%s&project_id=%s&limit=500", testWorkspaceID, projectID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListIssues: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Issues []IssueResponse `json:"issues"`
		Total  int64           `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(resp.Issues) != 2 || resp.Total != 2 {
		t.Fatalf("dedicated project list = %d rows (total %d), want 2", len(resp.Issues), resp.Total)
	}

	byID := map[string]IssueResponse{}
	for _, iss := range resp.Issues {
		byID[iss.ID] = iss
	}
	held, ok := byID[heldID]
	if !ok {
		t.Fatalf("held issue %s missing from list", heldID)
	}
	if !held.RunSuppressed || held.RunSuppressedAt == nil {
		t.Fatalf("GET /api/issues lost the hold on %s: run_suppressed=%v run_suppressed_at=%v",
			heldID, held.RunSuppressed, held.RunSuppressedAt)
	}
	plain, ok := byID[plainID]
	if !ok {
		t.Fatalf("plain issue %s missing from list", plainID)
	}
	if plain.RunSuppressed || plain.RunSuppressedAt != nil {
		t.Fatalf("GET /api/issues invented a hold on %s: run_suppressed=%v run_suppressed_at=%v",
			plainID, plain.RunSuppressed, plain.RunSuppressedAt)
	}
}

// Same contract for POST /api/issues/table/rows — the board/table surface the
// badge lives on. The inner scannedRow must scan both snapshot columns, not
// just carry zero values into the shared response constructor.
func TestIssueTableRowsReturnRunSuppressedSnapshot(t *testing.T) {
	projectID, heldID, plainID := seedSuppressedBadgeListFixture(t)

	w := httptest.NewRecorder()
	testHandler.ListIssueTableRows(w, newRequest(http.MethodPost, "/api/issues/table/rows", issueTableRowsRequest{
		Query: issueTableQuerySpec{
			Scope:   issueTableScope{Kind: "project", ProjectID: projectID},
			Filters: issueTableFiltersRequest{},
			Sort:    issueTableSortRequest{Field: "title", Direction: "asc"},
		},
		Group: issueTableGroupSpec{Kind: "none"},
		Page:  issueTablePageRequest{Limit: 50},
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("ListIssueTableRows: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var rows issueTableRowsResponse
	if err := json.NewDecoder(w.Body).Decode(&rows); err != nil {
		t.Fatalf("decode rows response: %v", err)
	}
	if len(rows.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows.Rows))
	}

	byID := map[string]IssueResponse{}
	for _, row := range rows.Rows {
		byID[row.Issue.ID] = row.Issue
	}
	held, ok := byID[heldID]
	if !ok {
		t.Fatalf("held issue %s missing from table rows", heldID)
	}
	if !held.RunSuppressed || held.RunSuppressedAt == nil {
		t.Fatalf("table rows lost the hold on %s: run_suppressed=%v run_suppressed_at=%v",
			heldID, held.RunSuppressed, held.RunSuppressedAt)
	}
	plain, ok := byID[plainID]
	if !ok {
		t.Fatalf("plain issue %s missing from table rows", plainID)
	}
	if plain.RunSuppressed || plain.RunSuppressedAt != nil {
		t.Fatalf("table rows invented a hold on %s: run_suppressed=%v run_suppressed_at=%v",
			plainID, plain.RunSuppressed, plain.RunSuppressedAt)
	}
}
