package handler

// Consumer-side tests for the prompt marketplace (RUYI-100): install, preview,
// apply, restore.
//
// The acceptance conditions these pin are the ones a prompt marketplace lives
// or dies on — installing must not touch a prompt, applying must not overwrite
// without an explicit choice, a failed apply must leave nothing behind, and
// restore must refuse rather than discard a hand edit.

import (
	"encoding/json"
	"net/http"
	"testing"
)

func decodePromptInstall(t *testing.T, raw string) PromptInstallResponse {
	t.Helper()
	var resp PromptInstallResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("decode install: %v (%s)", err, raw)
	}
	return resp
}

func decodePromptPreview(t *testing.T, raw string) PreviewPromptApplyResponse {
	t.Helper()
	var resp PreviewPromptApplyResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("decode preview: %v (%s)", err, raw)
	}
	return resp
}

// installPrompt installs a published version into the test workspace.
func installPrompt(t *testing.T, versionID string) PromptInstallResponse {
	t.Helper()
	code, raw := callPrompt(t, testHandler.InstallPrompt, http.MethodPost,
		"/api/marketplace/prompt-installations", map[string]any{"version_id": versionID}, nil)
	if code != http.StatusOK {
		t.Fatalf("install: expected 200, got %d: %s", code, raw)
	}
	install := decodePromptInstall(t, raw)
	dbfx.Cleanup(t, `DELETE FROM workspace_prompt_install WHERE id = $1`, install.ID)
	return install
}

// previewApply renders the diff and returns the confirmation.
func previewApply(t *testing.T, installID, targetType, targetID string) (int, string) {
	t.Helper()
	return callPrompt(t, testHandler.PreviewPromptApply, http.MethodPost,
		"/api/marketplace/prompt-installations/"+installID+"/preview",
		map[string]any{"target_type": targetType, "target_id": targetID},
		map[string]string{"id": installID})
}

func applyPrompt(t *testing.T, installID string, body map[string]any) (int, string) {
	t.Helper()
	return callPrompt(t, testHandler.ApplyPrompt, http.MethodPost,
		"/api/marketplace/prompt-installations/"+installID+"/apply", body,
		map[string]string{"id": installID})
}

func agentInstructions(t *testing.T, agentID string) string {
	t.Helper()
	var instructions string
	dbfx.QueryRow(t, `SELECT instructions FROM agent WHERE id = $1`, agentID).Scan(&instructions)
	return instructions
}

// installedPromptFixture is the standard consumer setup: a published prompt,
// installed, plus an empty target agent to apply it to.
func installedPromptFixture(t *testing.T, name, sourceText string) (install PromptInstallResponse, targetID string) {
	t.Helper()
	_, published := publishedPrompt(t, name+"-source", sourceText)
	install = installPrompt(t, published.ID)
	targetID = promptTestAgent(t, name+"-target", "")
	return install, targetID
}

// ── A7 / Owner baseline 7: install is not apply ─────────────────────────────

// The single most important property of the two-phase design: installing adds
// a library row and changes no agent's prompt.
func TestInstallDoesNotTouchAnyPrompt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const targetText = "the target's own prompt"
	_, published := publishedPrompt(t, "prompt-install-noapply", "the marketplace prompt")
	targetID := promptTestAgent(t, "prompt-install-target", targetText)

	install := installPrompt(t, published.ID)

	if install.Version != 1 {
		t.Errorf("installed version = %d, want 1", install.Version)
	}
	if install.ContentSha256 != published.ContentSha256 {
		t.Error("the install pointer does not match the published snapshot")
	}
	if got := agentInstructions(t, targetID); got != targetText {
		t.Fatalf("installing rewrote a prompt: %q", got)
	}
}

// Re-installing the same series is idempotent: the workspace library holds one
// row per asset, so a double click cannot fork it.
func TestInstallIsIdempotentPerSeries(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, published := publishedPrompt(t, "prompt-install-idem", "prompt text")

	first := installPrompt(t, published.ID)
	second := installPrompt(t, published.ID)
	if first.ID != second.ID {
		t.Fatalf("re-install created a second row: %s vs %s", first.ID, second.ID)
	}

	count := dbfx.Count(t, `SELECT count(*) FROM workspace_prompt_install WHERE workspace_id = $1 AND series_id = $2`,
		testWorkspaceID, published.SeriesID)
	if count != 1 {
		t.Fatalf("library holds %d rows for one series, want 1", count)
	}
}

