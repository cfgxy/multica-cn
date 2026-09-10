package handler

// Regression tests for the review findings on RUYI-100's first delivery.
//
// Each block below pins one contract that was broken in a way no existing test
// noticed, because each broke in the gap between two steps that looked correct
// on their own: a publish that answered 200 without doing anything, a check
// that ran outside the transaction it was guarding, and an existence test that
// could not tell "nothing was there" from "nothing was recorded".

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func promptErrorCode(t *testing.T, raw string) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, raw)
	}
	return body.Code
}

func publishPromptVersionAs(t *testing.T, versionID string, public bool) (int, string) {
	t.Helper()
	return callPrompt(t, testHandler.PublishPromptVersion, http.MethodPost,
		"/api/marketplace/prompt-versions/"+versionID+"/publish",
		map[string]any{"public": public}, map[string]string{"id": versionID})
}

func promptVersionVisibility(t *testing.T, versionID string) string {
	t.Helper()
	var visibility string
	dbfx.QueryRow(t, `SELECT visibility FROM marketplace_prompt_version WHERE id = $1`, versionID).Scan(&visibility)
	return visibility
}

// ── Blocker 3: publishing freezes once, and a version's visibility is part of
// what gets frozen ──────────────────────────────────────────────────────────

// The regression: the dialog scanned by publishing privately and then "made it
// public" with a second call. The second call hit the already-published early
// return, got a 200, and changed nothing — so a publisher who chose public got
// a version nobody outside their workspace could find, and was told it worked.
//
// The endpoint cannot distinguish those two calls from a genuine retry by
// itself, so it refuses the one that would silently lie: a repeat asking for
// the visibility the row already has is idempotent, and a repeat asking for a
// different one is a conflict.
func TestRepublishingWithADifferentVisibilityIsRefused(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := promptTestAgent(t, "prompt-revisibility", "a prompt worth publishing")
	draft := createPromptDraft(t, agentID, nil)

	if code, raw := publishPromptVersionAs(t, draft.ID, false); code != http.StatusOK {
		t.Fatalf("private publish: expected 200, got %d: %s", code, raw)
	}
	if got := promptVersionVisibility(t, draft.ID); got != promptVisibilityPrivate {
		t.Fatalf("visibility = %q, want private", got)
	}

	code, raw := publishPromptVersionAs(t, draft.ID, true)
	if code != http.StatusConflict {
		t.Fatalf("expected 409 turning a published version public, got %d: %s", code, raw)
	}
	if got := promptErrorCode(t, raw); got != "prompt_already_published" {
		t.Fatalf("code = %q, want prompt_already_published", got)
	}
	// The important half: whatever the answer, it must match reality.
	if got := promptVersionVisibility(t, draft.ID); got != promptVisibilityPrivate {
		t.Fatalf("visibility = %q after a refused change, want private", got)
	}
}

// A real retry — same visibility, same version — still succeeds, so a dropped
// response or a double click is not turned into an error the user cannot act on.
func TestRepublishingWithTheSameVisibilityIsIdempotent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := promptTestAgent(t, "prompt-republish-idem", "a prompt worth publishing")
	draft := createPromptDraft(t, agentID, nil)

	first := publishPromptDraft(t, draft.ID)
	code, raw := publishPromptVersionAs(t, draft.ID, true)
	if code != http.StatusOK {
		t.Fatalf("expected 200 retrying the same publish, got %d: %s", code, raw)
	}
	second := decodePromptVersion(t, raw)
	if first.Version == nil || second.Version == nil || *first.Version != *second.Version {
		t.Fatalf("a retry assigned a different version: %v vs %v", first.Version, second.Version)
	}
	if got := promptVersionVisibility(t, draft.ID); got != promptVisibilityPublic {
		t.Fatalf("visibility = %q, want public", got)
	}
}

