package handler

// Prompt marketplace — consumer side (RUYI-100).
//
// Install, preview, apply, restore. The publisher's half is in
// marketplace_prompt.go.
//
// The central design decision here is Owner baseline 7: installing and applying
// are SEPARATE acts. Installing records "this workspace holds this version" and
// touches no agent and no squad. Applying writes text into one target, after a
// human has seen a diff. A single combined "install and use" step would make
// every install a silent prompt overwrite, which is the one outcome a prompt
// marketplace must not have.
//
// Everything that writes into a target is guarded by three things at once:
// live permission on the target, a preview token proving a human saw the diff
// that is about to be applied, and an expected content hash proving the target
// has not moved since. Any of the three failing means no write.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Conflict strategies. `preserve` is the default and never overwrites; only an
// explicit `replace`, carrying a preview token for the exact text being
// replaced, writes over existing content.
const (
	promptStrategyPreserve = "preserve"
	promptStrategyReplace  = "replace"
)

// ── Preview tokens ──────────────────────────────────────────────────────────
//
// A preview token is proof that this apply is the one the user confirmed. It
// binds the install, the target and — critically — the hash of the text the
// user was shown as the target's CURRENT content. If the target changed after
// the preview was rendered, the token no longer matches and the apply is
// refused rather than silently overwriting an edit the user never saw.
//
// It is signed rather than stored: the state it carries is entirely derivable
// and self-verifying, and a table of pending previews would need its own
// expiry sweep to say the same thing.

const (
	promptPreviewVersion = "v1"

	// promptPreviewTTL bounds how stale a confirmation may be. Long enough for
	// a human to read a diff and decide, short enough that a token left in a
	// tab overnight cannot be replayed against a prompt that has since changed.
	promptPreviewTTL = 15 * time.Minute

	// promptPreviewKeyDomain separates this signing domain from every other
	// HMAC derived from the deployment's root secret.
	promptPreviewKeyDomain = "marketplace-prompt-preview:"
)

var (
	promptPreviewKeyOnce sync.Once
	promptPreviewKey     []byte
)

// promptPreviewSigningKey derives the preview key from the deployment's JWT
// secret, mirroring attachmentCapabilitySigningKey. Deriving rather than
// reusing means a preview token can never be confused with a session token,
// and rotating JWT_SECRET invalidates outstanding previews — which is the right
// behaviour, since a rotation should not leave confirmations floating.
func promptPreviewSigningKey() []byte {
	promptPreviewKeyOnce.Do(func() {
		sum := sha256.Sum256(append([]byte(promptPreviewKeyDomain), auth.JWTSecret()...))
		promptPreviewKey = sum[:]
	})
	return promptPreviewKey
}

// signPromptPreview signs the fields that make one confirmation unique.
//
// currentHash is what makes the token a concurrency check rather than a
// formality: it is the hash of the target's content AT PREVIEW TIME, so the
// signature stops matching the moment anyone edits the target.
//
// Fields are joined with "|", which cannot occur in a UUID or a hex hash, so no
// two field tuples can produce the same signed message.
func signPromptPreview(installID, targetType, targetID, currentHash, incomingHash string, exp int64) string {
	mac := hmac.New(sha256.New, promptPreviewSigningKey())
	for _, part := range []string{
		promptPreviewVersion, installID, targetType, targetID,
		currentHash, incomingHash, strconv.FormatInt(exp, 10),
	} {
		mac.Write([]byte(part))
		mac.Write([]byte("|"))
	}
	return hex.EncodeToString(mac.Sum(nil))
}

// promptPreviewToken is the wire format: expiry and signature, joined by a dot.
func promptPreviewToken(installID, targetType, targetID, currentHash, incomingHash string, now time.Time) string {
	exp := now.Add(promptPreviewTTL).Unix()
	return strconv.FormatInt(exp, 10) + "." +
		signPromptPreview(installID, targetType, targetID, currentHash, incomingHash, exp)
}

// verifyPromptPreviewToken fails closed on every path: malformed token, missing
// half, unparseable or elapsed expiry, and a signature minted for a different
// install, target, current text or incoming text all return false.
func verifyPromptPreviewToken(token, installID, targetType, targetID, currentHash, incomingHash string, now time.Time) bool {
	rawExp, rawSig, found := strings.Cut(token, ".")
	if !found || rawExp == "" || rawSig == "" {
		return false
	}
	exp, err := strconv.ParseInt(rawExp, 10, 64)
	if err != nil || now.Unix() > exp {
		return false
	}
	got, err := hex.DecodeString(rawSig)
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(signPromptPreview(installID, targetType, targetID, currentHash, incomingHash, exp))
	if err != nil {
		return false
	}
	return hmac.Equal(got, want)
}

