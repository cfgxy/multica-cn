package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestClauseRevisionBaselineFromAudit(t *testing.T) {
	audit := []byte(`[
		{"action":"created"},
		{"action":"submitted","revision_baseline":{"clause_id":"clause-1","binding_id":"binding-1","clause_version":2,"since_clause_version":2}}
	]`)
	baseline, ok := clauseRevisionBaselineFromAudit(audit)
	if !ok {
		t.Fatal("valid submitted revision baseline was not parsed")
	}
	if !clauseRevisionBaselineMatches(baseline, "clause-1", "binding-1", 2, 2) {
		t.Fatalf("parsed baseline does not match its submitted state: %+v", baseline)
	}
	if clauseRevisionBaselineMatches(baseline, "clause-1", "binding-1", 3, 3) {
		t.Fatal("baseline matched after the clause advanced")
	}
	if clauseRevisionBaselineMatches(baseline, "clause-1", "new-binding", 2, 2) {
		t.Fatal("baseline matched after the active binding changed")
	}
}

func TestClauseRevisionBaselineFromAuditUsesLatestSubmission(t *testing.T) {
	audit := []byte(`[
		{"action":"submitted","revision_baseline":{"clause_id":"clause-1","binding_id":"binding-1","clause_version":1,"since_clause_version":1}},
		{"action":"reworked"},
		{"action":"submitted","revision_baseline":{"clause_id":"clause-1","binding_id":"binding-1","clause_version":2,"since_clause_version":2}}
	]`)
	baseline, ok := clauseRevisionBaselineFromAudit(audit)
	if !ok || baseline.ClauseVersion != 2 || baseline.SinceClauseVersion != 2 {
		t.Fatalf("latest submission baseline = %+v, ok=%v", baseline, ok)
	}
}

func TestClauseRevisionBaselineFromAuditFailsClosed(t *testing.T) {
	for _, audit := range [][]byte{
		[]byte(`[]`),
		[]byte(`[{"action":"submitted"}]`),
		[]byte(`[{"action":"submitted","revision_baseline":{"clause_id":"clause-1","binding_id":"binding-1","clause_version":0,"since_clause_version":0}}]`),
		[]byte(`not-json`),
	} {
		if _, ok := clauseRevisionBaselineFromAudit(audit); ok {
			t.Errorf("invalid or missing baseline accepted: %s", audit)
		}
	}
}

func TestStructuredClauseRevisionRequiresLibraryTarget(t *testing.T) {
	proposal := db.PromptProposal{
		ChangeKind: "revise_clause",
		ClauseID:   pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
	}
	if target, message := normalizeProposalEnactmentTarget("clause_library", proposal); message != "" || target != "clause_library" {
		t.Fatalf("structured library target = %q, %q; want clause_library", target, message)
	}
	if target, message := normalizeProposalEnactmentTarget("static_prompt", proposal); message == "" || target != "" {
		t.Fatalf("structured static target = %q, %q; want rejection", target, message)
	}

	proposal.ClauseID = pgtype.UUID{}
	if target, message := normalizeProposalEnactmentTarget("static_prompt", proposal); message != "" || target != "static_prompt" {
		t.Fatalf("legacy name-only static target = %q, %q; want static_prompt", target, message)
	}
}