// D3: installing writes to a shared workspace library, so it takes the same
// human owner/admin authority that adding an MCP server by hand takes.
func TestInstallRequiresOwnerOrAdmin(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, published := publishedPrompt(t, "prompt-install-role", "prompt text")

	dbfx.Exec(t, `UPDATE member SET role = 'member' WHERE workspace_id = $1 AND user_id = $2`,
		testWorkspaceID, testUserID)
	dbfx.Cleanup(t, `UPDATE member SET role = 'owner' WHERE workspace_id = $1 AND user_id = $2`,
		testWorkspaceID, testUserID)

	code, raw := callPrompt(t, testHandler.InstallPrompt, http.MethodPost,
		"/api/marketplace/prompt-installations", map[string]any{"version_id": published.ID}, nil)
	if code != http.StatusForbidden {
		t.Fatalf("expected 403 for a plain member, got %d: %s", code, raw)
	}
}

// ── A8: applying an empty target is the simple path ─────────────────────────

func TestApplyToEmptyTargetWritesPrompt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const sourceText = "the marketplace prompt body"
	install, targetID := installedPromptFixture(t, "prompt-apply-empty", sourceText)

	code, raw := previewApply(t, install.ID, "agent", targetID)
	if code != http.StatusOK {
		t.Fatalf("preview: expected 200, got %d: %s", code, raw)
	}
	preview := decodePromptPreview(t, raw)
	if !preview.TargetEmpty {
		t.Error("an empty target must be reported as empty")
	}
	if preview.RequiresConfirmation {
		t.Error("an empty target has nothing to lose and must not demand a conflict choice")
	}
	if preview.IncomingText != sourceText {
		t.Errorf("incoming = %q, want the installed snapshot", preview.IncomingText)
	}

	code, raw = applyPrompt(t, install.ID, map[string]any{
		"target_type": "agent", "target_id": targetID,
		"preview_token": preview.PreviewToken, "expected_sha256": preview.CurrentSha256,
		"operation_id": "op-apply-empty",
	})
	if code != http.StatusOK {
		t.Fatalf("apply: expected 200, got %d: %s", code, raw)
	}
	if got := agentInstructions(t, targetID); got != sourceText {
		t.Fatalf("instructions = %q, want the applied prompt", got)
	}
}

// ── A9: a non-empty target is preserved by default ──────────────────────────

// The default-preserve rule, and the reason install and apply are separate:
// existing prompt text is never overwritten without an explicit choice.
func TestApplyPreservesExistingPromptByDefault(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const existing = "the workspace's own carefully tuned prompt"
	_, published := publishedPrompt(t, "prompt-preserve-source", "the marketplace prompt")
	install := installPrompt(t, published.ID)
	targetID := promptTestAgent(t, "prompt-preserve-target", existing)

	code, raw := previewApply(t, install.ID, "agent", targetID)
	if code != http.StatusOK {
		t.Fatalf("preview: expected 200, got %d: %s", code, raw)
	}
	preview := decodePromptPreview(t, raw)
	if !preview.RequiresConfirmation {
		t.Error("a non-empty target must be flagged as needing confirmation")
	}
	// The diff has both sides so the user can actually see what they are
	// trading away.
	if preview.CurrentContent != existing {
		t.Errorf("preview current = %q, want the target's own text", preview.CurrentContent)
	}

	// Default strategy — the field is omitted entirely, which is the case a
	// forgetful client hits.
	code, raw = applyPrompt(t, install.ID, map[string]any{
		"target_type": "agent", "target_id": targetID,
		"preview_token": preview.PreviewToken, "operation_id": "op-preserve",
	})
	if code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", code, raw)
	}
	if got := agentInstructions(t, targetID); got != existing {
		t.Fatalf("a refused apply still changed the prompt: %q", got)
	}
}

