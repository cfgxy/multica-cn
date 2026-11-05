package handler

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func commentRequest(method, issueID, commentID string, body any) *http.Request {
	req := testutil.WithHeaders(testutil.JSONRequest(method, "/api/issues/"+issueID+"/comments", body), "X-User-ID", testUserID, "X-Workspace-ID", testWorkspaceID)
	if method == http.MethodPut {
		req.URL.Path = "/api/comments/" + commentID
		return testutil.WithURLParams(req, "commentId", commentID)
	}
	return testutil.WithURLParams(req, "id", issueID)
}

func requireInvalidMentionSpans(t *testing.T, warnings []invalidAgentMention, want ...invalidAgentMention) {
	t.Helper()
	if len(warnings) != len(want) {
		t.Fatalf("invalid_mentions = %+v, want %+v", warnings, want)
	}
	for i := range want {
		if warnings[i] != want[i] {
			t.Fatalf("invalid_mentions[%d] = %+v, want %+v", i, warnings[i], want[i])
		}
	}
}

func TestCommentMentionInvalidTargetsSaveWithBlockedOutcomes(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	privateID, _, _ := privateAgentTestFixture(t)
	archivedID := createHandlerTestAgent(t, "soft-admission-archived", nil)
	dbfx.Exec(t, `UPDATE agent SET archived_at = now() WHERE id = $1`, archivedID)

	foreignWorkspaceID := dbfx.Workspace(t, "soft admission foreign", "soft-admission-foreign")
	foreignRuntimeID := dbfx.Runtime(t, "soft-admission-foreign-runtime", testutil.Cols{"workspace_id": foreignWorkspaceID})
	foreignID := dbfx.Agent(t, "soft-admission-foreign-agent", foreignRuntimeID, testutil.Cols{"workspace_id": foreignWorkspaceID})

	issueID := dbfx.Issue(t, "soft mention admission")
	cases := []struct {
		name       string
		targetID   string
		wantReason DispatchReasonCode
	}{
		{name: "unknown", targetID: "00000000-0000-0000-0000-0000000000ff", wantReason: ReasonInvocationNotAllowed},
		{name: "cross workspace", targetID: foreignID, wantReason: ReasonInvocationNotAllowed},
		{name: "no invoke permission", targetID: privateID, wantReason: ReasonInvocationNotAllowed},
		{name: "archived", targetID: archivedID, wantReason: ReasonTargetUnavailable},
		{name: "malformed parsed id", targetID: "deadbeef", wantReason: ReasonTargetUnavailable},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := fmt.Sprintf("Please inspect [@Target](mention://agent/%s).", tc.targetID)

			var created CommentResponse
			testutil.Call(t, testHandler.CreateComment,
				commentRequest(http.MethodPost, issueID, "", map[string]any{"content": content}),
			).Want(http.StatusCreated).JSON(&created)
			outcome := findCommentOutcome(t, created.TriggerOutcomes, tc.targetID)
			if outcome.Status != DispatchBlocked || outcome.ReasonCode != tc.wantReason {
				t.Fatalf("create outcome = %+v, want blocked/%s", outcome, tc.wantReason)
			}
			if len(created.InvalidMentions) != 0 {
				t.Fatalf("parsed target warning = %+v, want none", created.InvalidMentions)
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE trigger_comment_id = $1`, created.ID); got != 0 {
				t.Fatalf("blocked create queued %d tasks", got)
			}

			existingID := dbfx.Comment(t, issueID, "original body")
			var updated CommentResponse
			testutil.Call(t, testHandler.UpdateComment,
				commentRequest(http.MethodPut, issueID, existingID, map[string]any{"content": content}),
			).Want(http.StatusOK).JSON(&updated)
			outcome = findCommentOutcome(t, updated.TriggerOutcomes, tc.targetID)
			if outcome.Status != DispatchBlocked || outcome.ReasonCode != tc.wantReason {
				t.Fatalf("edit outcome = %+v, want blocked/%s", outcome, tc.wantReason)
			}
			if updated.Content != content {
				t.Fatalf("stored edit content = %q, want %q", updated.Content, content)
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE trigger_comment_id = $1`, existingID); got != 0 {
				t.Fatalf("blocked edit queued %d tasks", got)
			}
		})
	}
}

