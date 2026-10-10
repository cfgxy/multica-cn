package main

// RUYI-648 regression: the issue:updated listener mirrors a priority change
// into audit_event as issue.priority_changed (RUYI-355 Phase 1 dual-write).
// The audit timeline is the only queryable record of WHEN an issue's priority
// moved — queue rows carry only the replayed integer, never the transition —
// so this pins the mirror against silent regression.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestActivityIssuePriorityChangedMirrorsAuditEvent(t *testing.T) {
	queries := db.New(testPool)
	bus := events.New()
	registerActivityListeners(bus, queries)

	issueID := createTestIssue(t, testWorkspaceID, testUserID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM audit_event WHERE issue_id = $1`, issueID)
		cleanupActivities(t, issueID)
		cleanupTestIssue(t, issueID)
	})

	bus.Publish(events.Event{
		Type:        protocol.EventIssueUpdated,
		WorkspaceID: testWorkspaceID,
		ActorType:   "member",
		ActorID:     testUserID,
		Payload: map[string]any{
			"issue": handler.IssueResponse{
				ID:          issueID,
				WorkspaceID: testWorkspaceID,
				Title:       "priority audit issue",
				Status:      "todo",
				Priority:    "urgent",
				CreatorType: "member",
				CreatorID:   testUserID,
			},
			"priority_changed": true,
			"prev_priority":    "none",
		},
	})

	var details []byte
	err := testPool.QueryRow(context.Background(), `
		SELECT details FROM audit_event
		WHERE issue_id = $1 AND event_type = 'issue.priority_changed'
		ORDER BY occurred_at DESC LIMIT 1`, issueID).Scan(&details)
	if err != nil {
		t.Fatalf("no issue.priority_changed audit event written: %v", err)
	}
	var parsed map[string]string
	if err := json.Unmarshal(details, &parsed); err != nil {
		t.Fatalf("audit details not JSON: %v", err)
	}
	if parsed["from"] != "none" || parsed["to"] != "urgent" {
		t.Errorf("audit details = %v, want from=none to=urgent", parsed)
	}
}