// The overwrite is available, but only as an explicit choice carrying a preview
// token for the exact text being replaced.
func TestApplyReplacesOnlyWithExplicitStrategy(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const existing = "the previous prompt"
	const incoming = "the marketplace prompt"
	_, published := publishedPrompt(t, "prompt-replace-source", incoming)
	install := installPrompt(t, published.ID)
	targetID := promptTestAgent(t, "prompt-replace-target", existing)

	_, raw := previewApply(t, install.ID, "agent", targetID)
	preview := decodePromptPreview(t, raw)

	code, raw := applyPrompt(t, install.ID, map[string]any{
		"target_type": "agent", "target_id": targetID, "strategy": "replace",
		"preview_token": preview.PreviewToken, "expected_sha256": preview.CurrentSha256,
		"operation_id": "op-replace",
	})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", code, raw)
	}
	if got := agentInstructions(t, targetID); got != incoming {
		t.Fatalf("instructions = %q, want the replacement", got)
	}
}

// ── A10: concurrent drift forces a re-preview ───────────────────────────────

// The concurrency check. If the target moves between preview and apply, the
// diff the user confirmed is no longer the diff that would be applied, so the
// write is refused rather than silently overwriting an unseen edit.
func TestApplyRefusesWhenTargetChangedSincePreview(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	install, targetID := installedPromptFixture(t, "prompt-drift", "marketplace prompt")

	_, raw := previewApply(t, install.ID, "agent", targetID)
	preview := decodePromptPreview(t, raw)

	// Someone else edits the target after the diff was rendered.
	const drifted = "an edit the confirming user never saw"
	dbfx.Exec(t, `UPDATE agent SET instructions = $1 WHERE id = $2`, drifted, targetID)

	code, raw := applyPrompt(t, install.ID, map[string]any{
		"target_type": "agent", "target_id": targetID, "strategy": "replace",
		"preview_token": preview.PreviewToken, "expected_sha256": preview.CurrentSha256,
		"operation_id": "op-drift",
	})
	if code != http.StatusConflict {
		t.Fatalf("expected 409 on drift, got %d: %s", code, raw)
	}
	if got := agentInstructions(t, targetID); got != drifted {
		t.Fatalf("the refused apply overwrote the concurrent edit: %q", got)
	}
}

// A stale or forged token is refused even when the caller supplies the correct
// expected hash: the token is what proves a human saw this specific diff.
func TestApplyRejectsMissingOrForgedPreviewToken(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	install, targetID := installedPromptFixture(t, "prompt-token", "marketplace prompt")

	_, raw := previewApply(t, install.ID, "agent", targetID)
	preview := decodePromptPreview(t, raw)

	for _, tc := range []struct {
		name  string
		token string
	}{
		{"missing", ""},
		{"garbage", "not-a-token"},
		{"forged signature", "99999999999.deadbeef"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, raw := applyPrompt(t, install.ID, map[string]any{
				"target_type": "agent", "target_id": targetID,
				"preview_token": tc.token, "expected_sha256": preview.CurrentSha256,
				"operation_id": "op-token-" + tc.name,
			})
			if code != http.StatusConflict {
				t.Fatalf("expected 409, got %d: %s", code, raw)
			}
			if got := agentInstructions(t, targetID); got != "" {
				t.Fatalf("an apply without a valid token wrote %q", got)
			}
		})
	}
}

// A token minted for one target must not authorise a write to another — the
// signature binds the target, not just the install.
func TestPreviewTokenIsBoundToItsTarget(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	install, targetA := installedPromptFixture(t, "prompt-token-bound", "marketplace prompt")
	targetB := promptTestAgent(t, "prompt-token-bound-other", "")

	_, raw := previewApply(t, install.ID, "agent", targetA)
	preview := decodePromptPreview(t, raw)

	code, raw := applyPrompt(t, install.ID, map[string]any{
		"target_type": "agent", "target_id": targetB,
		"preview_token": preview.PreviewToken, "operation_id": "op-bound",
	})
	if code != http.StatusConflict {
		t.Fatalf("expected 409 reusing a token across targets, got %d: %s", code, raw)
	}
	if got := agentInstructions(t, targetB); got != "" {
		t.Fatalf("a token for another target wrote %q", got)
	}
}

