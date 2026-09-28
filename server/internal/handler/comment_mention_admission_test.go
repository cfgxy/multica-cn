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