// ── Apply state ─────────────────────────────────────────────────────────────

// promptApplyState is the JSONB written onto agent.marketplace_prompt_state /
// squad.marketplace_prompt_state alongside the new instructions.
//
// PreviousText is the whole restore feature: exactly one previous version, kept
// so a user who applies a prompt and dislikes it can step back in one action.
// It is deliberately not a history — a second slot would imply a rollback stack
// this feature does not promise and cannot honour.
type promptApplyState struct {
	InstallID            string `json:"install_id"`
	VersionID            string `json:"version_id"`
	SeriesID             string `json:"series_id"`
	AppliedVersion       int32  `json:"applied_version"`
	AppliedContentSha256 string `json:"applied_content_sha256"`
	PreviousText         string `json:"previous_text"`
	PreviousUpdatedAt    string `json:"previous_updated_at"`
	AppliedBy            string `json:"applied_by"`
	AppliedAt            string `json:"applied_at"`
	LastOperationID      string `json:"last_operation_id"`

	// HasRestorePoint says whether PreviousText is a captured previous version
	// or merely absent. The two are NOT the same and cannot be told apart from
	// the text: applying onto an empty prompt captures "" as a perfectly
	// legitimate restore point, and testing PreviousText != "" reported that
	// user as having nothing to undo — the one case where the undo matters
	// most, since the alternative to restoring is guessing what was there.
	HasRestorePoint bool `json:"has_restore_point"`

	// Restored marks that the last operation on this target was a restore, not
	// an apply. PreviousText is cleared at that point — the text has been put
	// back, so there is nothing left to step back to — and this flag is what
	// distinguishes "already restored" from "never applied" when a retry with
	// the same operation id arrives.
	Restored bool `json:"restored"`
}

// canRestore is the single test for "is there a step back from here".
//
// The PreviousText fallback covers state written before HasRestorePoint
// existed: those rows only ever recorded a restore point when the text was
// non-empty, so the old test is exactly right for them and exactly wrong for
// everything written since.
func (s promptApplyState) canRestore() bool {
	if s.Restored {
		return false
	}
	return s.HasRestorePoint || s.PreviousText != ""
}

func decodePromptApplyState(raw []byte) promptApplyState {
	var state promptApplyState
	if len(raw) == 0 {
		return state
	}
	// A malformed blob degrades to "no apply state": restore then reports
	// nothing to restore, which is the safe direction — the alternative would
	// be writing an unknown string over a live prompt.
	_ = json.Unmarshal(raw, &state)
	return state
}

func encodePromptApplyState(state promptApplyState) ([]byte, error) {
	return json.Marshal(state)
}

// ── Target resolution ───────────────────────────────────────────────────────

// promptTarget is a resolved apply target: its persisted prompt text, its apply
// state, and the kind of asset that may be applied to it.
type promptTarget struct {
	targetType string
	kind       string
	id         pgtype.UUID
	content    string
	state      promptApplyState
	updatedAt  pgtype.Timestamptz
}

// loadPromptTarget resolves a target and checks, live, that the caller may
// manage it.
//
// Permission is checked against the target object itself, not the workspace:
// applying a prompt is the same act as editing the prompt by hand, so it takes
// the same authority — canManageAgent / canManageSquad.
func (h *Handler) loadPromptTarget(w http.ResponseWriter, r *http.Request, targetType, targetID string) (promptTarget, bool) {
	kind, ok := promptSourceToKind(targetType)
	if !ok {
		writeError(w, http.StatusBadRequest, `target_type must be "agent" or "squad"`)
		return promptTarget{}, false
	}
	targetUUID, ok := parseUUIDOrBadRequest(w, targetID, "target_id")
	if !ok {
		return promptTarget{}, false
	}

	switch targetType {
	case promptSourceAgent:
		agent, err := h.Queries.GetAgent(r.Context(), targetUUID)
		if err != nil {
			writeError(w, http.StatusNotFound, "agent not found")
			return promptTarget{}, false
		}
		if !h.canManageAgent(w, r, agent) {
			return promptTarget{}, false
		}
		return promptTarget{
			targetType: promptSourceAgent,
			kind:       kind,
			id:         agent.ID,
			content:    agent.Instructions,
			state:      decodePromptApplyState(agent.MarketplacePromptState),
			updatedAt:  agent.UpdatedAt,
		}, true

	case promptSourceSquad:
		squad, err := h.Queries.GetSquad(r.Context(), targetUUID)
		if err != nil {
			writeError(w, http.StatusNotFound, "squad not found")
			return promptTarget{}, false
		}
		// Same reason as loadPromptSourceForPublisher: the caller's authority is
		// looked up in the TARGET squad's workspace. Using the context member
		// would let a workspace-A owner apply a prompt into a workspace-B squad.
		member, ok := h.requireWorkspaceMember(w, r, uuidToString(squad.WorkspaceID), "squad not found")
		if !ok {
			return promptTarget{}, false
		}
		if !canManageSquad(member, squad) {
			writeError(w, http.StatusForbidden, "only the squad creator or a workspace admin can manage this squad")
			return promptTarget{}, false
		}
		return promptTarget{
			targetType: promptSourceSquad,
			kind:       kind,
			id:         squad.ID,
			content:    squad.Instructions,
			state:      decodePromptApplyState(squad.MarketplacePromptState),
			updatedAt:  squad.UpdatedAt,
		}, true
	}
	writeError(w, http.StatusBadRequest, `target_type must be "agent" or "squad"`)
	return promptTarget{}, false
}

