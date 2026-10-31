package handler

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

type invalidMentionResponse struct {
	Code            string `json:"code"`
	Error           string `json:"error"`
	InvalidMentions []struct {
		Start int `json:"start"`
		End   int `json:"end"`
	} `json:"invalid_mentions"`
}

func commentRequest(method, issueID, commentID string, body any) *http.Request {
	req := testutil.WithHeaders(testutil.JSONRequest(method, "/api/issues/"+issueID+"/comments", body), "X-User-ID", testUserID, "X-Workspace-ID", testWorkspaceID)
	if method == http.MethodPut {
		req.URL.Path = "/api/comments/" + commentID
		return testutil.WithURLParams(req, "commentId", commentID)
	}
	return testutil.WithURLParams(req, "id", issueID)
}

func TestCommentMentionAdmissionRejectsInvalidTargetsBeforeWrites(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "admission-runtime")
	allowedID := dbfx.Agent(t, "admission-allowed", runtimeID)
	privateOwner := dbfx.User(t, "admission-private-owner", "admission-private-owner@example.test")
	privateID := dbfx.Agent(t, "admission-private", runtimeID, testutil.Cols{"owner_id": privateOwner})
	archivedID := dbfx.Agent(t, "admission-archived", runtimeID, testutil.Cols{"archived_at": testutil.Raw("now()")})
	unknownID := "00000000-0000-0000-0000-0000000000ff"
	issueID := dbfx.Issue(t, "mention admission failure")
	old := dbfx.Comment(t, issueID, "original body")
	taskID := dbfx.Task(t, allowedID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID, "trigger_comment_id": old})

	var revision int64
	var activity string
	dbfx.QueryRow(t, `SELECT revision, COALESCE(last_activity_at::text, '') FROM issue WHERE id = $1`, issueID).Scan(&revision, &activity)
	var oldRevision int64
	dbfx.QueryRow(t, `SELECT revision FROM comment WHERE id = $1`, old).Scan(&oldRevision)

	cases := []struct {
		name, invalidID string
	}{
		{"unknown", unknownID},
		{"private", privateID},
		{"archived", archivedID},
		{"malformed", "not-a-uuid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, method := range []string{http.MethodPost, http.MethodPut} {
				t.Run(method, func(t *testing.T) {
					// The first mention is valid but suppressed; the second is not.
					content := fmt.Sprintf("你好 [@OK](mention://agent/%s) [@Bad](mention://agent/%s)", allowedID, tc.invalidID)
					body := map[string]any{"content": content, "suppress_agent_ids": []string{allowedID}}
					var result invalidMentionResponse
					h := testHandler.CreateComment
					if method == http.MethodPut {
						h = testHandler.UpdateComment
					}
					testutil.Call(t, h, commentRequest(method, issueID, old, body)).Want(http.StatusUnprocessableEntity).JSON(&result)
					start := strings.Index(content, "[@Bad]")
					if result.Code != "invalid_agent_mentions" || len(result.InvalidMentions) != 1 || result.InvalidMentions[0].Start != start || result.InvalidMentions[0].End != len(content) {
						t.Fatalf("%s response = %+v, want invalid byte span [%d,%d)", tc.name, result, start, len(content))
					}
					if strings.Contains(result.Error, tc.invalidID) || strings.Contains(result.Error, "private") {
						t.Fatalf("error reveals target details: %s", result.Error)
					}
				})
			}
		})
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM comment WHERE issue_id = $1`, issueID); got != 1 {
		t.Fatalf("comments = %d, want original only", got)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id = $1`, issueID); got != 1 {
		t.Fatalf("tasks = %d, want original only", got)
	}
	var content, status string
	var currentRevision int64
	dbfx.QueryRow(t, `SELECT content, revision FROM comment WHERE id = $1`, old).Scan(&content, &currentRevision)
	dbfx.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id = $1`, taskID).Scan(&status)
	if content != "original body" || currentRevision != oldRevision || status != "queued" {
		t.Fatalf("old comment/task changed: %q, revision %d, task %q", content, currentRevision, status)
	}
	var newRevision int64
	var newActivity string
	dbfx.QueryRow(t, `SELECT revision, COALESCE(last_activity_at::text, '') FROM issue WHERE id = $1`, issueID).Scan(&newRevision, &newActivity)
	if newRevision != revision || newActivity != activity {
		t.Fatalf("issue touched: revision %d -> %d, activity %s -> %s", revision, newRevision, activity, newActivity)
	}
}

func TestAgentCommentsRequireWorkspaceResponsibilityMention(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "responsibility-mention-runtime")
	agentID := dbfx.Agent(t, "responsibility-mention-author", runtimeID)
	targetID := createHandlerTestAgent(t, "responsibility-mention-target", nil)
	issueID := dbfx.Issue(t, "agent responsibility mention")
	parentID := dbfx.Comment(t, issueID, "please take this")
	taskID := dbfx.Task(t, agentID, testutil.Cols{
		"runtime_id": runtimeID, "issue_id": issueID, "trigger_comment_id": parentID,
		"status": "running", "started_at": testutil.Raw("now()"),
	})

	request := func(method, commentID, content string) *http.Request {
		req := commentRequest(method, issueID, commentID, map[string]any{
			"content": content, "parent_id": parentID,
		})
		req.Header.Set("X-Agent-ID", agentID)
		req.Header.Set("X-Task-ID", taskID)
		return req
	}

	bad := testutil.Call(t, testHandler.CreateComment, request(http.MethodPost, "", "Progress is complete."))
	bad.Want(http.StatusUnprocessableEntity)
	if got := dbfx.Count(t, `SELECT count(*) FROM comment WHERE issue_id = $1 AND author_type = 'agent'`, issueID); got != 0 {
		t.Fatalf("comment without responsibility mention was stored: count=%d", got)
	}

	validContent := fmt.Sprintf("Progress is complete; handing off to [@Target](mention://agent/%s).", targetID)
	var created map[string]any
	testutil.Call(t, testHandler.CreateComment, request(http.MethodPost, "", validContent)).Want(http.StatusCreated).JSON(&created)
	commentID, _ := created["id"].(string)
	if commentID == "" {
		t.Fatalf("created comment has no id: %+v", created)
	}
	testutil.Call(t, testHandler.UpdateComment, request(http.MethodPut, commentID, "Edited without a responsibility mention.")).Want(http.StatusUnprocessableEntity)
	var content string
	dbfx.QueryRow(t, `SELECT content FROM comment WHERE id = $1`, commentID).Scan(&content)
	if content != validContent {
		t.Fatalf("rejected edit changed comment to %q", content)
	}
}

func TestAgentResponsibilityMentionUsesCurrentIssueSquadAssignment(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := handlerTestRuntimeID(t)
	leaderID := createHandlerTestAgent(t, "responsibility-squad-leader", nil)
	workerID := createHandlerTestAgent(t, "responsibility-squad-worker", nil)
	otherID := createHandlerTestAgent(t, "responsibility-squad-other", nil)
	outsiderID := createHandlerTestAgent(t, "responsibility-squad-outsider", nil)
	squadID := dbfx.Squad(t, "responsibility assignment squad", leaderID)
	dbfx.Exec(t, `
		INSERT INTO squad_member (squad_id, member_type, member_id, role)
		VALUES ($1, 'agent', $2, 'worker')
	`, squadID, workerID)
	issueID := dbfx.Issue(t, "squad responsibility mention", testutil.Cols{
		"assignee_type": "squad",
		"assignee_id":   squadID,
	})
	parentID := dbfx.Comment(t, issueID, "coordinate this work")

	newTask := func(agentID string) string {
		return dbfx.Task(t, agentID, testutil.Cols{
			"runtime_id": runtimeID, "issue_id": issueID, "trigger_comment_id": parentID,
			"status": "running", "started_at": testutil.Raw("now()"),
		})
	}
	request := func(method, agentID, taskID, commentID, content string) *http.Request {
		req := commentRequest(method, issueID, commentID, map[string]any{
			"content": content, "parent_id": parentID,
		})
		req.Header.Set("X-Agent-ID", agentID)
		req.Header.Set("X-Task-ID", taskID)
		return req
	}
	assertRejected := func(method, agentID, taskID, commentID, content, wantError string) {
		t.Helper()
		var got map[string]string
		h := testHandler.CreateComment
		if method == http.MethodPut {
			h = testHandler.UpdateComment
		}
		testutil.Call(t, h, request(method, agentID, taskID, commentID, content)).Want(http.StatusUnprocessableEntity).JSON(&got)
		if got["error"] != wantError || got["code"] != "invalid_agent_comment_mention" || got["agent_comment_mention_policy"] != agentCommentMentionPolicy {
			t.Fatalf("responsibility mention rejection = %+v", got)
		}
	}

	workerTaskID := newTask(workerID)
	leaderMention := fmt.Sprintf("[@Leader](mention://agent/%s)", leaderID)
	otherMention := fmt.Sprintf("[@Other](mention://agent/%s)", otherID)
	assertRejected(http.MethodPost, workerID, workerTaskID, "", leaderMention+" "+otherMention, "agent comments may mention only one valid responsibility agent")
	assertRejected(http.MethodPost, workerID, workerTaskID, "", otherMention, "non-leader members of the issue's assigned squad must mention that squad's leader")
	assertRejected(http.MethodPost, workerID, workerTaskID, "", "handoff to "+leaderID, "agent comments must mention one valid responsibility agent")

	var workerComment CommentResponse
	testutil.Call(t, testHandler.CreateComment, request(http.MethodPost, workerID, workerTaskID, "", leaderMention)).Want(http.StatusCreated).JSON(&workerComment)
	assertRejected(http.MethodPut, workerID, workerTaskID, workerComment.ID, leaderMention+" "+otherMention, "agent comments may mention only one valid responsibility agent")

	leaderTaskID := newTask(leaderID)
	testutil.Call(t, testHandler.CreateComment, request(http.MethodPost, leaderID, leaderTaskID, "", otherMention)).Want(http.StatusCreated)

	outsiderTaskID := newTask(outsiderID)
	testutil.Call(t, testHandler.CreateComment, request(http.MethodPost, outsiderID, outsiderTaskID, "", otherMention)).Want(http.StatusCreated)
}

func TestAgentCommentSelfMentionGateRejectsBeforeWrites(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "self-mention-gate-runtime")
	authorID := dbfx.Agent(t, "self-mention-gate-author", runtimeID)
	otherID := dbfx.Agent(t, "self-mention-gate-other", runtimeID)
	issueID := dbfx.Issue(t, "agent self mention gate")
	commentID := dbfx.Comment(t, issueID, "original body")
	taskID := dbfx.Task(t, authorID, testutil.Cols{
		"runtime_id":         runtimeID,
		"issue_id":           issueID,
		"originator_user_id": testUserID,
		"status":             "running",
		"started_at":         testutil.Raw("now()"),
	})

	selfContent := fmt.Sprintf("[@负责人](mention://agent/%s) [@另一个别名](mention://agent/%s)", strings.ToUpper(authorID), authorID)
	request := func(method, targetCommentID, content string) *http.Request {
		req := commentRequest(method, issueID, targetCommentID, map[string]any{"content": content})
		req.Header.Set("X-Agent-ID", authorID)
		req.Header.Set("X-Task-ID", taskID)
		return req
	}

	for _, tc := range []struct {
		name, method, targetCommentID string
	}{
		{"create", http.MethodPost, ""},
		{"content edit", http.MethodPut, commentID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var result map[string]string
			h := testHandler.CreateComment
			if tc.method == http.MethodPut {
				h = testHandler.UpdateComment
			}
			testutil.Call(t, h, request(tc.method, tc.targetCommentID, selfContent)).Want(http.StatusUnprocessableEntity).JSON(&result)
			if result["error"] != "agents cannot mention themselves in comments" {
				t.Fatalf("self-mention rejection = %+v", result)
			}
		})
	}
	var noteResult map[string]string
	noteSelfContent := "/note " + selfContent
	testutil.Call(t, testHandler.CreateComment, request(http.MethodPost, "", noteSelfContent)).Want(http.StatusUnprocessableEntity).JSON(&noteResult)
	if noteResult["error"] != "agents cannot mention themselves in comments" || noteResult["code"] != "invalid_agent_comment_mention" || noteResult["agent_comment_mention_policy"] != agentCommentMentionPolicy {
		t.Fatalf("note self-mention rejection = %+v", noteResult)
	}

	if got := dbfx.Count(t, `SELECT count(*) FROM comment WHERE issue_id = $1`, issueID); got != 1 {
		t.Fatalf("comments = %d, want original only", got)
	}
	var content string
	dbfx.QueryRow(t, `SELECT content FROM comment WHERE id = $1`, commentID).Scan(&content)
	if content != "original body" {
		t.Fatalf("rejected edit changed comment to %q", content)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2`, issueID, authorID); got != 1 {
		t.Fatalf("author tasks = %d, want only the running author task", got)
	}

	allowedContent := fmt.Sprintf("[@%s](mention://member/%s) [@负责人](mention://agent/%s)", "Member", testUserID, otherID)
	var created CommentResponse
	testutil.Call(t, testHandler.CreateComment, request(http.MethodPost, "", allowedContent)).Want(http.StatusCreated).JSON(&created)
	if len(created.TriggerOutcomes) != 1 || created.TriggerOutcomes[0].TargetType != "agent" || created.TriggerOutcomes[0].TargetID != otherID {
		t.Fatalf("same-label cross-agent handoff = %+v", created.TriggerOutcomes)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2`, issueID, otherID); got != 1 {
		t.Fatalf("cross-agent handoff tasks = %d, want 1", got)
	}
}

func TestAssignedAgentCanReportWithoutResponsibilityMention(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "assigned-agent-reply-runtime")
	agentID := dbfx.Agent(t, "assigned-agent-reply-author", runtimeID)
	firstTargetID := dbfx.Agent(t, "assigned-agent-reply-first-target", dbfx.Runtime(t, "assigned-agent-reply-first-target-runtime"))
	secondTargetID := dbfx.Agent(t, "assigned-agent-reply-second-target", dbfx.Runtime(t, "assigned-agent-reply-second-target-runtime"))
	issueID := dbfx.Issue(t, "assigned agent can close member thread", testutil.Cols{
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	parentID := dbfx.Comment(t, issueID, "please complete this", testutil.Cols{
		"author_type": "member",
		"author_id":   testUserID,
	})
	taskID := dbfx.Task(t, agentID, testutil.Cols{
		"runtime_id": runtimeID, "issue_id": issueID, "trigger_comment_id": parentID,
		"status": "running", "started_at": testutil.Raw("now()"),
	})

	req := commentRequest(http.MethodPost, issueID, "", map[string]any{
		"content":   "The requested work is complete; no follow-up action is assigned.",
		"parent_id": parentID,
	})
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)
	var created CommentResponse
	testutil.Call(t, testHandler.CreateComment, req).Want(http.StatusCreated).JSON(&created)
	if len(created.TriggerOutcomes) != 0 {
		t.Fatalf("terminal reply triggered agents: %+v", created.TriggerOutcomes)
	}

	topLevelRequest := func(method, commentID, content string, attachments bool) *http.Request {
		body := map[string]any{"content": content}
		if attachments {
			body["attachment_ids"] = []string{}
		}
		req := commentRequest(method, issueID, commentID, body)
		req.Header.Set("X-Agent-ID", agentID)
		req.Header.Set("X-Task-ID", taskID)
		return req
	}
	var topLevel CommentResponse
	testutil.Call(t, testHandler.CreateComment,
		topLevelRequest(http.MethodPost, "", "The requested work is complete at the issue root.", false),
	).Want(http.StatusCreated).JSON(&topLevel)
	if len(topLevel.TriggerOutcomes) != 0 {
		t.Fatalf("top-level terminal report triggered agents: %+v", topLevel.TriggerOutcomes)
	}
	var topLevelUpdated CommentResponse
	testutil.Call(t, testHandler.UpdateComment,
		topLevelRequest(http.MethodPut, topLevel.ID, "The issue-root report was verified.", false),
	).Want(http.StatusOK).JSON(&topLevelUpdated)
	if len(topLevelUpdated.TriggerOutcomes) != 0 {
		t.Fatalf("top-level content edit triggered agents: %+v", topLevelUpdated.TriggerOutcomes)
	}
	var topLevelAttachmentUpdated CommentResponse
	testutil.Call(t, testHandler.UpdateComment,
		topLevelRequest(http.MethodPut, topLevel.ID, topLevelUpdated.Content, true),
	).Want(http.StatusOK).JSON(&topLevelAttachmentUpdated)
	if len(topLevelAttachmentUpdated.TriggerOutcomes) != 0 {
		t.Fatalf("top-level attachment edit triggered agents: %+v", topLevelAttachmentUpdated.TriggerOutcomes)
	}
	for _, issueStatus := range []string{"in_review", "blocked"} {
		t.Run("top-level "+issueStatus, func(t *testing.T) {
			statusIssueID := dbfx.Issue(t, "assigned agent top-level "+issueStatus, testutil.Cols{
				"assignee_type": "agent",
				"assignee_id":   agentID,
				"status":        issueStatus,
			})
			statusTaskID := dbfx.Task(t, agentID, testutil.Cols{
				"runtime_id": runtimeID, "issue_id": statusIssueID,
				"status": "running", "started_at": testutil.Raw("now()"),
			})
			statusRequest := commentRequest(http.MethodPost, statusIssueID, "", map[string]any{
				"content": "No further agent action is assigned.",
			})
			statusRequest.Header.Set("X-Agent-ID", agentID)
			statusRequest.Header.Set("X-Task-ID", statusTaskID)
			var statusCreated CommentResponse
			testutil.Call(t, testHandler.CreateComment, statusRequest).Want(http.StatusCreated).JSON(&statusCreated)
			if len(statusCreated.TriggerOutcomes) != 0 {
				t.Fatalf("%s top-level report triggered agents: %+v", issueStatus, statusCreated.TriggerOutcomes)
			}
		})
	}
	twoAgentMentions := fmt.Sprintf("[@First](mention://agent/%s) [@Second](mention://agent/%s)", firstTargetID, secondTargetID)
	assertTwoTargetRejection := func(t *testing.T, name string, handler http.HandlerFunc, req *http.Request) {
		t.Helper()
		var rejected map[string]string
		testutil.Call(t, handler, req).Want(http.StatusUnprocessableEntity).JSON(&rejected)
		if rejected["error"] != "agent comments may mention only one valid responsibility agent" || rejected["code"] != "invalid_agent_comment_mention" {
			t.Fatalf("%s two-target rejection = %+v", name, rejected)
		}
		for _, targetID := range []string{firstTargetID, secondTargetID} {
			if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2`, issueID, targetID); got != 0 {
				t.Fatalf("%s dispatched target %s: count=%d", name, targetID, got)
			}
		}
	}

	postWithTwoTargets := commentRequest(http.MethodPost, issueID, "", map[string]any{
		"content":   twoAgentMentions,
		"parent_id": parentID,
	})
	postWithTwoTargets.Header.Set("X-Agent-ID", agentID)
	postWithTwoTargets.Header.Set("X-Task-ID", taskID)
	assertTwoTargetRejection(t, "create", testHandler.CreateComment, postWithTwoTargets)
	if got := dbfx.Count(t, `SELECT count(*) FROM comment WHERE issue_id = $1`, issueID); got != 3 {
		t.Fatalf("rejected create stored comment: count=%d, want 3", got)
	}

	contentEdit := commentRequest(http.MethodPut, issueID, created.ID, map[string]any{
		"content": "The requested work is complete after verification.",
	})
	contentEdit.Header.Set("X-Agent-ID", agentID)
	contentEdit.Header.Set("X-Task-ID", taskID)
	var updated CommentResponse
	testutil.Call(t, testHandler.UpdateComment, contentEdit).Want(http.StatusOK).JSON(&updated)
	if updated.Content != "The requested work is complete after verification." || len(updated.TriggerOutcomes) != 0 {
		t.Fatalf("terminal content edit = %+v", updated)
	}
	twoTargetContentEdit := commentRequest(http.MethodPut, issueID, created.ID, map[string]any{
		"content": twoAgentMentions,
	})
	twoTargetContentEdit.Header.Set("X-Agent-ID", agentID)
	twoTargetContentEdit.Header.Set("X-Task-ID", taskID)
	assertTwoTargetRejection(t, "content edit", testHandler.UpdateComment, twoTargetContentEdit)

	twoTargetAttachmentEdit := commentRequest(http.MethodPut, issueID, created.ID, map[string]any{
		"content":        twoAgentMentions,
		"attachment_ids": []string{},
	})
	twoTargetAttachmentEdit.Header.Set("X-Agent-ID", agentID)
	twoTargetAttachmentEdit.Header.Set("X-Task-ID", taskID)
	assertTwoTargetRejection(t, "attachment edit", testHandler.UpdateComment, twoTargetAttachmentEdit)
	var storedContent string
	dbfx.QueryRow(t, `SELECT content FROM comment WHERE id = $1`, created.ID).Scan(&storedContent)
	if storedContent != updated.Content {
		t.Fatalf("rejected edit changed content to %q", storedContent)
	}

	attachmentEdit := commentRequest(http.MethodPut, issueID, created.ID, map[string]any{
		"content":        updated.Content,
		"attachment_ids": []string{},
	})
	attachmentEdit.Header.Set("X-Agent-ID", agentID)
	attachmentEdit.Header.Set("X-Task-ID", taskID)
	var attachmentUpdated CommentResponse
	testutil.Call(t, testHandler.UpdateComment, attachmentEdit).Want(http.StatusOK).JSON(&attachmentUpdated)
	if attachmentUpdated.Content != updated.Content || len(attachmentUpdated.TriggerOutcomes) != 0 {
		t.Fatalf("terminal attachment edit = %+v", attachmentUpdated)
	}

	singleTargetMention := fmt.Sprintf("[@First](mention://agent/%s)", firstTargetID)
	delegatingComment := commentRequest(http.MethodPost, issueID, "", map[string]any{
		"content":   singleTargetMention,
		"parent_id": parentID,
	})
	delegatingComment.Header.Set("X-Agent-ID", agentID)
	delegatingComment.Header.Set("X-Task-ID", taskID)
	var delegated CommentResponse
	testutil.Call(t, testHandler.CreateComment, delegatingComment).Want(http.StatusCreated).JSON(&delegated)
	if len(delegated.TriggerOutcomes) != 1 || delegated.TriggerOutcomes[0].TargetID != firstTargetID || delegated.TriggerOutcomes[0].Status != DispatchQueued {
		t.Fatalf("single-target delegation = %+v, want one queued task for %s", delegated.TriggerOutcomes, firstTargetID)
	}

	var queuedTaskID, queuedStatus string
	dbfx.QueryRow(t, `SELECT id, status FROM agent_task_queue WHERE trigger_comment_id = $1 AND agent_id = $2`, delegated.ID, firstTargetID).Scan(&queuedTaskID, &queuedStatus)
	if queuedStatus != "queued" {
		t.Fatalf("single-target task status = %q, want queued", queuedStatus)
	}

	legacyTwoTargetEdit := commentRequest(http.MethodPut, issueID, delegated.ID, map[string]any{
		"content": twoAgentMentions,
	})
	legacyTwoTargetEdit.Header.Set("X-Agent-ID", agentID)
	legacyTwoTargetEdit.Header.Set("X-Task-ID", taskID)
	var legacyRejected map[string]string
	testutil.Call(t, testHandler.UpdateComment, legacyTwoTargetEdit).Want(http.StatusUnprocessableEntity).JSON(&legacyRejected)
	if legacyRejected["error"] != "agent comments may mention only one valid responsibility agent" {
		t.Fatalf("legacy two-target rejection = %+v", legacyRejected)
	}

	var storedDelegatedContent string
	dbfx.QueryRow(t, `SELECT content FROM comment WHERE id = $1`, delegated.ID).Scan(&storedDelegatedContent)
	if storedDelegatedContent != singleTargetMention {
		t.Fatalf("rejected legacy edit changed content to %q", storedDelegatedContent)
	}
	dbfx.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id = $1`, queuedTaskID).Scan(&queuedStatus)
	if queuedStatus != "queued" {
		t.Fatalf("rejected legacy edit cancelled queued task: status=%q", queuedStatus)
	}
	var cancellationEvents int
	dbfx.QueryRow(t, `SELECT count(*) FROM audit_event WHERE task_id = $1 AND domain = 'run' AND event_type = 'run.cancelled'`, queuedTaskID).Scan(&cancellationEvents)
	if cancellationEvents != 0 {
		t.Fatalf("rejected legacy edit wrote %d run.cancelled audit events", cancellationEvents)
	}
}

func TestAssignedSquadLeaderCanReportWithoutResponsibilityMention(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "assigned-squad-leader-reply-runtime")
	leaderID := dbfx.Agent(t, "assigned-squad-leader-reply-leader", runtimeID)
	workerID := dbfx.Agent(t, "assigned-squad-leader-reply-worker", runtimeID)
	firstTargetID := dbfx.Agent(t, "assigned-squad-leader-reply-first-target", dbfx.Runtime(t, "assigned-squad-leader-reply-first-target-runtime"))
	secondTargetID := dbfx.Agent(t, "assigned-squad-leader-reply-second-target", dbfx.Runtime(t, "assigned-squad-leader-reply-second-target-runtime"))
	squadID := dbfx.Squad(t, "assigned squad leader terminal reply", leaderID)
	dbfx.Exec(t, `
		INSERT INTO squad_member (squad_id, member_type, member_id, role)
		VALUES ($1, 'agent', $2, 'worker')
	`, squadID, workerID)
	issueID := dbfx.Issue(t, "assigned squad leader can close member thread", testutil.Cols{
		"assignee_type": "squad",
		"assignee_id":   squadID,
	})
	parentID := dbfx.Comment(t, issueID, "please complete this", testutil.Cols{
		"author_type": "member",
		"author_id":   testUserID,
	})
	leaderTaskID := dbfx.Task(t, leaderID, testutil.Cols{
		"runtime_id": runtimeID, "issue_id": issueID, "trigger_comment_id": parentID,
		"status": "running", "started_at": testutil.Raw("now()"), "is_leader_task": true, "squad_id": squadID,
	})

	request := func(method, commentID, content string, attachments bool) *http.Request {
		body := map[string]any{"content": content}
		if method == http.MethodPost {
			body["parent_id"] = parentID
		}
		if attachments {
			body["attachment_ids"] = []string{}
		}
		req := commentRequest(method, issueID, commentID, body)
		req.Header.Set("X-Agent-ID", leaderID)
		req.Header.Set("X-Task-ID", leaderTaskID)
		return req
	}

	terminal := "The requested work is complete; no follow-up action is assigned."
	var created CommentResponse
	testutil.Call(t, testHandler.CreateComment, request(http.MethodPost, "", terminal, false)).Want(http.StatusCreated).JSON(&created)
	if len(created.TriggerOutcomes) != 0 {
		t.Fatalf("terminal leader reply triggered agents: %+v", created.TriggerOutcomes)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued'`, issueID, leaderID); got != 0 {
		t.Fatalf("terminal leader reply queued %d leader tasks", got)
	}

	topLevelRequest := func(method, commentID, content string, attachments bool) *http.Request {
		body := map[string]any{"content": content}
		if attachments {
			body["attachment_ids"] = []string{}
		}
		req := commentRequest(method, issueID, commentID, body)
		req.Header.Set("X-Agent-ID", leaderID)
		req.Header.Set("X-Task-ID", leaderTaskID)
		return req
	}
	var topLevel CommentResponse
	testutil.Call(t, testHandler.CreateComment,
		topLevelRequest(http.MethodPost, "", "The issue-root report is complete; no handoff is required.", false),
	).Want(http.StatusCreated).JSON(&topLevel)
	if len(topLevel.TriggerOutcomes) != 0 {
		t.Fatalf("top-level leader report triggered agents: %+v", topLevel.TriggerOutcomes)
	}
	var topLevelUpdated CommentResponse
	testutil.Call(t, testHandler.UpdateComment,
		topLevelRequest(http.MethodPut, topLevel.ID, "The issue-root report was verified.", false),
	).Want(http.StatusOK).JSON(&topLevelUpdated)
	if len(topLevelUpdated.TriggerOutcomes) != 0 {
		t.Fatalf("top-level leader content edit triggered agents: %+v", topLevelUpdated.TriggerOutcomes)
	}
	var topLevelAttachmentUpdated CommentResponse
	testutil.Call(t, testHandler.UpdateComment,
		topLevelRequest(http.MethodPut, topLevel.ID, topLevelUpdated.Content, true),
	).Want(http.StatusOK).JSON(&topLevelAttachmentUpdated)
	if len(topLevelAttachmentUpdated.TriggerOutcomes) != 0 {
		t.Fatalf("top-level leader attachment edit triggered agents: %+v", topLevelAttachmentUpdated.TriggerOutcomes)
	}
	for _, issueStatus := range []string{"in_review", "blocked"} {
		t.Run("top-level "+issueStatus, func(t *testing.T) {
			statusIssueID := dbfx.Issue(t, "assigned squad leader top-level "+issueStatus, testutil.Cols{
				"assignee_type": "squad",
				"assignee_id":   squadID,
				"status":        issueStatus,
			})
			statusTaskID := dbfx.Task(t, leaderID, testutil.Cols{
				"runtime_id": runtimeID, "issue_id": statusIssueID,
				"status": "running", "started_at": testutil.Raw("now()"), "is_leader_task": true, "squad_id": squadID,
			})
			statusRequest := commentRequest(http.MethodPost, statusIssueID, "", map[string]any{
				"content": "No further agent action is assigned.",
			})
			statusRequest.Header.Set("X-Agent-ID", leaderID)
			statusRequest.Header.Set("X-Task-ID", statusTaskID)
			var statusCreated CommentResponse
			testutil.Call(t, testHandler.CreateComment, statusRequest).Want(http.StatusCreated).JSON(&statusCreated)
			if len(statusCreated.TriggerOutcomes) != 0 {
				t.Fatalf("%s top-level leader report triggered agents: %+v", issueStatus, statusCreated.TriggerOutcomes)
			}
		})
	}

	updatedContent := "The requested work is complete after verification."
	var updated CommentResponse
	testutil.Call(t, testHandler.UpdateComment, request(http.MethodPut, created.ID, updatedContent, false)).Want(http.StatusOK).JSON(&updated)
	if updated.Content != updatedContent || len(updated.TriggerOutcomes) != 0 {
		t.Fatalf("terminal leader content edit = %+v", updated)
	}
	var attachmentUpdated CommentResponse
	testutil.Call(t, testHandler.UpdateComment, request(http.MethodPut, created.ID, updatedContent, true)).Want(http.StatusOK).JSON(&attachmentUpdated)
	if attachmentUpdated.Content != updatedContent || len(attachmentUpdated.TriggerOutcomes) != 0 {
		t.Fatalf("terminal leader attachment edit = %+v", attachmentUpdated)
	}

	workerParentID := dbfx.Comment(t, issueID, fmt.Sprintf("[@Leader](mention://agent/%s) work is complete", leaderID), testutil.Cols{
		"author_type": "agent",
		"author_id":   workerID,
	})
	leaderReplyToWorkerRequest := func(method, commentID, content string, attachments bool) *http.Request {
		body := map[string]any{"content": content}
		if method == http.MethodPost {
			body["parent_id"] = workerParentID
		}
		if attachments {
			body["attachment_ids"] = []string{}
		}
		req := commentRequest(method, issueID, commentID, body)
		req.Header.Set("X-Agent-ID", leaderID)
		req.Header.Set("X-Task-ID", leaderTaskID)
		return req
	}
	var workerReply CommentResponse
	testutil.Call(t, testHandler.CreateComment, leaderReplyToWorkerRequest(http.MethodPost, "", terminal, false)).Want(http.StatusCreated).JSON(&workerReply)
	if len(workerReply.TriggerOutcomes) != 0 {
		t.Fatalf("terminal leader-to-worker reply triggered agents: %+v", workerReply.TriggerOutcomes)
	}
	testutil.Call(t, testHandler.UpdateComment, leaderReplyToWorkerRequest(http.MethodPut, workerReply.ID, updatedContent, false)).Want(http.StatusOK).JSON(&updated)
	if updated.Content != updatedContent || len(updated.TriggerOutcomes) != 0 {
		t.Fatalf("terminal leader-to-worker content edit = %+v", updated)
	}
	testutil.Call(t, testHandler.UpdateComment, leaderReplyToWorkerRequest(http.MethodPut, workerReply.ID, updatedContent, true)).Want(http.StatusOK).JSON(&attachmentUpdated)
	if attachmentUpdated.Content != updatedContent || len(attachmentUpdated.TriggerOutcomes) != 0 {
		t.Fatalf("terminal leader-to-worker attachment edit = %+v", attachmentUpdated)
	}

	outsideParentAgentID := dbfx.Agent(t, "assigned-squad-leader-reply-outside-parent", runtimeID)
	outsideParentID := dbfx.Comment(t, issueID, "unrelated agent reply", testutil.Cols{
		"author_type": "agent",
		"author_id":   outsideParentAgentID,
	})
	outsideParentRequest := commentRequest(http.MethodPost, issueID, "", map[string]any{
		"content":   terminal,
		"parent_id": outsideParentID,
	})
	outsideParentRequest.Header.Set("X-Agent-ID", leaderID)
	outsideParentRequest.Header.Set("X-Task-ID", leaderTaskID)
	testutil.Call(t, testHandler.CreateComment, outsideParentRequest).Want(http.StatusUnprocessableEntity)

	dbfx.Exec(t, `
		INSERT INTO squad_member (squad_id, member_type, member_id, role)
		VALUES ($1, 'agent', $2, 'leader')
		ON CONFLICT (squad_id, member_type, member_id) DO NOTHING
	`, squadID, leaderID)
	leaderParentID := dbfx.Comment(t, issueID, "leader's earlier reply", testutil.Cols{
		"author_type": "agent",
		"author_id":   leaderID,
	})
	leaderParentRequest := commentRequest(http.MethodPost, issueID, "", map[string]any{
		"content":   terminal,
		"parent_id": leaderParentID,
	})
	leaderParentRequest.Header.Set("X-Agent-ID", leaderID)
	leaderParentRequest.Header.Set("X-Task-ID", leaderTaskID)
	testutil.Call(t, testHandler.CreateComment, leaderParentRequest).Want(http.StatusUnprocessableEntity)

	twoAgentMentions := fmt.Sprintf("[@First](mention://agent/%s) [@Second](mention://agent/%s)", firstTargetID, secondTargetID)
	assertTwoTargetRejection := func(t *testing.T, name string, handler http.HandlerFunc, req *http.Request) {
		t.Helper()
		var rejected map[string]string
		testutil.Call(t, handler, req).Want(http.StatusUnprocessableEntity).JSON(&rejected)
		if rejected["error"] != "agent comments may mention only one valid responsibility agent" || rejected["code"] != "invalid_agent_comment_mention" {
			t.Fatalf("%s two-target rejection = %+v", name, rejected)
		}
	}
	assertTwoTargetRejection(t, "create", testHandler.CreateComment, request(http.MethodPost, "", twoAgentMentions, false))
	assertTwoTargetRejection(t, "content edit", testHandler.UpdateComment, request(http.MethodPut, created.ID, twoAgentMentions, false))
	assertTwoTargetRejection(t, "attachment edit", testHandler.UpdateComment, request(http.MethodPut, created.ID, twoAgentMentions, true))
	selfMention := fmt.Sprintf("[@Leader](mention://agent/%s)", leaderID)
	var selfRejected map[string]string
	testutil.Call(t, testHandler.CreateComment, request(http.MethodPost, "", selfMention, false)).Want(http.StatusUnprocessableEntity).JSON(&selfRejected)
	if selfRejected["error"] != "agents cannot mention themselves in comments" {
		t.Fatalf("leader self-mention rejection = %+v", selfRejected)
	}

	workerTaskID := dbfx.Task(t, workerID, testutil.Cols{
		"runtime_id": runtimeID, "issue_id": issueID, "trigger_comment_id": parentID,
		"status": "running", "started_at": testutil.Raw("now()"), "squad_id": squadID,
	})
	workerRequest := func(content string) *http.Request {
		req := commentRequest(http.MethodPost, issueID, "", map[string]any{"content": content, "parent_id": parentID})
		req.Header.Set("X-Agent-ID", workerID)
		req.Header.Set("X-Task-ID", workerTaskID)
		return req
	}
	testutil.Call(t, testHandler.CreateComment, workerRequest("work is complete")).Want(http.StatusUnprocessableEntity)

	dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'completed' WHERE id = $1`, leaderTaskID)
	leaderMention := fmt.Sprintf("[@Leader](mention://agent/%s)", leaderID)
	var delegated CommentResponse
	testutil.Call(t, testHandler.CreateComment, workerRequest(leaderMention)).Want(http.StatusCreated).JSON(&delegated)
	if len(delegated.TriggerOutcomes) != 1 || delegated.TriggerOutcomes[0].TargetID != leaderID || delegated.TriggerOutcomes[0].Status != DispatchQueued {
		t.Fatalf("worker-to-leader handoff = %+v, want one queued task for %s", delegated.TriggerOutcomes, leaderID)
	}
}

func TestCommentMentionAdmissionNormalizedSpansAndPreview(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issueID := dbfx.Issue(t, "mention preview and offsets")
	allowed := dbfx.Agent(t, "mention preview allowed", dbfx.Runtime(t, "mention-preview-runtime"))
	bad := "[@Lost](mention://agent/00000000-0000-0000-0000-0000000000ff)"
	content := "前缀 [文档](https://example.test)\x00 " + bad + " " + bad
	normalized := strings.ReplaceAll(content, "\x00", "")

	var denied invalidMentionResponse
	testutil.Call(t, testHandler.CreateComment, commentRequest(http.MethodPost, issueID, "", map[string]any{"content": content})).Want(http.StatusUnprocessableEntity).JSON(&denied)
	first := strings.Index(normalized, bad)
	second := strings.LastIndex(normalized, bad)
	if len(denied.InvalidMentions) != 2 || denied.InvalidMentions[0].Start != first || denied.InvalidMentions[0].End != first+len(bad) || denied.InvalidMentions[1].Start != second || denied.InvalidMentions[1].End != second+len(bad) {
		t.Fatalf("normalized UTF-8 spans = %+v, want [%d,%d), [%d,%d)", denied.InvalidMentions, first, first+len(bad), second, second+len(bad))
	}

	mixed := fmt.Sprintf("[@Allowed](mention://agent/%s) %s", allowed, bad)
	var preview CommentTriggerPreviewResponse
	testutil.Call(t, testHandler.PreviewCommentTriggers,
		commentRequest(http.MethodPost, issueID, "", map[string]any{"content": mixed})).Want(http.StatusOK).JSON(&preview)
	if len(preview.Agents) != 0 || len(preview.Blocked) != 1 || preview.Blocked[0].ReasonCode != ReasonInvocationNotAllowed {
		t.Fatalf("preview must show the same rejected target and no runnable agents: %+v", preview)
	}
	if len(preview.InvalidMentions) != 1 || preview.InvalidMentions[0].Start != strings.Index(mixed, bad) {
		t.Fatalf("preview invalid span = %+v", preview.InvalidMentions)
	}
	var malformedPreview CommentTriggerPreviewResponse
	malformed := "[@Wrong](mention://agent/not-a-uuid)"
	testutil.Call(t, testHandler.PreviewCommentTriggers,
		commentRequest(http.MethodPost, issueID, "", map[string]any{"content": malformed})).Want(http.StatusOK).JSON(&malformedPreview)
	if len(malformedPreview.InvalidMentions) != 1 || malformedPreview.InvalidMentions[0].Start != 0 || malformedPreview.InvalidMentions[0].End != len(malformed) {
		t.Fatalf("malformed preview invalid span = %+v", malformedPreview.InvalidMentions)
	}

	otherUser := dbfx.User(t, "preview-other-user", "preview-other-user@example.test")
	dbfx.Member(t, testWorkspaceID, otherUser, "member")
	old := dbfx.Comment(t, issueID, "original body")
	for _, handler := range []struct {
		name string
		fn   http.HandlerFunc
		body map[string]any
	}{
		{"preview", testHandler.PreviewCommentTriggers, map[string]any{"content": mixed, "editing_comment_id": old}},
		{"edit", testHandler.UpdateComment, map[string]any{"content": mixed}},
	} {
		t.Run(handler.name, func(t *testing.T) {
			req := commentRequest(http.MethodPut, issueID, old, handler.body)
			if handler.name == "preview" {
				req = commentRequest(http.MethodPost, issueID, "", handler.body)
			}
			req.Header.Set("X-User-ID", otherUser)
			testutil.Call(t, handler.fn, req).Want(http.StatusForbidden)
		})
	}
}

func TestCommentMentionAdmissionValidOfflineAndExistingPending(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "mention-offline-runtime", testutil.Cols{"status": "offline"})
	agentID := dbfx.Agent(t, "mention-offline-agent", runtimeID)
	issueID := dbfx.Issue(t, "offline admission")
	content := fmt.Sprintf("[@Offline](mention://agent/%s)", agentID)
	var first CommentResponse
	testutil.Call(t, testHandler.CreateComment, commentRequest(http.MethodPost, issueID, "", map[string]any{"content": content})).Want(http.StatusCreated).JSON(&first)
	if len(first.TriggerOutcomes) != 1 || first.TriggerOutcomes[0].Status != DispatchQueued {
		t.Fatalf("first offline mention = %+v, want queued", first.TriggerOutcomes)
	}
	var second CommentResponse
	testutil.Call(t, testHandler.CreateComment, commentRequest(http.MethodPost, issueID, "", map[string]any{"content": content})).Want(http.StatusCreated).JSON(&second)
	if len(second.TriggerOutcomes) != 1 || (second.TriggerOutcomes[0].Status != DispatchCoalesced && second.TriggerOutcomes[0].Status != DispatchDeferred) {
		t.Fatalf("second offline mention = %+v, want coalesced or deferred", second.TriggerOutcomes)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM comment WHERE issue_id = $1`, issueID); got != 2 {
		t.Fatalf("accepted comments = %d, want 2", got)
	}
}

func TestCommentMentionAdmissionNoteAndAttachmentOnlyStayCompatible(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issueID := dbfx.Issue(t, "mention note exception")
	content := "/note [@Unknown](mention://agent/00000000-0000-0000-0000-0000000000ff)"
	var created CommentResponse
	testutil.Call(t, testHandler.CreateComment, commentRequest(http.MethodPost, issueID, "", map[string]any{"content": content})).Want(http.StatusCreated).JSON(&created)
	if len(created.TriggerOutcomes) != 0 {
		t.Fatalf("note triggered: %+v", created.TriggerOutcomes)
	}
	var updated CommentResponse
	testutil.Call(t, testHandler.UpdateComment, commentRequest(http.MethodPut, issueID, created.ID, map[string]any{"content": content, "attachment_ids": []string{}})).Want(http.StatusOK).JSON(&updated)
	if updated.Content != content {
		t.Fatalf("attachment-only edit changed content: %q", updated.Content)
	}
}