func TestCommentMentionUnparsedLinkSavesWithWarning(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	issueID := dbfx.Issue(t, "mention warning")
	allowedID := createHandlerTestAgent(t, "mention-warning-allowed", nil)
	parsedLink := fmt.Sprintf("[@Allowed](mention://agent/%s)", allowedID)
	escapedBadLink := `[@Lost \[draft\]](mention://agent/not-a-uuid)`
	emptyLabelLink := fmt.Sprintf("[](mention://agent/%s)", allowedID)
	content := strings.Join([]string{parsedLink, escapedBadLink, emptyLabelLink}, " ")
	escapedStart := strings.Index(content, escapedBadLink)
	emptyStart := strings.Index(content, emptyLabelLink)
	warnings := []invalidAgentMention{
		{Start: escapedStart, End: escapedStart + len(escapedBadLink)},
		{Start: emptyStart, End: emptyStart + len(emptyLabelLink)},
	}

	var preview CommentTriggerPreviewResponse
	testutil.Call(t, testHandler.PreviewCommentTriggers,
		commentRequest(http.MethodPost, issueID, "", map[string]any{"content": content}),
	).Want(http.StatusOK).JSON(&preview)
	requirePreviewAgents(t, preview, allowedID)
	requireInvalidMentionSpans(t, preview.InvalidMentions, warnings...)

	var created CommentResponse
	testutil.Call(t, testHandler.CreateComment,
		commentRequest(http.MethodPost, issueID, "", map[string]any{"content": content}),
	).Want(http.StatusCreated).JSON(&created)
	requireInvalidMentionSpans(t, created.InvalidMentions, warnings...)
	if outcome := findCommentOutcome(t, created.TriggerOutcomes, allowedID); outcome.Status != DispatchQueued {
		t.Fatalf("allowed outcome = %+v, want queued", outcome)
	}

	updatedContent := content + " after review"
	var updated CommentResponse
	testutil.Call(t, testHandler.UpdateComment,
		commentRequest(http.MethodPut, issueID, created.ID, map[string]any{"content": updatedContent}),
	).Want(http.StatusOK).JSON(&updated)
	requireInvalidMentionSpans(t, updated.InvalidMentions, warnings...)
	if updated.Content != updatedContent {
		t.Fatalf("stored edit content = %q, want %q", updated.Content, updatedContent)
	}

	otherUser := dbfx.User(t, "warning-preview-other", "warning-preview-other@example.test")
	dbfx.Member(t, testWorkspaceID, otherUser, "member")
	for _, handler := range []struct {
		name string
		fn   http.HandlerFunc
		body map[string]any
	}{
		{name: "preview", fn: testHandler.PreviewCommentTriggers, body: map[string]any{"content": content, "editing_comment_id": created.ID}},
		{name: "edit", fn: testHandler.UpdateComment, body: map[string]any{"content": content}},
	} {
		t.Run(handler.name+" permission", func(t *testing.T) {
			req := commentRequest(http.MethodPut, issueID, created.ID, handler.body)
			if handler.name == "preview" {
				req = commentRequest(http.MethodPost, issueID, "", handler.body)
			}
			req.Header.Set("X-User-ID", otherUser)
			testutil.Call(t, handler.fn, req).Want(http.StatusForbidden)
		})
	}
}

func TestAgentCommentResponsibilityMentionsAreAdvisory(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	runtimeID := dbfx.Runtime(t, "advisory-responsibility-runtime")
	authorID := dbfx.Agent(t, "advisory-responsibility-author", runtimeID)
	firstTargetID := createHandlerTestAgent(t, "advisory-first-target", nil)
	secondTargetID := createHandlerTestAgent(t, "advisory-second-target", nil)
	issueID := dbfx.Issue(t, "responsibility mentions are advisory")
	taskID := dbfx.Task(t, authorID, testutil.Cols{
		"runtime_id": runtimeID, "issue_id": issueID,
		"originator_user_id": testUserID, "accountable_user_id": testUserID,
		"status": "running", "started_at": testutil.Raw("now()"),
	})
	request := func(method, commentID, content string) *http.Request {
		req := commentRequest(method, issueID, commentID, map[string]any{"content": content})
		req.Header.Set("X-Agent-ID", authorID)
		req.Header.Set("X-Task-ID", taskID)
		return req
	}

	var created CommentResponse
	testutil.Call(t, testHandler.CreateComment,
		request(http.MethodPost, "", "The requested work is complete; no handoff is required."),
	).Want(http.StatusCreated).JSON(&created)
	if len(created.TriggerOutcomes) != 0 {
		t.Fatalf("terminal report outcomes = %+v, want none", created.TriggerOutcomes)
	}

	multiMention := fmt.Sprintf("[@First](mention://agent/%s) [@Second](mention://agent/%s)", firstTargetID, secondTargetID)
	var updated CommentResponse
	testutil.Call(t, testHandler.UpdateComment,
		request(http.MethodPut, created.ID, multiMention),
	).Want(http.StatusOK).JSON(&updated)
	for _, targetID := range []string{firstTargetID, secondTargetID} {
		if outcome := findCommentOutcome(t, updated.TriggerOutcomes, targetID); outcome.Status != DispatchQueued {
			t.Fatalf("multi-mention outcome for %s = %+v, want queued", targetID, outcome)
		}
	}

	leaderID := createHandlerTestAgent(t, "advisory-squad-leader", nil)
	workerID := createHandlerTestAgent(t, "advisory-squad-worker", nil)
	otherID := createHandlerTestAgent(t, "advisory-squad-other", nil)
	squadID := dbfx.Squad(t, "advisory assigned squad", leaderID)
	dbfx.SquadMember(t, squadID, "agent", workerID, testutil.Cols{"role": "worker"})
	squadIssueID := dbfx.Issue(t, "worker may mention non-leader", testutil.Cols{
		"assignee_type": "squad",
		"assignee_id":   squadID,
	})
	workerTaskID := dbfx.Task(t, workerID, testutil.Cols{
		"runtime_id": handlerTestRuntimeID(t), "issue_id": squadIssueID,
		"originator_user_id": testUserID, "accountable_user_id": testUserID,
		"status": "running", "started_at": testutil.Raw("now()"), "squad_id": squadID,
	})
	workerContent := fmt.Sprintf("Verification complete; [@Other](mention://agent/%s) should inspect the follow-up.", otherID)
	workerReq := commentRequest(http.MethodPost, squadIssueID, "", map[string]any{"content": workerContent})
	workerReq.Header.Set("X-Agent-ID", workerID)
	workerReq.Header.Set("X-Task-ID", workerTaskID)
	var workerComment CommentResponse
	testutil.Call(t, testHandler.CreateComment, workerReq).Want(http.StatusCreated).JSON(&workerComment)
	if outcome := findCommentOutcome(t, workerComment.TriggerOutcomes, otherID); outcome.Status != DispatchQueued {
		t.Fatalf("worker non-leader handoff outcome = %+v, want queued", outcome)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued'`, squadIssueID, leaderID); got != 0 {
		t.Fatalf("worker non-leader mention also queued %d leader tasks", got)
	}
}

