package handler

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestParseGitHubReviewRefreshTarget(t *testing.T) {
	body := []byte(`{"action":"submitted","installation":{"id":42},"repository":{"name":"widget","owner":{"login":"acme"}},"pull_request":{"number":17},"review":{"state":"CHANGES_REQUESTED"}}`)
	target, ok := parseGitHubReviewRefreshTarget(body)
	if !ok {
		t.Fatal("valid submitted review should enqueue a snapshot refresh")
	}
	if target != (githubPRRefreshTarget{installationID: 42, owner: "acme", repo: "widget", number: 17}) {
		t.Fatalf("refresh target = %+v", target)
	}

	for _, tc := range []struct {
		name string
		body string
	}{
		{"ignored_action", `{"action":"created","installation":{"id":42},"repository":{"name":"widget","owner":{"login":"acme"}},"pull_request":{"number":17}}`},
		{"missing_installation", `{"action":"submitted","repository":{"name":"widget","owner":{"login":"acme"}},"pull_request":{"number":17}}`},
		{"missing_pr", `{"action":"dismissed","installation":{"id":42},"repository":{"name":"widget","owner":{"login":"acme"}}}`},
		{"malformed", `{"action":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := parseGitHubReviewRefreshTarget([]byte(tc.body)); ok {
				t.Fatalf("unexpected refresh target %+v", got)
			}
		})
	}
}

func TestIssuePullRequestResponseIncludesOnlyCurrentSnapshotReviewDecision(t *testing.T) {
	row := db.ListPullRequestsByIssueRow{
		HeadSha:           "head-a",
		SnapshotHeadSha:   "head-a",
		SnapshotFetchedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		ReviewDecision:    pgtype.Text{String: "CHANGES_REQUESTED", Valid: true},
	}
	resp := issuePullRequestRowToResponse(row, true)
	if resp.ReviewDecision == nil || *resp.ReviewDecision != "changes_requested" {
		t.Fatalf("current snapshot review_decision = %v", resp.ReviewDecision)
	}

	row.SnapshotHeadSha = "older-head"
	resp = issuePullRequestRowToResponse(row, true)
	if resp.ReviewDecision != nil {
		t.Fatalf("stale-head review_decision = %q, want nil", *resp.ReviewDecision)
	}
}