// ── Install ─────────────────────────────────────────────────────────────────

// PromptInstallResponse is one entry in the workspace's installed library.
type PromptInstallResponse struct {
	ID                   string `json:"id"`
	SeriesID             string `json:"series_id"`
	Kind                 string `json:"kind"`
	VersionID            string `json:"installed_version_id"`
	Version              int32  `json:"installed_version"`
	ContentSha256        string `json:"installed_content_sha256"`
	Name                 string `json:"name"`
	Summary              string `json:"summary"`
	PublisherDisplayName string `json:"publisher_display_name"`
	LicenseCode          string `json:"license_code"`
	InstalledAt          string `json:"installed_at"`
	UpdatedAt            string `json:"updated_at"`
}

func promptInstallToResponse(i db.WorkspacePromptInstall) PromptInstallResponse {
	return PromptInstallResponse{
		ID:                   uuidToString(i.ID),
		SeriesID:             uuidToString(i.SeriesID),
		Kind:                 i.Kind,
		VersionID:            uuidToString(i.InstalledVersionID),
		Version:              i.InstalledVersion,
		ContentSha256:        i.InstalledContentSha256,
		Name:                 i.Name,
		Summary:              i.Summary,
		PublisherDisplayName: i.PublisherDisplayName,
		LicenseCode:          i.LicenseCode,
		InstalledAt:          timestampToString(i.InstalledAt),
		UpdatedAt:            timestampToString(i.UpdatedAt),
	}
}

// InstallPromptRequest names the version to install. Installing a newer version
// of an already-installed series IS the update path — there is no separate
// update endpoint, because "update" and "install" differ only in which row the
// pointer already held.
type InstallPromptRequest struct {
	VersionID string `json:"version_id"`
}

// InstallPrompt adds a version to the workspace's library —
// POST /api/marketplace/prompt-installations.
//
// This writes exactly one row and changes no agent and no squad. Owner decision
// D3 puts it behind a human owner/admin, the same gate installing an MCP server
// takes: it adds to a shared workspace library that every manager can then
// apply from.
func (h *Handler) InstallPrompt(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplaceV1(w, r) {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if !h.requireWorkspaceMcpWriter(w, r, workspaceID) {
		return
	}

	var req InstallPromptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	versionUUID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(req.VersionID), "version_id")
	if !ok {
		return
	}

	version, err := h.Queries.GetPromptVersion(r.Context(), versionUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "prompt version not found")
		return
	}
	// Only a live catalog entry can be installed. A draft was never published;
	// a withdrawn version was pulled by its author, and honouring the
	// withdrawal means no NEW installs — workspaces that already hold it keep
	// their row.
	if version.State != promptStatePublished || version.Visibility != promptVisibilityPublic {
		writeError(w, http.StatusNotFound, "prompt version not found")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to install")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	// Same teardown fence as the MCP install: the table has no FK, so without
	// this lock an install committing after a workspace delete swept would
	// leave a row pointing at a workspace that no longer exists.
	if _, err := qtx.LockWorkspaceForChatSessionCreate(r.Context(), workspaceUUID); err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	// Re-read the version under a row lock and re-check it here. The check
	// above ran outside this transaction, so on its own it only proves the
	// version was live at some point BEFORE the write — a withdrawal
	// committing in between would leave this install to commit anyway, which
	// is exactly the "no new installs after a withdrawal" promise broken.
	version, err = qtx.GetPromptVersionForUpdate(r.Context(), versionUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "prompt version not found")
		return
	}
	if version.State != promptStatePublished || version.Visibility != promptVisibilityPublic {
		writeError(w, http.StatusNotFound, "prompt version not found")
		return
	}

	install, err := qtx.UpsertPromptInstall(r.Context(), db.UpsertPromptInstallParams{
		WorkspaceID:            workspaceUUID,
		SeriesID:               version.SeriesID,
		Kind:                   version.Kind,
		InstalledVersionID:     version.ID,
		InstalledVersion:       version.Version.Int32,
		InstalledContentSha256: version.ContentSha256,
		Name:                   version.Name,
		Summary:                version.Summary,
		PublisherDisplayName:   version.PublisherDisplayName,
		LicenseCode:            version.LicenseCode,
		InstalledBy:            parseUUID(userID),
	})
	if err != nil {
		slog.Error("prompt install failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to install")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to install")
		return
	}
	// Installing the same version twice is idempotent by construction: the
	// upsert refreshes one row rather than creating a second, so a double-click
	// cannot fork a workspace's library.
	writeJSON(w, http.StatusOK, promptInstallToResponse(install))
}

