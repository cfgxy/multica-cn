package handler

// Daily proposals at the HTTP boundary (RUYI-265, spec §A B1–B3).
//
// B1: no falsifiable prophecy, no pool entry — the server refuses, per type
//     (quantitative prophecies need an object, direction and numeric band;
//     behavior prophecies need an observable outcome and a falsifier).
// B2: adoption and verification are separate records — verification is only
//     possible after adoption, requires evidence, and appends marks rather
//     than overwriting them.
// B3: rejection retains the row with an audited reason; a rejected proposal
//     cannot be adopted in place, only restored (audited) back to draft.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

var (
	behaviorProphecy = map[string]any{
		"outcome_text":      "the next three runs attach the checklist without being asked",
		"falsify_condition": "two of the next three runs omit the checklist",
	}
	quantitativeProphecy = map[string]any{
		"object":       map[string]any{"metric": "median_total_tokens"},
		"outcome_text": "the quiz median falls into the predicted band",
		"direction":    "down",
		"range_low":    float64(200),
		"range_high":   float64(600),
	}
)

func createProposal(t *testing.T, body map[string]any) string {
	t.Helper()
	var created struct{ ID string }
	testutil.Call(t, testHandler.PostProposal, newRequest(http.MethodPost, "/api/proposals", body)).
		Want(http.StatusCreated).JSON(&created)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM proposal WHERE id = $1`, created.ID)
	})
	return created.ID
}

func listProposals(t *testing.T, status string) []ProposalResponse {
	t.Helper()
	path := "/api/proposals"
	if status != "" {
		path += "?status=" + status
	}
	var proposals []ProposalResponse
	testutil.Call(t, testHandler.GetProposals, newRequest(http.MethodGet, path, nil)).
		Want(http.StatusOK).JSON(&proposals)
	return proposals
}

func proposalByID(t *testing.T, id string) ProposalResponse {
	t.Helper()
	for _, proposal := range listProposals(t, "") {
		if proposal.ID == id {
			return proposal
		}
	}
	t.Fatalf("proposal %s not found in the pool", id)
	return ProposalResponse{}
}

func proposalAction(t *testing.T, id, action string, body any) int {
	t.Helper()
	response := testutil.Call(t, proposalActionHandler(action),
		withURLParam(newRequest(http.MethodPost, "/api/proposals/"+id+"/"+action, body), "id", id))
	return response.Code
}

func proposalActionHandler(action string) func(w http.ResponseWriter, r *http.Request) {
	switch action {
	case "adopt":
		return testHandler.AdoptProposal
	case "reject":
		return testHandler.RejectProposal
	case "restore":
		return testHandler.RestoreProposal
	case "verify":
		return testHandler.VerifyProposal
	}
	panic("unknown proposal action " + action)
}

// TestProposalProphecyGate is B1's refusal half: the pool only accepts
// proposals whose prophecy is falsifiable, with the required shape per type.
func TestProposalProphecyGate(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}

	// No prophecy at all.
	testutil.Call(t, testHandler.PostProposal, newRequest(http.MethodPost, "/api/proposals", map[string]any{
		"type": "lesson", "title": "no prophecy", "summary": "an unfalsifiable feeling",
	})).Want(http.StatusUnprocessableEntity)

	// Behavior prophecy without a falsification condition.
	testutil.Call(t, testHandler.PostProposal, newRequest(http.MethodPost, "/api/proposals", map[string]any{
		"type": "lesson", "title": "no falsifier", "summary": "outcome but no falsifier",
		"prophecy": map[string]any{"outcome_text": "things get better"},
	})).Want(http.StatusUnprocessableEntity)

	// Quantitative prophecy missing its numeric band.
	testutil.Call(t, testHandler.PostProposal, newRequest(http.MethodPost, "/api/proposals", map[string]any{
		"type": "skill", "title": "no band", "summary": "direction without a band",
		"prophecy": map[string]any{
			"object":       map[string]any{"metric": "median_total_tokens"},
			"outcome_text": "tokens fall",
			"direction":    "down",
		},
	})).Want(http.StatusUnprocessableEntity)

	// Unknown type.
	testutil.Call(t, testHandler.PostProposal, newRequest(http.MethodPost, "/api/proposals", map[string]any{
		"type": "wish", "prophecy": behaviorProphecy,
	})).Want(http.StatusBadRequest)

	// The accepted shapes do enter the pool, with the generation snapshot
	// captured server-side.
	behavior := createProposal(t, map[string]any{
		"type": "lesson", "title": "checklist habit", "summary": "attach the checklist unprompted",
		"prophecy": behaviorProphecy,
	})
	quantitative := createProposal(t, map[string]any{
		"type": "prompt_revision", "title": "shorter system prompt",
		"summary": "trim the preamble", "prophecy": quantitativeProphecy,
	})
	for _, id := range []string{behavior, quantitative} {
		proposal := proposalByID(t, id)
		if _, ok := proposal.GenerationSnapshot["captured_at"]; !ok {
			t.Fatalf("proposal %s: generation snapshot not captured", id)
		}
		if _, hasVersion := proposal.GenerationSnapshot["prompt_version"]; !hasVersion {
			t.Fatalf("proposal %s: generation snapshot lacks the prompt_version baseline", id)
		}
		if len(proposal.Prophecy) == 0 {
			t.Fatalf("proposal %s: prophecy missing from the response", id)
		}
	}
}

// TestProposalAdoptVerifySeparation is B2: verification records what
// happened after adoption — never before it, never without evidence, and
// never by overwriting an earlier mark.
func TestProposalAdoptVerifySeparation(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	fakeBdSetup(t)
	registerKnowledgeDir(t, "ultimate", fakeBdDir(t, nil))

	id := createProposal(t, map[string]any{
		"type": "lesson", "title": "checklist habit", "summary": "attach the checklist unprompted",
		"prophecy": behaviorProphecy,
	})

	// Verification before adoption refuses.
	if code := proposalAction(t, id, "verify", map[string]any{
		"verdict": "established", "evidence": "premature mark",
	}); code != http.StatusConflict {
		t.Fatalf("verify before adopt: %d, want 409", code)
	}

	// Adoption records its own snapshot, independent of any verification.
	if code := proposalAction(t, id, "adopt", nil); code != http.StatusOK {
		t.Fatalf("adopt: %d", code)
	}
	proposal := proposalByID(t, id)
	if proposal.Status != "adopted" {
		t.Fatalf("status after adopt: %q", proposal.Status)
	}
	if _, ok := proposal.AdoptionSnapshot["adopted_by"]; !ok {
		t.Fatalf("adoption snapshot missing the decision maker")
	}
	transfer, _ := proposal.AdoptionSnapshot["knowledge_transfer"].(map[string]any)
	if transfer["key"] != "proposal-"+id[:8] {
		t.Fatalf("adoption snapshot transfer: %v, want key proposal-%s", transfer, id[:8])
	}
	if proposal.Verification != nil && len(proposal.Verification) > 0 {
		t.Fatalf("adoption must not fabricate a verification record: %v", proposal.Verification)
	}

	// Evidence is mandatory; verdicts are constrained.
	if code := proposalAction(t, id, "verify", map[string]any{
		"verdict": "established",
	}); code != http.StatusUnprocessableEntity {
		t.Fatalf("verify without evidence: %d, want 422", code)
	}
	if code := proposalAction(t, id, "verify", map[string]any{
		"verdict": "probably", "evidence": "hunch",
	}); code != http.StatusBadRequest {
		t.Fatalf("verify with unknown verdict: %d, want 400", code)
	}

	// Marks append — the second verification keeps the first.
	if code := proposalAction(t, id, "verify", map[string]any{
		"verdict": "partial", "evidence": "two of three runs attached it",
	}); code != http.StatusOK {
		t.Fatalf("first verify: %d", code)
	}
	if code := proposalAction(t, id, "verify", map[string]any{
		"verdict": "established", "evidence": "all three runs attached it",
	}); code != http.StatusOK {
		t.Fatalf("second verify: %d", code)
	}
	proposal = proposalByID(t, id)
	marks, _ := proposal.Verification["marks"].([]any)
	if len(marks) != 2 {
		t.Fatalf("verification marks: %d, want 2 (append, not overwrite)", len(marks))
	}
}

// TestProposalRejectRetainsAndRestore is B3: rejection keeps the row and the
// audited reason, blocks in-place adoption, and re-entry happens only via
// the audited restore action.
func TestProposalRejectRetainsAndRestore(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	fakeBdSetup(t)
	registerKnowledgeDir(t, "ultimate", fakeBdDir(t, nil))

	id := createProposal(t, map[string]any{
		"type": "lesson", "title": "checklist habit", "summary": "attach the checklist unprompted",
		"prophecy": behaviorProphecy,
	})
	if code := proposalAction(t, id, "reject", map[string]any{
		"reason": "the falsifier already fired during the trial week",
	}); code != http.StatusOK {
		t.Fatalf("reject: %d", code)
	}

	// Retained and retrievable: still in the pool, still carrying its
	// prophecy and the audited reason.
	rejected := proposalByID(t, id)
	if rejected.Status != "rejected" || len(rejected.Prophecy) == 0 {
		t.Fatalf("rejected proposal: status=%q prophecy=%v", rejected.Status, rejected.Prophecy)
	}
	foundReason := false
	for _, entry := range rejected.AuditLog {
		audit, ok := entry.(map[string]any)
		if !ok || audit["action"] != "reject" {
			continue
		}
		detail, _ := audit["detail"].(map[string]any)
		reason, _ := detail["reason"].(string)
		if strings.Contains(reason, "falsifier") {
			foundReason = true
		}
	}
	if !foundReason {
		t.Fatalf("audit log after reject: %v, want a reject entry carrying the reason", rejected.AuditLog)
	}

	// In-place adoption of a rejected proposal refuses.
	if code := proposalAction(t, id, "adopt", nil); code != http.StatusConflict {
		t.Fatalf("adopt after reject: %d, want 409", code)
	}

	// Restore re-enters the pool as a draft, audited; adoption then works.
	if code := proposalAction(t, id, "restore", nil); code != http.StatusOK {
		t.Fatalf("restore: %d", code)
	}
	if proposalByID(t, id).Status != "draft" {
		t.Fatalf("status after restore: %q, want draft", proposalByID(t, id).Status)
	}
	if code := proposalAction(t, id, "adopt", nil); code != http.StatusOK {
		t.Fatalf("adopt after restore: %d", code)
	}
	adopted := proposalByID(t, id)
	restoreFound := false
	for _, entry := range adopted.AuditLog {
		if audit, ok := entry.(map[string]any); ok && audit["action"] == "restore" {
			restoreFound = true
		}
	}
	if !restoreFound {
		t.Fatalf("audit log after restore+adopt: %v, want a restore entry", adopted.AuditLog)
	}
}

// TestProposalKnowledgeTransferFailureAndRetry pins the ②–④ adoption rule:
// a knowledge-type proposal is adopted only when its content reached the
// ultimate bd and was read back. A failed transfer leaves the proposal
// un-adopted with the reason; a retry after the failure heals succeeds.
func TestProposalKnowledgeTransferFailureAndRetry(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	fakeBdSetup(t)
	ultimatePath := fakeBdDir(t, nil)
	registerKnowledgeDir(t, "ultimate", ultimatePath)

	id := createProposal(t, map[string]any{
		"type": "pitfall", "title": "migration renumbering trap",
		"summary": "renumbering without rewriting the ledger replays dropped DDL",
		"prophecy": behaviorProphecy,
	})
	if err := os.WriteFile(filepath.Join(ultimatePath, "FAIL_REMEMBER"), []byte("1"), 0o644); err != nil {
		t.Fatalf("plant failure flag: %v", err)
	}
	if code := proposalAction(t, id, "adopt", nil); code != http.StatusConflict {
		t.Fatalf("adopt with failing transfer: %d, want 409", code)
	}
	stalled := proposalByID(t, id)
	if stalled.Status != "draft" || stalled.TransferError == "" {
		t.Fatalf("after failed transfer: status=%q transfer_error=%q, want draft + reason", stalled.Status, stalled.TransferError)
	}
	if _, hasSnapshot := stalled.AdoptionSnapshot["adopted_by"]; hasSnapshot {
		t.Fatalf("a failed transfer must not leave an adoption snapshot")
	}

	if err := os.Remove(filepath.Join(ultimatePath, "FAIL_REMEMBER")); err != nil {
		t.Fatalf("clear failure flag: %v", err)
	}
	if code := proposalAction(t, id, "adopt", nil); code != http.StatusOK {
		t.Fatalf("adopt after healing: %d", code)
	}
	adopted := proposalByID(t, id)
	if adopted.Status != "adopted" || adopted.TransferError != "" {
		t.Fatalf("after successful retry: status=%q transfer_error=%q", adopted.Status, adopted.TransferError)
	}
	if content := fakeBdState(t, ultimatePath)["proposal-"+id[:8]]; !strings.Contains(content, "renumbering without rewriting the ledger") {
		t.Fatalf("ultimate state after retry: %q", content)
	}
}

// TestProposalNonKnowledgeAdoptionSkipsTransfer pins the type split:
// prompt_revision and skill proposals adopt without touching the knowledge
// library — their adoption is the prompt/skill decision, not a transfer.
func TestProposalNonKnowledgeAdoptionSkipsTransfer(t *testing.T) {
	if testHandler == nil {
		t.Fatal("database fixture is required")
	}
	// Deliberately NO fake bd and NO ultimate directory registered: if the
	// adoption path tried any bd interaction it would fail loudly here.
	id := createProposal(t, map[string]any{
		"type": "prompt_revision", "title": "shorter system prompt",
		"summary": "trim the preamble", "prophecy": quantitativeProphecy,
	})
	if code := proposalAction(t, id, "adopt", nil); code != http.StatusOK {
		t.Fatalf("adopt prompt_revision without knowledge transfer: %d", code)
	}
	proposal := proposalByID(t, id)
	if _, hasTransfer := proposal.AdoptionSnapshot["knowledge_transfer"]; hasTransfer {
		t.Fatalf("prompt_revision adoption must not record a knowledge transfer")
	}
}