// Scanning is what the wizard needs before the publisher chooses anything, and
// the reason the endpoint exists: it answers the same question publishing does
// without freezing the draft, so a publisher who scans and then decides to keep
// working still has a draft to work on.
func TestScanDoesNotPublishOrFreezeTheDraft(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := promptTestAgent(t, "prompt-scan-nofreeze", "a clean prompt")
	draft := createPromptDraft(t, agentID, nil)

	code, raw := callPrompt(t, testHandler.ScanPromptVersion, http.MethodPost,
		"/api/marketplace/prompt-versions/"+draft.ID+"/scan", nil,
		map[string]string{"id": draft.ID})
	if code != http.StatusOK {
		t.Fatalf("scan: expected 200, got %d: %s", code, raw)
	}
	var result struct {
		ScannerRevision string `json:"scanner_revision"`
		Passed          bool   `json:"passed"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode scan: %v (%s)", err, raw)
	}
	if !result.Passed {
		t.Fatalf("a clean prompt did not pass: %s", raw)
	}
	if result.ScannerRevision == "" {
		t.Error("a pass must name the detector revision that produced it")
	}

	var state string
	var version *int32
	dbfx.QueryRow(t, `SELECT state, version FROM marketplace_prompt_version WHERE id = $1`, draft.ID).
		Scan(&state, &version)
	if state != promptStateDraft {
		t.Fatalf("state = %q after a scan, want draft — scanning must not publish", state)
	}
	if version != nil {
		t.Fatalf("version = %d after a scan; a scan must not assign one", *version)
	}

	// Still editable, which is the whole point.
	code, raw = callPrompt(t, testHandler.UpdatePromptVersion, http.MethodPut,
		"/api/marketplace/prompt-versions/"+draft.ID,
		map[string]any{"name": "Renamed After Scanning", "summary": "still a draft", "license_code": "cc0"},
		map[string]string{"id": draft.ID})
	if code != http.StatusOK {
		t.Fatalf("the draft was not editable after a scan: %d: %s", code, raw)
	}
}

// A scan that finds a secret must block with the same body publishing blocks
// with, and must not carry the matched value anywhere in it.
func TestScanBlocksOnASecretWithoutLeakingIt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := promptTestAgent(t, "prompt-scan-secret",
		"Authenticate with this token: "+promptSecretText)
	draft := createPromptDraft(t, agentID, nil)

	code, raw := callPrompt(t, testHandler.ScanPromptVersion, http.MethodPost,
		"/api/marketplace/prompt-versions/"+draft.ID+"/scan", nil,
		map[string]string{"id": draft.ID})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 scanning a prompt with a secret, got %d: %s", code, raw)
	}
	if got := promptErrorCode(t, raw); got != "prompt_secret_detected" {
		t.Fatalf("code = %q, want prompt_secret_detected", got)
	}
	if strings.Contains(raw, promptSecretText) || strings.Contains(raw, promptSecretText[:8]) {
		t.Fatalf("the scan response carried the secret or a prefix of it: %s", raw)
	}
}

// ── Blocker 4: a withdrawal beats a consumer that has already passed its
// pre-check ─────────────────────────────────────────────────────────────────

// The simple ordering first: a withdrawal that has already committed before the
// request arrives. The pre-check alone was enough for this one, which is why it
// passed all along and why the race below is the test that matters.
func TestInstallAfterWithdrawalIsRefused(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, published := publishedPrompt(t, "prompt-race-install", "prompt text")

	if code, raw := callPrompt(t, testHandler.WithdrawPromptVersion, http.MethodPost,
		"/api/marketplace/prompt-versions/"+published.ID+"/withdraw", nil,
		map[string]string{"id": published.ID}); code != http.StatusOK {
		t.Fatalf("withdraw: expected 200, got %d: %s", code, raw)
	}

	code, raw := callPrompt(t, testHandler.InstallPrompt, http.MethodPost,
		"/api/marketplace/prompt-installations",
		map[string]any{"version_id": published.ID}, nil)
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 installing a withdrawn version, got %d: %s", code, raw)
	}
	count := dbfx.Count(t, `SELECT count(*) FROM workspace_prompt_install WHERE workspace_id = $1 AND series_id = $2`,
		testWorkspaceID, published.SeriesID)
	if count != 0 {
		t.Fatalf("a refused install still wrote %d rows", count)
	}
}

// The apply side of the same contract, and the one the pre-check could not
// cover: the install is legitimate and predates the withdrawal, so the apply
// gets all the way to its transaction before the version turns out to be gone.
// The in-transaction re-check is what makes the answer honest.
func TestApplyAfterWithdrawalIsRefusedAndWritesNothing(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const targetText = "the target's own prompt"
	_, published := publishedPrompt(t, "prompt-race-apply", "the marketplace prompt")
	install := installPrompt(t, published.ID)
	targetID := promptTestAgent(t, "prompt-race-apply-target", targetText)

	// Preview first: the token is minted while the version is still live, so
	// the apply below fails on the withdrawal and nothing else.
	code, raw := previewApply(t, install.ID, "agent", targetID)
	if code != http.StatusOK {
		t.Fatalf("preview: expected 200, got %d: %s", code, raw)
	}
	preview := decodePromptPreview(t, raw)

	if code, raw := callPrompt(t, testHandler.WithdrawPromptVersion, http.MethodPost,
		"/api/marketplace/prompt-versions/"+published.ID+"/withdraw", nil,
		map[string]string{"id": published.ID}); code != http.StatusOK {
		t.Fatalf("withdraw: expected 200, got %d: %s", code, raw)
	}

	code, raw = applyPrompt(t, install.ID, map[string]any{
		"target_type": "agent", "target_id": targetID, "strategy": "replace",
		"preview_token": preview.PreviewToken, "expected_sha256": preview.CurrentSha256,
		"operation_id": "op-race-apply",
	})
	if code != http.StatusConflict {
		t.Fatalf("expected 409 applying a withdrawn version, got %d: %s", code, raw)
	}
	if got := promptErrorCode(t, raw); got != "prompt_version_withdrawn" {
		t.Fatalf("code = %q, want prompt_version_withdrawn", got)
	}
	if got := agentInstructions(t, targetID); got != targetText {
		t.Fatalf("a refused apply rewrote the prompt: %q", got)
	}
}

// withdrawWhileBlocked runs a consumer handler against a version whose row is
// already locked by a transaction that is about to withdraw it.
//
// This is the actual race, not a re-ordering of it. The consumer starts while
// the version is still live and published, so its pre-transaction check passes;
// it then blocks on the version's row lock, and the withdrawal commits
// underneath it. A consumer that re-reads the version under that lock sees the
// withdrawal. One that trusts the check it did outside the transaction does
// not, and commits a consumption of a version its author had already pulled —
// which is what the pre-check alone could never prevent, and what no
// sequential test can catch.
func withdrawWhileBlocked(t *testing.T, versionID string, consumer func() (int, string)) (int, string) {
	t.Helper()

	tx, err := testPool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin the withdrawing transaction: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()

	if _, err := tx.Exec(t.Context(),
		`SELECT id FROM marketplace_prompt_version WHERE id = $1 FOR UPDATE`, versionID); err != nil {
		t.Fatalf("lock the version row: %v", err)
	}

	type result struct {
		code int
		raw  string
	}
	done := make(chan result, 1)
	go func() {
		code, raw := consumer()
		done <- result{code, raw}
	}()

	// Wait for the consumer to actually block on this row rather than guessing
	// with a sleep: until it does, committing the withdrawal would just be the
	// sequential ordering the tests above already cover.
	waitForBlockedOnVersionRow(t)

	if _, err := tx.Exec(t.Context(),
		`UPDATE marketplace_prompt_version
		 SET state = 'withdrawn', withdrawn_at = now(), updated_at = now()
		 WHERE id = $1`, versionID); err != nil {
		t.Fatalf("withdraw under the lock: %v", err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit the withdrawal: %v", err)
	}
	committed = true

	select {
	case r := <-done:
		return r.code, r.raw
	case <-time.After(10 * time.Second):
		t.Fatal("the consumer never finished after the withdrawal committed")
		return 0, ""
	}
}

// waitForBlockedOnVersionRow polls until some backend is waiting on a lock. If
// nothing ever blocks, the consumer never took the version's row lock at all —
// which is itself the defect, so this fails rather than hanging.
func waitForBlockedOnVersionRow(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := testPool.QueryRow(t.Context(), `
			SELECT count(*) FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND datname = current_database()
		`).Scan(&waiting); err == nil && waiting > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no request ever blocked on the version's row lock; the consumer is not taking it")
}

// The install half of the race.
func TestInstallLosesToAWithdrawalCommittingUnderneathIt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, published := publishedPrompt(t, "prompt-race-install-live", "prompt text")

	code, raw := withdrawWhileBlocked(t, published.ID, func() (int, string) {
		return callPrompt(t, testHandler.InstallPrompt, http.MethodPost,
			"/api/marketplace/prompt-installations",
			map[string]any{"version_id": published.ID}, nil)
	})
	if code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s — the install committed after the withdrawal", code, raw)
	}

	count := dbfx.Count(t, `SELECT count(*) FROM workspace_prompt_install WHERE workspace_id = $1 AND series_id = $2`,
		testWorkspaceID, published.SeriesID)
	if count != 0 {
		t.Fatalf("a version withdrawn mid-install was installed anyway: %d rows", count)
	}
}

// The apply half. Its install is legitimate and predates the withdrawal
// entirely, so nothing before the transaction can tell it to stop.
func TestApplyLosesToAWithdrawalCommittingUnderneathIt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const targetText = "the target's own prompt"
	_, published := publishedPrompt(t, "prompt-race-apply-live", "the marketplace prompt")
	install := installPrompt(t, published.ID)
	targetID := promptTestAgent(t, "prompt-race-apply-live-target", targetText)

	_, raw := previewApply(t, install.ID, "agent", targetID)
	preview := decodePromptPreview(t, raw)

	code, raw := withdrawWhileBlocked(t, published.ID, func() (int, string) {
		return applyPrompt(t, install.ID, map[string]any{
			"target_type": "agent", "target_id": targetID, "strategy": "replace",
			"preview_token": preview.PreviewToken, "expected_sha256": preview.CurrentSha256,
			"operation_id": "op-race-apply-live",
		})
	})
	if code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s — the apply committed after the withdrawal", code, raw)
	}
	if got := promptErrorCode(t, raw); got != "prompt_version_withdrawn" {
		t.Fatalf("code = %q, want prompt_version_withdrawn", got)
	}
	if got := agentInstructions(t, targetID); got != targetText {
		t.Fatalf("a version withdrawn mid-apply was written anyway: %q", got)
	}
}

// The version row is the lock both writers take, so withdrawing twice is a
// retry rather than a deadlock or a 409 the client cannot act on.
func TestWithdrawingTwiceIsIdempotent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, published := publishedPrompt(t, "prompt-withdraw-twice", "prompt text")

	for i, label := range []string{"first", "second"} {
		code, raw := callPrompt(t, testHandler.WithdrawPromptVersion, http.MethodPost,
			"/api/marketplace/prompt-versions/"+published.ID+"/withdraw", nil,
			map[string]string{"id": published.ID})
		if code != http.StatusOK {
			t.Fatalf("%s withdraw (call %d): expected 200, got %d: %s", label, i+1, code, raw)
		}
		if got := decodePromptVersion(t, raw).State; got != promptStateWithdrawn {
			t.Fatalf("%s withdraw returned state %q", label, got)
		}
	}
}

// ── Blocker 5: an originally-empty prompt is restorable ─────────────────────

// The regression: the restore point was recorded as the previous text, and
// "was there a restore point" was answered by asking whether that text was
// non-empty. Applying onto an empty agent — the single most common first
// use of the feature — therefore looked exactly like never having applied at
// all, and the undo button was gone the moment it was needed.
func TestRestoreUndoesAnApplyOntoAnEmptyPrompt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const sourceText = "the marketplace prompt body"
	install, targetID := installedPromptFixture(t, "prompt-restore-empty", sourceText)

	_, raw := previewApply(t, install.ID, "agent", targetID)
	preview := decodePromptPreview(t, raw)
	code, raw := applyPrompt(t, install.ID, map[string]any{
		"target_type": "agent", "target_id": targetID,
		"preview_token": preview.PreviewToken, "expected_sha256": preview.CurrentSha256,
		"operation_id": "op-restore-empty-apply",
	})
	if code != http.StatusOK {
		t.Fatalf("apply: expected 200, got %d: %s", code, raw)
	}
	var applied PromptApplyResponse
	if err := json.Unmarshal([]byte(raw), &applied); err != nil {
		t.Fatalf("decode apply: %v (%s)", err, raw)
	}
	if !applied.CanRestore {
		t.Fatal("applying onto an empty prompt must still offer the undo")
	}

	// The status strip has to agree, or the button is never rendered to click.
	code, raw = callPrompt(t, testHandler.GetPromptTargetState, http.MethodGet,
		"/api/marketplace/prompt-targets/agent/"+targetID, nil,
		map[string]string{"type": "agent", "id": targetID})
	if code != http.StatusOK {
		t.Fatalf("state: expected 200, got %d: %s", code, raw)
	}
	var state struct {
		CanRestore           bool `json:"can_restore"`
		AppliedContentIntact bool `json:"applied_content_intact"`
	}
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatalf("decode state: %v (%s)", err, raw)
	}
	if !state.CanRestore {
		t.Fatalf("the status strip reports no undo available: %s", raw)
	}

	// One step, back to empty.
	code, raw = callPrompt(t, testHandler.RestorePrompt, http.MethodPost,
		"/api/marketplace/prompt-targets/agent/"+targetID+"/restore",
		map[string]any{"operation_id": "op-restore-empty-undo"},
		map[string]string{"type": "agent", "id": targetID})
	if code != http.StatusOK {
		t.Fatalf("restore: expected 200, got %d: %s", code, raw)
	}
	if got := agentInstructions(t, targetID); got != "" {
		t.Fatalf("instructions = %q, want the empty prompt the target started with", got)
	}

	// And still one step, not a stack: there is nothing left behind it.
	code, raw = callPrompt(t, testHandler.RestorePrompt, http.MethodPost,
		"/api/marketplace/prompt-targets/agent/"+targetID+"/restore",
		map[string]any{"operation_id": "op-restore-empty-again"},
		map[string]string{"type": "agent", "id": targetID})
	if code != http.StatusConflict {
		t.Fatalf("expected 409 on a second restore, got %d: %s", code, raw)
	}
}

// ── REVIEW NOTE follow-up: authority is re-checked under the row lock ───────

// The window the review registered as a note rather than a blocker: the
// caller's authority over the target was checked before the transaction, and
// the transaction never looked again. Access lost in between — a member
// removed from the workspace, or demoted, mid-request — left an apply to
// commit on authority that no longer existed.
//
// Driven the same way as the withdrawal race: the request blocks on the
// target's row lock while the membership is revoked underneath it, so the
// out-of-transaction check has already passed by the time the revocation
// commits.
func TestApplyLosesToMembershipRevokedUnderneathIt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const targetText = "the target's own prompt"
	install, _ := installedPromptFixture(t, "prompt-race-perm", "the marketplace prompt")
	targetID := promptTestAgent(t, "prompt-race-perm-real-target", targetText)

	_, raw := previewApply(t, install.ID, "agent", targetID)
	preview := decodePromptPreview(t, raw)

	tx, err := testPool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin the revoking transaction: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()
	// Hold the target's row so the apply blocks exactly where the re-check has
	// to happen.
	if _, err := tx.Exec(t.Context(), `SELECT id FROM agent WHERE id = $1 FOR UPDATE`, targetID); err != nil {
		t.Fatalf("lock the target row: %v", err)
	}

	type result struct {
		code int
		raw  string
	}
	done := make(chan result, 1)
	go func() {
		code, raw := applyPrompt(t, install.ID, map[string]any{
			"target_type": "agent", "target_id": targetID, "strategy": "replace",
			"preview_token": preview.PreviewToken, "expected_sha256": preview.CurrentSha256,
			"operation_id": "op-race-perm",
		})
		done <- result{code, raw}
	}()
	waitForBlockedOnVersionRow(t)

	if _, err := tx.Exec(t.Context(),
		`DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`,
		testWorkspaceID, testUserID); err != nil {
		t.Fatalf("revoke the membership: %v", err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit the revocation: %v", err)
	}
	committed = true
	// Put the membership back however this test ends: every other test in the
	// package needs it.
	dbfx.Cleanup(t, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
		ON CONFLICT DO NOTHING`, testWorkspaceID, testUserID)

	var got result
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the apply never finished after the revocation committed")
	}
	if got.code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s — the apply committed on revoked authority", got.code, got.raw)
	}
	if instructions := agentInstructions(t, targetID); instructions != targetText {
		t.Fatalf("a caller who lost access still rewrote the prompt: %q", instructions)
	}
}