// ListPromptInstalls returns the workspace's installed prompts —
// GET /api/marketplace/prompt-installations.
func (h *Handler) ListPromptInstalls(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplaceV1(w, r) {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	installs, err := h.Queries.ListPromptInstalls(r.Context(), workspaceUUID)
	if err != nil {
		slog.Error("prompt install list failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to load installed prompts")
		return
	}
	resp := make([]PromptInstallResponse, 0, len(installs))
	for _, i := range installs {
		resp = append(resp, promptInstallToResponse(i))
	}
	writeJSON(w, http.StatusOK, resp)
}

// ── Preview ─────────────────────────────────────────────────────────────────

// PreviewPromptApplyRequest names the target to preview against.
type PreviewPromptApplyRequest struct {
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
}

// PreviewPromptApplyResponse is what the confirmation dialog renders.
//
// The server returns both texts and lets the client render the diff: a diff is
// a presentation concern, and shipping the two sides keeps the client free to
// show word-level, line-level or side-by-side without a server change.
type PreviewPromptApplyResponse struct {
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`

	CurrentContent string `json:"current_content"`
	CurrentSha256  string `json:"current_sha256"`
	IncomingText   string `json:"incoming_content"`
	IncomingSha256 string `json:"incoming_sha256"`

	// TargetEmpty tells the UI it can offer a one-click apply: there is nothing
	// to lose, so the conflict confirmation is noise.
	TargetEmpty bool `json:"target_empty"`
	// Identical means applying would change nothing.
	Identical bool `json:"identical"`
	// RequiresConfirmation is true when applying would overwrite existing text.
	// The client must send strategy=replace in that case; preserve will refuse.
	RequiresConfirmation bool `json:"requires_confirmation"`

	// PreviewToken must be echoed back on apply. It expires, and it stops
	// matching if the target changes in the meantime.
	PreviewToken string `json:"preview_token"`
	ExpiresAt    string `json:"preview_expires_at"`
}

// PreviewPromptApply renders the diff and mints the confirmation token —
// POST /api/marketplace/prompt-installations/{id}/preview.
func (h *Handler) PreviewPromptApply(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplaceV1(w, r) {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	if !h.requirePromptHumanActor(w, r, workspaceID) {
		return
	}
	installUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "install_id")
	if !ok {
		return
	}

	var req PreviewPromptApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	install, err := h.Queries.GetPromptInstall(r.Context(), db.GetPromptInstallParams{
		ID: installUUID, WorkspaceID: workspaceUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "installed prompt not found")
		return
	}
	version, err := h.Queries.GetPromptVersion(r.Context(), install.InstalledVersionID)
	if err != nil {
		writeError(w, http.StatusNotFound, "prompt version not found")
		return
	}
	target, ok := h.loadPromptTarget(w, r, strings.TrimSpace(req.TargetType), strings.TrimSpace(req.TargetID))
	if !ok {
		return
	}
	// An agent prompt onto a squad is refused here rather than only in the UI:
	// the two prompts address different runtimes, and the resulting text would
	// be silently wrong rather than obviously broken.
	if install.Kind != target.kind {
		writeErrorCode(w, http.StatusBadRequest, "prompt_kind_mismatch",
			"this prompt cannot be applied to that kind of target")
		return
	}

	currentHash := sha256Hex(target.content)
	incomingHash := sha256Hex(version.Content)
	empty := strings.TrimSpace(target.content) == ""

	now := time.Now()
	exp := now.Add(promptPreviewTTL)
	writeJSON(w, http.StatusOK, PreviewPromptApplyResponse{
		TargetType:           target.targetType,
		TargetID:             uuidToString(target.id),
		CurrentContent:       target.content,
		CurrentSha256:        currentHash,
		IncomingText:         version.Content,
		IncomingSha256:       incomingHash,
		TargetEmpty:          empty,
		Identical:            currentHash == incomingHash,
		RequiresConfirmation: !empty && currentHash != incomingHash,
		PreviewToken: promptPreviewToken(uuidToString(install.ID), target.targetType,
			uuidToString(target.id), currentHash, incomingHash, now),
		ExpiresAt: exp.UTC().Format(time.RFC3339),
	})
}

// ── Apply ───────────────────────────────────────────────────────────────────

// ApplyPromptRequest is the confirmed write.
type ApplyPromptRequest struct {
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`

	// Strategy defaults to preserve. An empty string is treated as preserve, so
	// a client that forgets the field cannot accidentally overwrite.
	Strategy string `json:"strategy"`

	PreviewToken string `json:"preview_token"`

	// ExpectedSha256 is the target hash the user confirmed against. Redundant
	// with the token by construction — kept because it lets the server return a
	// precise "the prompt changed" rather than a generic "invalid token", which
	// is the difference between a user retrying and a user filing a bug.
	ExpectedSha256 string `json:"expected_sha256"`

	// OperationID makes a retry safe. A network failure after commit would
	// otherwise be retried, and the second apply would capture the JUST-APPLIED
	// text as previous_text — destroying the restore point.
	OperationID string `json:"operation_id"`
}

// PromptApplyResponse reports the applied state.
type PromptApplyResponse struct {
	TargetType     string `json:"target_type"`
	TargetID       string `json:"target_id"`
	AppliedVersion int32  `json:"applied_version"`
	ContentSha256  string `json:"content_sha256"`
	// CanRestore tells the UI whether to offer the one-step undo.
	CanRestore bool `json:"can_restore"`
}

// ApplyPrompt writes an installed prompt into a target —
// POST /api/marketplace/prompt-installations/{id}/apply.
//
// The sequence, all inside one transaction:
//
//  1. Lock the target row and re-read its persisted text. The lock is what
//     makes the hash check meaningful — checking a hash read outside the
//     transaction would be a race, not a check.
//  2. Refuse if the text moved since the preview (expected hash, then token).
//  3. Refuse if the target is non-empty and the strategy is preserve.
//  4. Write instructions and the restore state in ONE statement, so a target
//     can never hold new text with a stale restore pointer.
func (h *Handler) ApplyPrompt(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplaceV1(w, r) {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	if !h.requirePromptHumanActor(w, r, workspaceID) {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	installUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "install_id")
	if !ok {
		return
	}

	var req ApplyPromptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	strategy := strings.TrimSpace(req.Strategy)
	if strategy == "" {
		strategy = promptStrategyPreserve
	}
	if strategy != promptStrategyPreserve && strategy != promptStrategyReplace {
		writeError(w, http.StatusBadRequest, `strategy must be "preserve" or "replace"`)
		return
	}
	operationID := strings.TrimSpace(req.OperationID)
	if operationID == "" {
		writeError(w, http.StatusBadRequest, "operation_id is required")
		return
	}

	install, err := h.Queries.GetPromptInstall(r.Context(), db.GetPromptInstallParams{
		ID: installUUID, WorkspaceID: workspaceUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "installed prompt not found")
		return
	}
	version, err := h.Queries.GetPromptVersion(r.Context(), install.InstalledVersionID)
	if err != nil {
		writeError(w, http.StatusNotFound, "prompt version not found")
		return
	}
	// A withdrawn version cannot be newly applied: the author pulled it, and
	// putting it into another prompt after that is a new distribution, not the
	// continued use of something already applied.
	if version.State != promptStatePublished {
		writeErrorCode(w, http.StatusConflict, "prompt_version_withdrawn",
			"this prompt has been withdrawn by its publisher and can no longer be applied")
		return
	}

	// Permission on the target is checked outside the transaction — it involves
	// several reads and would hold row locks for their duration. The write
	// itself is guarded by the hash check inside, so a permission change
	// racing this request cannot produce a write the caller was not entitled
	// to make at the moment they were checked.
	target, ok := h.loadPromptTarget(w, r, strings.TrimSpace(req.TargetType), strings.TrimSpace(req.TargetID))
	if !ok {
		return
	}
	if install.Kind != target.kind {
		writeErrorCode(w, http.StatusBadRequest, "prompt_kind_mismatch",
			"this prompt cannot be applied to that kind of target")
		return
	}

	// Retry of an operation that already committed: return the same success
	// without touching anything. Re-applying would overwrite previous_text with
	// the text this very operation installed, which would quietly destroy the
	// user's ability to undo.
	if target.state.LastOperationID == operationID && !target.state.Restored {
		writeJSON(w, http.StatusOK, PromptApplyResponse{
			TargetType:     target.targetType,
			TargetID:       uuidToString(target.id),
			AppliedVersion: target.state.AppliedVersion,
			ContentSha256:  target.state.AppliedContentSha256,
			CanRestore:     target.state.canRestore(),
		})
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to apply the prompt")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	// Re-read the version under a row lock and re-check the withdrawal. The
	// state check above ran outside this transaction and so only proves the
	// version was live before the write; a withdrawal committing in between
	// would otherwise let this apply through. Locked before the target so the
	// two writers always take version-then-target and cannot deadlock.
	version, err = qtx.GetPromptVersionForUpdate(r.Context(), version.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "prompt version not found")
		return
	}
	if version.State != promptStatePublished {
		writeErrorCode(w, http.StatusConflict, "prompt_version_withdrawn",
			"this prompt has been withdrawn by its publisher and can no longer be applied")
		return
	}

	// Re-read the target under a row lock. Everything below decides against
	// THIS text, not the copy read during permission checking.
	locked, ok := h.lockPromptTarget(r, qtx, target.targetType, target.id)
	if !ok {
		writeError(w, http.StatusNotFound, "target not found")
		return
	}
	if !h.stillManageable(r, target.targetType, locked) {
		writeError(w, http.StatusNotFound, "target not found")
		return
	}
	currentHash := sha256Hex(locked.content)

	if expected := strings.TrimSpace(req.ExpectedSha256); expected != "" && expected != currentHash {
		writePromptConflict(w, currentHash)
		return
	}
	incomingHash := sha256Hex(version.Content)
	if !verifyPromptPreviewToken(strings.TrimSpace(req.PreviewToken), uuidToString(install.ID),
		target.targetType, uuidToString(target.id), currentHash, incomingHash, time.Now()) {
		// One code for both causes: an expired token and a changed target are
		// the same instruction to the client — re-preview and confirm again.
		writePromptConflict(w, currentHash)
		return
	}

	// The default-preserve rule. Reached only with a valid token, so the user
	// HAS seen the diff; refusing anyway is deliberate — seeing a diff is not
	// the same as choosing to overwrite, and `replace` is that choice.
	if strings.TrimSpace(locked.content) != "" && currentHash != incomingHash && strategy != promptStrategyReplace {
		writeErrorCode(w, http.StatusConflict, "prompt_conflict_requires_confirmation",
			"this target already has prompt text; confirm the overwrite to replace it")
		return
	}

	state := promptApplyState{
		InstallID:            uuidToString(install.ID),
		VersionID:            uuidToString(version.ID),
		SeriesID:             uuidToString(install.SeriesID),
		AppliedVersion:       install.InstalledVersion,
		AppliedContentSha256: incomingHash,
		PreviousText:         locked.content,
		PreviousUpdatedAt:    timestampToString(locked.updatedAt),
		HasRestorePoint:      true,
		AppliedBy:            userID,
		AppliedAt:            time.Now().UTC().Format(time.RFC3339),
		LastOperationID:      operationID,
	}
	blob, err := encodePromptApplyState(state)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to apply the prompt")
		return
	}

	if !h.writePromptToTarget(r, qtx, target.targetType, target.id, version.Content, blob) {
		slog.Error("prompt apply write failed", append(logger.RequestAttrs(r),
			"target_type", target.targetType, "target_id", uuidToString(target.id))...)
		writeError(w, http.StatusInternalServerError, "failed to apply the prompt")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to apply the prompt")
		return
	}
	slog.Info("marketplace prompt applied", append(logger.RequestAttrs(r),
		"target_type", target.targetType, "target_id", uuidToString(target.id),
		"series_id", uuidToString(install.SeriesID), "version", install.InstalledVersion)...)
	writeJSON(w, http.StatusOK, PromptApplyResponse{
		TargetType:     target.targetType,
		TargetID:       uuidToString(target.id),
		AppliedVersion: install.InstalledVersion,
		ContentSha256:  incomingHash,
		CanRestore:     state.canRestore(),
	})
}

// writePromptConflict is the "re-preview and try again" response. It carries
// the target's actual current hash so a client can tell whether its cached
// preview is merely stale or was for a different target entirely.
func writePromptConflict(w http.ResponseWriter, currentHash string) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":          "the prompt changed since it was previewed; review the new text and confirm again",
		"code":           "prompt_preview_stale",
		"current_sha256": currentHash,
	})
}