// ── A11: kind matching ──────────────────────────────────────────────────────

func TestApplyRejectsCrossKindTarget(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, published := publishedPrompt(t, "prompt-kind-mismatch", "an agent prompt")
	install := installPrompt(t, published.ID)
	squadID := promptTestSquad(t, "prompt-kind-mismatch-squad", "the squad's prompt")

	code, raw := previewApply(t, install.ID, "squad", squadID)
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400 previewing an agent prompt onto a squad, got %d: %s", code, raw)
	}
}

// ── A12: apply is atomic and retry-safe ─────────────────────────────────────

// A failed apply must leave no partial write: neither the prompt nor the state
// it is paired with may move.
func TestFailedApplyLeavesNoPartialWrite(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const existing = "untouched prompt"
	_, published := publishedPrompt(t, "prompt-atomic-source", "marketplace prompt")
	install := installPrompt(t, published.ID)
	targetID := promptTestAgent(t, "prompt-atomic-target", existing)

	_, raw := previewApply(t, install.ID, "agent", targetID)
	preview := decodePromptPreview(t, raw)

	// preserve against a non-empty target: refused after the transaction has
	// opened and locked the row.
	code, raw := applyPrompt(t, install.ID, map[string]any{
		"target_type": "agent", "target_id": targetID,
		"preview_token": preview.PreviewToken, "operation_id": "op-atomic",
	})
	if code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", code, raw)
	}

	var instructions string
	var state []byte
	dbfx.QueryRow(t, `SELECT instructions, marketplace_prompt_state FROM agent WHERE id = $1`, targetID).
		Scan(&instructions, &state)
	if instructions != existing {
		t.Fatalf("instructions = %q, want unchanged", instructions)
	}
	if string(state) != "{}" {
		t.Fatalf("a failed apply wrote state %s", state)
	}
}

// Retrying the same operation id must not re-capture the restore point: the
// second apply would otherwise record the just-applied text as "previous",
// destroying the undo.
func TestApplyRetryWithSameOperationIDPreservesRestorePoint(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const existing = "the original prompt"
	const incoming = "the marketplace prompt"
	_, published := publishedPrompt(t, "prompt-retry-source", incoming)
	install := installPrompt(t, published.ID)
	targetID := promptTestAgent(t, "prompt-retry-target", existing)

	_, raw := previewApply(t, install.ID, "agent", targetID)
	preview := decodePromptPreview(t, raw)

	body := map[string]any{
		"target_type": "agent", "target_id": targetID, "strategy": "replace",
		"preview_token": preview.PreviewToken, "expected_sha256": preview.CurrentSha256,
		"operation_id": "op-retry-once",
	}
	if code, raw := applyPrompt(t, install.ID, body); code != http.StatusOK {
		t.Fatalf("first apply: expected 200, got %d: %s", code, raw)
	}
	// The retry a client makes after a dropped response.
	if code, raw := applyPrompt(t, install.ID, body); code != http.StatusOK {
		t.Fatalf("retry: expected 200, got %d: %s", code, raw)
	}

	var state []byte
	dbfx.QueryRow(t, `SELECT marketplace_prompt_state FROM agent WHERE id = $1`, targetID).Scan(&state)
	var decoded promptApplyState
	if err := json.Unmarshal(state, &decoded); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if decoded.PreviousText != existing {
		t.Fatalf("previous_text = %q, want the pre-apply prompt — the retry destroyed the restore point",
			decoded.PreviousText)
	}
}

// ── A13: one-step restore ───────────────────────────────────────────────────