// The hand-edit guard is not weakened by the fix: an empty target that was
// applied to and then edited by hand still refuses, because refusing is what
// stops the undo from discarding the edit.
func TestRestoreAfterEmptyApplyStillRefusesAHandEdit(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	install, targetID := installedPromptFixture(t, "prompt-restore-empty-edit", "the marketplace prompt body")

	_, raw := previewApply(t, install.ID, "agent", targetID)
	preview := decodePromptPreview(t, raw)
	if code, raw := applyPrompt(t, install.ID, map[string]any{
		"target_type": "agent", "target_id": targetID,
		"preview_token": preview.PreviewToken, "expected_sha256": preview.CurrentSha256,
		"operation_id": "op-restore-empty-edit-apply",
	}); code != http.StatusOK {
		t.Fatalf("apply: expected 200, got %d: %s", code, raw)
	}

	const handEdited = "the marketplace prompt body, plus my own additions"
	dbfx.Exec(t, `UPDATE agent SET instructions = $1 WHERE id = $2`, handEdited, targetID)

	code, raw := callPrompt(t, testHandler.RestorePrompt, http.MethodPost,
		"/api/marketplace/prompt-targets/agent/"+targetID+"/restore",
		map[string]any{"operation_id": "op-restore-empty-edit-undo"},
		map[string]string{"type": "agent", "id": targetID})
	if code != http.StatusConflict {
		t.Fatalf("expected 409 restoring over a hand edit, got %d: %s", code, raw)
	}
	if got := promptErrorCode(t, raw); got != "prompt_target_modified" {
		t.Fatalf("code = %q, want prompt_target_modified", got)
	}
	if got := agentInstructions(t, targetID); got != handEdited {
		t.Fatalf("restore discarded the hand edit: %q", got)
	}
}