// lockedPromptTarget is the transactional view of a target: its text and apply
// state, read under FOR UPDATE.
type lockedPromptTarget struct {
	content   string
	state     promptApplyState
	updatedAt pgtype.Timestamptz

	// The authority columns, read under the same lock as the content, so the
	// permission re-check below judges the row this transaction is about to
	// write rather than a copy read before it.
	workspaceID pgtype.UUID
	// ownerID is agent.owner_id or squad.creator_id: the two play the same
	// role in canManageAgent and canManageSquad respectively.
	ownerID pgtype.UUID
}

// stillManageable re-checks, under the row lock, that the caller may still
// manage the target.
//
// loadPromptTarget already checked this, but outside the transaction — it does
// several reads and holding row locks across them would be worse than the
// window it leaves. That window is real: a member removed from the workspace,
// demoted, or an agent transferred to another owner between the check and the
// write would have their in-flight apply committed on authority they no longer
// hold. This is the cheap half of the check — one membership lookup — repeated
// where it is decisive.
//
// It writes no response: a failure here renders as the same "not found" the
// out-of-transaction check produces, and must not distinguish "you lost access"
// from "it was never there".
func (h *Handler) stillManageable(r *http.Request, targetType string, locked lockedPromptTarget) bool {
	member, err := h.getWorkspaceMember(r.Context(), requestUserID(r), uuidToString(locked.workspaceID))
	if err != nil {
		return false
	}
	if roleAllowed(member.Role, "owner", "admin") {
		return true
	}
	switch targetType {
	case promptSourceAgent:
		// canManageAgent: any member may manage the agent they own.
		return uuidToString(locked.ownerID) == requestUserID(r)
	case promptSourceSquad:
		// canManageSquad: a plain member manages only squads they created.
		return uuidToString(locked.ownerID) == uuidToString(member.UserID)
	}
	return false
}

