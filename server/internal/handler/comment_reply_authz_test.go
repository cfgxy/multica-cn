package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestTaskCoversReplyParent pins the comment-reply authorization allow-list
// (MUL-4348): a comment-triggered task may reply under its trigger comment OR
// under any earlier comment it coalesced, and nothing else. This is what lets
// a coalesced cross-thread run answer each thread in its own thread instead of
// being rejected with "parent_id must equal this task's trigger comment id".
func TestTaskCoversReplyParent(t *testing.T) {
	trigger := "11111111-1111-1111-1111-111111111111"
	c1 := "22222222-2222-2222-2222-222222222222"
	c2 := "33333333-3333-3333-3333-333333333333"
	stranger := "99999999-9999-9999-9999-999999999999"

	task := db.AgentTaskQueue{
		TriggerCommentID: util.MustParseUUID(trigger),
		CoalescedCommentIds: []pgtype.UUID{
			util.MustParseUUID(c1),
			util.MustParseUUID(c2),
		},
	}

	cases := []struct {
		name   string
		parent pgtype.UUID
		want   bool
	}{
		{"trigger comment allowed", util.MustParseUUID(trigger), true},
		{"first coalesced comment allowed", util.MustParseUUID(c1), true},
		{"second coalesced comment allowed", util.MustParseUUID(c2), true},
		{"unrelated comment rejected", util.MustParseUUID(stranger), false},
		{"invalid/empty parent rejected", pgtype.UUID{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := taskCoversReplyParent(task, tc.parent); got != tc.want {
				t.Errorf("taskCoversReplyParent(%s) = %v, want %v", uuidToString(tc.parent), got, tc.want)
			}
		})
	}

	// An assignment-triggered task (no trigger comment, no coalesced set) covers
	// nothing here; the caller only consults this when TriggerCommentID is valid.
	empty := db.AgentTaskQueue{}
	if taskCoversReplyParent(empty, util.MustParseUUID(trigger)) {
		t.Errorf("a task with no trigger/coalesced comments must not authorize any parent")
	}
}

func TestTaskBoundAgentMayReplyWithoutResponsibilityMention(t *testing.T) {
	issueID := util.MustParseUUID("11111111-1111-1111-1111-111111111111")
	agentID := util.MustParseUUID("22222222-2222-2222-2222-222222222222")
	triggerID := util.MustParseUUID("33333333-3333-3333-3333-333333333333")
	coalescedID := util.MustParseUUID("44444444-4444-4444-4444-444444444444")
	otherID := util.MustParseUUID("55555555-5555-5555-5555-555555555555")
	task := db.AgentTaskQueue{
		IssueID:             issueID,
		AgentID:             agentID,
		Status:              "running",
		TriggerCommentID:    triggerID,
		CoalescedCommentIds: []pgtype.UUID{coalescedID},
	}
	issue := db.Issue{ID: issueID}
	actorID := uuidToString(agentID)

	for _, tc := range []struct {
		name   string
		parent *db.Comment
		mutate func(*db.AgentTaskQueue)
		want   bool
	}{
		{"trigger parent", &db.Comment{ID: triggerID}, nil, true},
		{"coalesced parent", &db.Comment{ID: coalescedID}, nil, true},
		{"unrelated parent", &db.Comment{ID: otherID}, nil, false},
		{"missing parent", nil, nil, false},
		{"wrong issue", &db.Comment{ID: triggerID}, func(t *db.AgentTaskQueue) { t.IssueID = otherID }, false},
		{"wrong agent", &db.Comment{ID: triggerID}, func(t *db.AgentTaskQueue) { t.AgentID = otherID }, false},
		{"terminal task", &db.Comment{ID: triggerID}, func(t *db.AgentTaskQueue) { t.Status = "completed" }, false},
		{"no action task", &db.Comment{ID: triggerID}, func(t *db.AgentTaskQueue) { t.NoActionAt = pgtype.Timestamptz{Valid: true} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := task
			if tc.mutate != nil {
				tc.mutate(&candidate)
			}
			if got := taskBoundAgentMayReplyWithoutResponsibilityMention(issue, tc.parent, actorID, &candidate); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}

	assignment := task
	assignment.TriggerCommentID = pgtype.UUID{}
	assignment.CoalescedCommentIds = nil
	if !taskBoundAgentMayReplyWithoutResponsibilityMention(issue, nil, actorID, &assignment) {
		t.Fatal("assignment-triggered task should allow a top-level terminal comment")
	}
	if taskBoundAgentMayReplyWithoutResponsibilityMention(issue, &db.Comment{ID: triggerID}, actorID, &assignment) {
		t.Fatal("assignment-triggered task must not claim an unrelated thread")
	}
}