func TestRestoreReturnsThePreviousPrompt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const existing = "the prompt the workspace had before"
	const incoming = "the marketplace prompt"
	_, published := publishedPrompt(t, "prompt-restore-source", incoming)
	install := installPrompt(t, published.ID)
	targetID := promptTestAgent(t, "prompt-restore-target", existing)

	_, raw := previewApply(t, install.ID, "agent", targetID)
	preview := decodePromptPreview(t, raw)
	if code, raw := applyPrompt(t, install.ID, map[string]any{
		"target_type": "agent", "target_id": targetID, "strategy": "replace",
		"preview_token": preview.PreviewToken, "expected_sha256": preview.CurrentSha256,
		"operation_id": "op-restore-apply",
	}); code != http.StatusOK {
		t.Fatalf("apply: expected 200, got %d: %s", code, raw)
	}
	if got := agentInstructions(t, targetID); got != incoming {
		t.Fatalf("apply did not take effect: %q", got)
	}

	code, raw := callPrompt(t, testHandler.RestorePrompt, http.MethodPost,
		"/api/marketplace/prompt-targets/agent/"+targetID+"/restore",
		map[string]any{"operation_id": "op-restore-1"},
		map[string]string{"type": "agent", "id": targetID})
	if code != http.StatusOK {
		t.Fatalf("restore: expected 200, got %d: %s", code, raw)
	}
	if got := agentInstructions(t, targetID); got != existing {
		t.Fatalf("instructions = %q, want the pre-apply prompt", got)
	}

	// One step back, not a stack: a second restore has nothing left to undo.
	code, raw = callPrompt(t, testHandler.RestorePrompt, http.MethodPost,
		"/api/marketplace/prompt-targets/agent/"+targetID+"/restore",
		map[string]any{"operation_id": "op-restore-2"},
		map[string]string{"type": "agent", "id": targetID})
	if code != http.StatusConflict {
		t.Fatalf("expected 409 on a second restore, got %d: %s", code, raw)
	}
	if got := agentInstructions(t, targetID); got != existing {
		t.Fatalf("the refused second restore changed the prompt: %q", got)
	}
}

// The hand-edit guard: restoring after the user edited the applied prompt would
// silently discard that edit, so it is refused and the choice left to them.
func TestRestoreRefusesAfterTargetWasHandEdited(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const existing = "the original prompt"
	const incoming = "the marketplace prompt"
	_, published := publishedPrompt(t, "prompt-handedit-source", incoming)
	install := installPrompt(t, published.ID)
	targetID := promptTestAgent(t, "prompt-handedit-target", existing)

	_, raw := previewApply(t, install.ID, "agent", targetID)
	preview := decodePromptPreview(t, raw)
	if code, raw := applyPrompt(t, install.ID, map[string]any{
		"target_type": "agent", "target_id": targetID, "strategy": "replace",
		"preview_token": preview.PreviewToken, "expected_sha256": preview.CurrentSha256,
		"operation_id": "op-handedit-apply",
	}); code != http.StatusOK {
		t.Fatalf("apply: expected 200, got %d: %s", code, raw)
	}

	const handEdited = "the marketplace prompt, plus my own additions"
	dbfx.Exec(t, `UPDATE agent SET instructions = $1 WHERE id = $2`, handEdited, targetID)

	code, raw := callPrompt(t, testHandler.RestorePrompt, http.MethodPost,
		"/api/marketplace/prompt-targets/agent/"+targetID+"/restore",
		map[string]any{"operation_id": "op-handedit-restore"},
		map[string]string{"type": "agent", "id": targetID})
	if code != http.StatusConflict {
		t.Fatalf("expected 409 restoring over a hand edit, got %d: %s", code, raw)
	}
	if got := agentInstructions(t, targetID); got != handEdited {
		t.Fatalf("restore discarded the hand edit: %q", got)
	}
}

func TestRestoreRefusesWhenNothingWasApplied(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	targetID := promptTestAgent(t, "prompt-restore-none", "a hand-written prompt")

	code, raw := callPrompt(t, testHandler.RestorePrompt, http.MethodPost,
		"/api/marketplace/prompt-targets/agent/"+targetID+"/restore",
		map[string]any{"operation_id": "op-none"},
		map[string]string{"type": "agent", "id": targetID})
	if code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", code, raw)
	}
	if got := agentInstructions(t, targetID); got != "a hand-written prompt" {
		t.Fatalf("instructions = %q, want unchanged", got)
	}
}