func TestAgentSelfMentionSavesAndSuppressesSameIssueTrigger(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := dbfx.Runtime(t, "soft-self-mention-runtime")
	authorID := dbfx.Agent(t, "soft-self-mention-author", runtimeID)
	issueID := dbfx.Issue(t, "soft self mention")
	taskID := dbfx.Task(t, authorID, testutil.Cols{
		"runtime_id": runtimeID, "issue_id": issueID,
		"originator_user_id": testUserID, "accountable_user_id": testUserID,
		"status": "running", "started_at": testutil.Raw("now()"),
	})
	request := func(method, commentID, content string) *http.Request {
		req := commentRequest(method, issueID, commentID, map[string]any{"content": content})
		req.Header.Set("X-Agent-ID", authorID)
		req.Header.Set("X-Task-ID", taskID)
		return req
	}
	selfMention := fmt.Sprintf("[@Self](mention://agent/%s)", authorID)

	var created CommentResponse
	testutil.Call(t, testHandler.CreateComment,
		request(http.MethodPost, "", selfMention),
	).Want(http.StatusCreated).JSON(&created)
	if outcome := findCommentOutcome(t, created.TriggerOutcomes, authorID); outcome.Status != DispatchBlocked || outcome.ReasonCode != ReasonSelfTriggerSuppressed {
		t.Fatalf("create self-mention outcome = %+v, want blocked/self_trigger_suppressed", outcome)
	}

	var updated CommentResponse
	testutil.Call(t, testHandler.UpdateComment,
		request(http.MethodPut, created.ID, selfMention+" after verification"),
	).Want(http.StatusOK).JSON(&updated)
	if outcome := findCommentOutcome(t, updated.TriggerOutcomes, authorID); outcome.Status != DispatchBlocked || outcome.ReasonCode != ReasonSelfTriggerSuppressed {
		t.Fatalf("edit self-mention outcome = %+v, want blocked/self_trigger_suppressed", outcome)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2`, issueID, authorID); got != 1 {
		t.Fatalf("same-issue self mention tasks = %d, want only the authoring task", got)
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
	content := "/note [@Unknown](mention://agent/not-a-uuid)"
	var created CommentResponse
	testutil.Call(t, testHandler.CreateComment, commentRequest(http.MethodPost, issueID, "", map[string]any{"content": content})).Want(http.StatusCreated).JSON(&created)
	if len(created.TriggerOutcomes) != 0 || len(created.InvalidMentions) != 0 {
		t.Fatalf("note feedback = outcomes %+v warnings %+v, want none", created.TriggerOutcomes, created.InvalidMentions)
	}
	var updated CommentResponse
	testutil.Call(t, testHandler.UpdateComment, commentRequest(http.MethodPut, issueID, created.ID, map[string]any{"content": content, "attachment_ids": []string{}})).Want(http.StatusOK).JSON(&updated)
	if updated.Content != content || len(updated.TriggerOutcomes) != 0 || len(updated.InvalidMentions) != 0 {
		t.Fatalf("attachment-only note edit = %+v", updated)
	}
}