func (h *Handler) lockPromptTarget(r *http.Request, qtx *db.Queries, targetType string, id pgtype.UUID) (lockedPromptTarget, bool) {
	switch targetType {
	case promptSourceAgent:
		row, err := qtx.GetAgentPromptStateForUpdate(r.Context(), id)
		if err != nil {
			return lockedPromptTarget{}, false
		}
		return lockedPromptTarget{
			content:     row.Instructions,
			state:       decodePromptApplyState(row.MarketplacePromptState),
			updatedAt:   row.UpdatedAt,
			workspaceID: row.WorkspaceID,
			ownerID:     row.OwnerID,
		}, true
	case promptSourceSquad:
		row, err := qtx.GetSquadPromptStateForUpdate(r.Context(), id)
		if err != nil {
			return lockedPromptTarget{}, false
		}
		return lockedPromptTarget{
			content:     row.Instructions,
			state:       decodePromptApplyState(row.MarketplacePromptState),
			updatedAt:   row.UpdatedAt,
			workspaceID: row.WorkspaceID,
			ownerID:     row.CreatorID,
		}, true
	}
	return lockedPromptTarget{}, false
}

// writePromptToTarget writes instructions and apply state together. The paired
// write is the atomicity guarantee this feature rests on: one statement, so
// there is no interval in which a target holds new text and a stale restore
// pointer.
func (h *Handler) writePromptToTarget(r *http.Request, qtx *db.Queries, targetType string, id pgtype.UUID, content string, state []byte) bool {
	switch targetType {
	case promptSourceAgent:
		_, err := qtx.ApplyPromptToAgent(r.Context(), db.ApplyPromptToAgentParams{
			ID: id, Instructions: content, MarketplacePromptState: state,
		})
		return err == nil
	case promptSourceSquad:
		_, err := qtx.ApplyPromptToSquad(r.Context(), db.ApplyPromptToSquadParams{
			ID: id, Instructions: content, MarketplacePromptState: state,
		})
		return err == nil
	}
	return false
}