// ── A14: updating to a newer version ────────────────────────────────────────

// Installing a newer version of an already-installed series IS the update path;
// applying it is still a separate, confirmed step.
func TestUpdateInstallsNewerVersionWithoutApplying(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, v1 := publishedPrompt(t, "prompt-update-source", "version one text")
	install := installPrompt(t, v1.ID)
	targetID := promptTestAgent(t, "prompt-update-target", "")

	// Apply v1 so the target is running the marketplace prompt.
	_, raw := previewApply(t, install.ID, "agent", targetID)
	preview := decodePromptPreview(t, raw)
	if code, raw := applyPrompt(t, install.ID, map[string]any{
		"target_type": "agent", "target_id": targetID,
		"preview_token": preview.PreviewToken, "operation_id": "op-update-v1",
	}); code != http.StatusOK {
		t.Fatalf("apply v1: expected 200, got %d: %s", code, raw)
	}

	// Publisher ships v2.
	dbfx.Exec(t, `UPDATE agent SET instructions = $1 WHERE id = $2`, "version two text", agentID)
	draft2 := createPromptDraft(t, agentID, map[string]any{"series_id": v1.SeriesID})
	v2 := publishPromptDraft(t, draft2.ID)

	// The catalog offers the update.
	code, raw := callPrompt(t, testHandler.ListPromptVersions, http.MethodGet,
		"/api/marketplace/prompt-versions", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("discovery: expected 200, got %d: %s", code, raw)
	}
	var items []PromptMarketItemResponse
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	var found bool
	for _, item := range items {
		if item.SeriesID != v1.SeriesID {
			continue
		}
		found = true
		if !item.Installed {
			t.Error("the entry is not marked installed")
		}
		if !item.UpdateAvailable {
			t.Error("a newer published version must be offered as an update")
		}
	}
	if !found {
		t.Fatal("the installed series is missing from discovery")
	}

	// Taking the update refreshes the pointer and still leaves the prompt alone.
	updated := installPrompt(t, v2.ID)
	if updated.Version != 2 {
		t.Fatalf("installed version = %d, want 2", updated.Version)
	}
	if got := agentInstructions(t, targetID); got != "version one text" {
		t.Fatalf("taking an update rewrote the prompt to %q; applying must stay a separate step", got)
	}
}

// ── S2: squad targets ───────────────────────────────────────────────────────

func TestSquadPromptAppliesAndRestores(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const sourceText = "Route work to the right specialist."
	const existing = "the squad's original prompt"
	sourceSquad := promptTestSquad(t, "prompt-squad-apply-source", sourceText)

	code, raw := callPrompt(t, testHandler.CreatePromptVersion, http.MethodPost,
		"/api/marketplace/prompt-versions", map[string]any{
			"source_type": "squad", "source_id": sourceSquad,
			"name": "Squad Router", "license_code": "cc0",
		}, nil)
	if code != http.StatusCreated {
		t.Fatalf("squad draft: expected 201, got %d: %s", code, raw)
	}
	draft := decodePromptVersion(t, raw)
	dbfx.Cleanup(t, `DELETE FROM marketplace_prompt_version WHERE series_id = $1`, draft.SeriesID)
	published := publishPromptDraft(t, draft.ID)

	install := installPrompt(t, published.ID)
	targetSquad := promptTestSquad(t, "prompt-squad-apply-target", existing)

	code, raw = previewApply(t, install.ID, "squad", targetSquad)
	if code != http.StatusOK {
		t.Fatalf("preview: expected 200, got %d: %s", code, raw)
	}
	preview := decodePromptPreview(t, raw)

	code, raw = applyPrompt(t, install.ID, map[string]any{
		"target_type": "squad", "target_id": targetSquad, "strategy": "replace",
		"preview_token": preview.PreviewToken, "expected_sha256": preview.CurrentSha256,
		"operation_id": "op-squad-apply",
	})
	if code != http.StatusOK {
		t.Fatalf("apply: expected 200, got %d: %s", code, raw)
	}
	var instructions string
	dbfx.QueryRow(t, `SELECT instructions FROM squad WHERE id = $1`, targetSquad).Scan(&instructions)
	if instructions != sourceText {
		t.Fatalf("squad instructions = %q, want the applied prompt", instructions)
	}

	code, raw = callPrompt(t, testHandler.RestorePrompt, http.MethodPost,
		"/api/marketplace/prompt-targets/squad/"+targetSquad+"/restore",
		map[string]any{"operation_id": "op-squad-restore"},
		map[string]string{"type": "squad", "id": targetSquad})
	if code != http.StatusOK {
		t.Fatalf("restore: expected 200, got %d: %s", code, raw)
	}
	dbfx.QueryRow(t, `SELECT instructions FROM squad WHERE id = $1`, targetSquad).Scan(&instructions)
	if instructions != existing {
		t.Fatalf("squad instructions = %q, want the pre-apply prompt", instructions)
	}
}

