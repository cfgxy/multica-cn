package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// anchorHTTP drives the GET /api/comments/{id}/anchor handler and returns the
// raw status plus the decoded payload on 200. Mirrors the probe the web client
// issues for a cross-issue `mention://comment/<id>` chip (RUYI-643).
func anchorHTTP(t *testing.T, commentID string) (int, CommentAnchorResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	r := newRequest("GET", "/api/comments/"+commentID+"/anchor", nil)
	r = withURLParam(r, "commentId", commentID)
	testHandler.ResolveCommentAnchor(w, r)
	var resp CommentAnchorResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode anchor response: %v", err)
		}
	}
	return w.Code, resp
}

// anchorFixture seeds one issue (with the given content as its single
// comment) in the test workspace and returns (issueID, issueNumber, commentID).
func anchorFixture(t *testing.T, content string) (string, int32, string) {
	t.Helper()
	ctx := context.Background()

	var issueID string
	var issueNumber int32
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, creator_type, creator_id, title)
		VALUES ($1, 'member', $2, $3)
		RETURNING id, number
	`, testWorkspaceID, testUserID, "anchor fixture").Scan(&issueID, &issueNumber); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})

	var commentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type, parent_id, created_at)
		VALUES ($1, $2, 'member', $3, $4, 'comment', NULL, $5)
		RETURNING id
	`, issueID, testWorkspaceID, testUserID, content, time.Now().UTC()).Scan(&commentID); err != nil {
		t.Fatalf("insert comment: %v", err)
	}

	return issueID, issueNumber, commentID
}

func TestResolveCommentAnchor_ReturnsOwningIssueAndAuthor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	setWorkspaceIssuePrefixForTest(t, "ANC")
	_, issueNumber, commentID := anchorFixture(t, "回传记录：构建产物已上传")

	code, resp := anchorHTTP(t, commentID)
	if code != http.StatusOK {
		t.Fatalf("anchor %s: status %d: %s", commentID, code, "body suppressed")
	}
	if resp.Identifier != fmt.Sprintf("ANC-%d", issueNumber) {
		t.Fatalf("identifier = %q, want ANC-%d", resp.Identifier, issueNumber)
	}
	if resp.AuthorType != "member" || resp.AuthorID != testUserID {
		t.Fatalf("author = %s/%s, want member/%s", resp.AuthorType, resp.AuthorID, testUserID)
	}
	if resp.Excerpt != "回传记录：构建产物已上传" {
		t.Fatalf("excerpt = %q", resp.Excerpt)
	}
	if resp.CreatedAt == "" {
		t.Fatalf("created_at must be set")
	}
}

func TestResolveCommentAnchor_UnknownAndForeignMissesLookIdentical(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	_, _, commentID := anchorFixture(t, "本单评论")

	// A valid id with no row behind it.
	missing := "0e0e0e0e-0e0e-4e0e-8e0e-0e0e0e0e0e0e"

	// The same comment content living in ANOTHER workspace: the caller's
	// membership does not reach it, and GetCommentInWorkspace must fold it
	// into the same 404 as the missing id — distinguishing the two would
	// disclose that an invisible comment exists.
	var foreignWS string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, '', $3)
		RETURNING id
	`, "Anchor Foreign", handlerTestSlug("anchor-foreign"), "AFW").Scan(&foreignWS); err != nil {
		t.Fatalf("create foreign workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, foreignWS)
	})
	var foreignIssue string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, creator_type, creator_id, title)
		VALUES ($1, 'member', $2, 'foreign anchor fixture')
		RETURNING id
	`, foreignWS, testUserID).Scan(&foreignIssue); err != nil {
		t.Fatalf("create foreign issue: %v", err)
	}
	var foreignComment string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type, parent_id, created_at)
		VALUES ($1, $2, 'member', $3, '外部单评论', 'comment', NULL, $4)
		RETURNING id
	`, foreignIssue, foreignWS, testUserID, time.Now().UTC()).Scan(&foreignComment); err != nil {
		t.Fatalf("insert foreign comment: %v", err)
	}

	codeMissing, _ := anchorHTTP(t, missing)
	codeForeign, _ := anchorHTTP(t, foreignComment)
	codeKnown := func() int { c, _ := anchorHTTP(t, commentID); return c }()

	if codeMissing != http.StatusNotFound {
		t.Fatalf("missing comment: status %d, want 404", codeMissing)
	}
	if codeForeign != http.StatusNotFound {
		t.Fatalf("foreign-workspace comment: status %d, want 404 (must be indistinguishable from missing)", codeForeign)
	}
	if codeKnown != http.StatusOK {
		t.Fatalf("own-workspace comment: status %d, want 200 (control)", codeKnown)
	}
}

func TestResolveCommentAnchor_ExcerptCappedWithoutSplittingRunes(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	// "a" + 100×"回": byte 240 lands mid-rune (byte 0 + 79×3 = 237 complete
	// runes; the 80th spans bytes 238-240). The cut must back off to a valid
	// boundary instead of emitting broken UTF-8.
	content := "a" + strings.Repeat("回", 100)
	_, _, commentID := anchorFixture(t, content)

	code, resp := anchorHTTP(t, commentID)
	if code != http.StatusOK {
		t.Fatalf("anchor status %d, want 200", code)
	}
	if len(resp.Excerpt) != 238 {
		t.Fatalf("excerpt bytes = %d, want 238 (240-byte cut backed off to a rune boundary)", len(resp.Excerpt))
	}
	if !utf8.ValidString(resp.Excerpt) {
		t.Fatalf("excerpt is not valid UTF-8")
	}
	if !strings.HasPrefix(content, resp.Excerpt) {
		t.Fatalf("excerpt must be a prefix of the content")
	}
}

func TestResolveCommentAnchor_MalformedIDIsBadRequest(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	code, _ := anchorHTTP(t, "not-a-uuid")
	if code != http.StatusBadRequest {
		t.Fatalf("malformed id: status %d, want 400", code)
	}
}