// ── Restore ─────────────────────────────────────────────────────────────────

// RestorePromptRequest confirms a one-step undo.
type RestorePromptRequest struct {
	// ExpectedSha256 is the applied content the user is stepping back FROM.
	ExpectedSha256 string `json:"expected_sha256"`
	OperationID    string `json:"operation_id"`
}

// RestorePromptResponse reports the restored state.
type RestorePromptResponse struct {
	TargetType    string `json:"target_type"`
	TargetID      string `json:"target_id"`
	ContentSha256 string `json:"content_sha256"`
	Restored      bool   `json:"restored"`
}

// RestorePrompt undoes the last apply —
// POST /api/marketplace/prompt-targets/{type}/{id}/restore.
//
// The refusal condition is the important part: restore only proceeds if the
// target's current text is STILL EXACTLY what the apply wrote. If the user has
// since hand-edited the prompt, restoring would silently discard that edit —
// so it returns 409 and lets them decide, rather than making the decision for
// them.
//
// After a successful restore the previous text is cleared. This feature
// promises one step back, not a stack, and keeping a spent restore point would
// let a second click re-apply text the user just rejected.
func (h *Handler) RestorePrompt(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplaceV1(w, r) {
		return
	}
	if !h.requirePromptHumanActor(w, r, h.resolveWorkspaceID(r)) {
		return
	}
	if _, ok := requireUserID(w, r); !ok {
		return
	}

	var req RestorePromptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	operationID := strings.TrimSpace(req.OperationID)
	if operationID == "" {
		writeError(w, http.StatusBadRequest, "operation_id is required")
		return
	}

	target, ok := h.loadPromptTarget(w, r, chi.URLParam(r, "type"), chi.URLParam(r, "id"))
	if !ok {
		return
	}

	// Retry of a restore that already committed. Answered before the "nothing
	// to restore" check below, since a completed restore is exactly the state
	// that has no previous text left.
	if target.state.Restored && target.state.LastOperationID == operationID {
		writeJSON(w, http.StatusOK, RestorePromptResponse{
			TargetType:    target.targetType,
			TargetID:      uuidToString(target.id),
			ContentSha256: sha256Hex(target.content),
			Restored:      true,
		})
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to restore the prompt")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	locked, ok := h.lockPromptTarget(r, qtx, target.targetType, target.id)
	if !ok {
		writeError(w, http.StatusNotFound, "target not found")
		return
	}
	if !h.stillManageable(r, target.targetType, locked) {
		writeError(w, http.StatusNotFound, "target not found")
		return
	}
	state := locked.state
	if state.AppliedContentSha256 == "" || !state.canRestore() {
		writeErrorCode(w, http.StatusConflict, "prompt_nothing_to_restore",
			"there is no applied marketplace prompt to undo on this target")
		return
	}

	currentHash := sha256Hex(locked.content)
	if expected := strings.TrimSpace(req.ExpectedSha256); expected != "" && expected != currentHash {
		writePromptConflict(w, currentHash)
		return
	}
	// The hand-edit guard.
	if currentHash != state.AppliedContentSha256 {
		writeErrorCode(w, http.StatusConflict, "prompt_target_modified",
			"this prompt has been edited since the marketplace version was applied; undo would discard those edits")
		return
	}

	restored := promptApplyState{
		InstallID:            state.InstallID,
		VersionID:            state.VersionID,
		SeriesID:             state.SeriesID,
		AppliedVersion:       state.AppliedVersion,
		AppliedContentSha256: sha256Hex(state.PreviousText),
		PreviousText:         "",
		PreviousUpdatedAt:    "",
		HasRestorePoint:      false,
		AppliedBy:            state.AppliedBy,
		AppliedAt:            state.AppliedAt,
		LastOperationID:      operationID,
		Restored:             true,
	}
	blob, err := encodePromptApplyState(restored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to restore the prompt")
		return
	}
	if !h.writePromptToTarget(r, qtx, target.targetType, target.id, state.PreviousText, blob) {
		writeError(w, http.StatusInternalServerError, "failed to restore the prompt")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to restore the prompt")
		return
	}
	slog.Info("marketplace prompt restored", append(logger.RequestAttrs(r),
		"target_type", target.targetType, "target_id", uuidToString(target.id))...)
	writeJSON(w, http.StatusOK, RestorePromptResponse{
		TargetType:    target.targetType,
		TargetID:      uuidToString(target.id),
		ContentSha256: restored.AppliedContentSha256,
		Restored:      true,
	})
}

// GetPromptTargetState reports what the target currently holds —
// GET /api/marketplace/prompt-targets/{type}/{id}.
//
// This is what the status strip on the prompt tab reads: which marketplace
// version is applied, and whether the one-step undo is still available.
func (h *Handler) GetPromptTargetState(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplaceV1(w, r) {
		return
	}
	target, ok := h.loadPromptTarget(w, r, chi.URLParam(r, "type"), chi.URLParam(r, "id"))
	if !ok {
		return
	}
	state := target.state
	currentHash := sha256Hex(target.content)
	writeJSON(w, http.StatusOK, map[string]any{
		"target_type": target.targetType,
		"target_id":   uuidToString(target.id),
		"series_id":   state.SeriesID,
		"version_id":  state.VersionID,
		"applied_version": func() any {
			if state.AppliedContentSha256 == "" {
				return nil
			}
			return state.AppliedVersion
		}(),
		"applied_at": state.AppliedAt,
		// Whether the applied text is still intact. A false here is why a
		// restore would be refused, so the UI can explain before the click.
		"applied_content_intact": state.AppliedContentSha256 != "" && state.AppliedContentSha256 == currentHash,
		"can_restore":            state.canRestore() && state.AppliedContentSha256 == currentHash,
		"current_sha256":         currentHash,
	})
}