// ── Target state ────────────────────────────────────────────────────────────

// What the status strip reads: which version is applied, and whether the undo
// is still available.
func TestTargetStateReportsAppliedVersionAndRestorability(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const existing = "the original"
	_, published := publishedPrompt(t, "prompt-state-source", "the marketplace prompt")
	install := installPrompt(t, published.ID)
	targetID := promptTestAgent(t, "prompt-state-target", existing)

	// Before any apply.
	code, raw := callPrompt(t, testHandler.GetPromptTargetState, http.MethodGet,
		"/api/marketplace/prompt-targets/agent/"+targetID, nil,
		map[string]string{"type": "agent", "id": targetID})
	if code != http.StatusOK {
		t.Fatalf("state: expected 200, got %d: %s", code, raw)
	}
	var before map[string]any
	json.Unmarshal([]byte(raw), &before)
	if before["applied_version"] != nil {
		t.Errorf("applied_version = %v, want null before any apply", before["applied_version"])
	}
	if before["can_restore"] != false {
		t.Error("can_restore must be false before any apply")
	}

	_, raw = previewApply(t, install.ID, "agent", targetID)
	preview := decodePromptPreview(t, raw)
	if code, raw := applyPrompt(t, install.ID, map[string]any{
		"target_type": "agent", "target_id": targetID, "strategy": "replace",
		"preview_token": preview.PreviewToken, "expected_sha256": preview.CurrentSha256,
		"operation_id": "op-state",
	}); code != http.StatusOK {
		t.Fatalf("apply: expected 200, got %d: %s", code, raw)
	}

	code, raw = callPrompt(t, testHandler.GetPromptTargetState, http.MethodGet,
		"/api/marketplace/prompt-targets/agent/"+targetID, nil,
		map[string]string{"type": "agent", "id": targetID})
	if code != http.StatusOK {
		t.Fatalf("state: expected 200, got %d: %s", code, raw)
	}
	var after map[string]any
	json.Unmarshal([]byte(raw), &after)
	if after["applied_version"] != float64(1) {
		t.Errorf("applied_version = %v, want 1", after["applied_version"])
	}
	if after["can_restore"] != true {
		t.Error("can_restore must be true right after an apply")
	}
	if after["applied_content_intact"] != true {
		t.Error("applied_content_intact must be true before any hand edit")
	}

	// A hand edit is exactly what makes restore refuse, and the status has to
	// say so before the user clicks.
	dbfx.Exec(t, `UPDATE agent SET instructions = $1 WHERE id = $2`, "hand edited", targetID)
	code, raw = callPrompt(t, testHandler.GetPromptTargetState, http.MethodGet,
		"/api/marketplace/prompt-targets/agent/"+targetID, nil,
		map[string]string{"type": "agent", "id": targetID})
	if code != http.StatusOK {
		t.Fatalf("state: expected 200, got %d: %s", code, raw)
	}
	var edited map[string]any
	json.Unmarshal([]byte(raw), &edited)
	if edited["applied_content_intact"] != false {
		t.Error("applied_content_intact must go false once the prompt is hand edited")
	}
	if edited["can_restore"] != false {
		t.Error("can_restore must go false once the prompt is hand edited")
	}
}
