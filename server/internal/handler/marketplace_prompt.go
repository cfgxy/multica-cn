package handler

// Prompt marketplace — publish side (RUYI-100).
//
// Agent and squad instructions become versionable marketplace assets. This file
// owns the publisher's half: draft, edit, publish, withdraw, and reading a
// version back. The consumer's half — install, preview, apply, restore — is in
// marketplace_prompt_apply.go.
//
// Two invariants shape everything here:
//
//   - The server reads the prompt text from the source object. A client never
//     uploads text "on behalf of" an agent, so publishing cannot be used to
//     attach arbitrary content to someone else's agent, and the snapshot always
//     matches something that actually ran.
//   - A published row is frozen. Publishing assigns the version under a series
//     lock and never runs again on that row; a correction is a new version.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/promptscan"
)

// Asset kinds, mirroring the `kind` CHECK constraint.
const (
	promptKindAgent = "agent_prompt"
	promptKindSquad = "squad_prompt"
)

// Source object types, mirroring the `source_type` CHECK constraint.
const (
	promptSourceAgent = "agent"
	promptSourceSquad = "squad"
)

const (
	promptStateDraft     = "draft"
	promptStatePublished = "published"
	promptStateWithdrawn = "withdrawn"

	promptVisibilityPrivate = "private"
	promptVisibilityPublic  = "public"
)

// promptLicenseCodes is the closed set from Owner decision D2. A license is a
// legal claim a consumer relies on, so it is an enum rather than free text: a
// typo'd or hand-written license in a cross-workspace catalog is worse than no
// license at all.
var promptLicenseCodes = map[string]struct{}{
	"cc0":                 {},
	"cc-by-4.0":           {},
	"internal-only":       {},
	"all-rights-reserved": {},
}

// Field caps mirror the column CHECK constraints. They are re-stated here so a
// too-long field is a 400 naming the field rather than a 500 from a constraint
// violation.
const (
	promptNameMax       = 100
	promptSummaryMax    = 200
	promptAudienceMax   = 200
	promptUsageNotesMax = 500
	promptCompanionsMax = 500
	promptCategoriesMax = 8
)

// promptSourceToKind maps a source object type to the asset kind it produces.
// Cross-type application (an agent prompt onto a squad) is rejected by
// comparing these, not by trusting the client's `kind`.
func promptSourceToKind(sourceType string) (string, bool) {
	switch sourceType {
	case promptSourceAgent:
		return promptKindAgent, true
	case promptSourceSquad:
		return promptKindSquad, true
	default:
		return "", false
	}
}

// sha256Hex is the content fingerprint used everywhere in this feature: as the
// frozen snapshot's identity, as the install pointer's expected value, and as
// the "has the target been hand-edited" test on restore.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ── Response DTOs ───────────────────────────────────────────────────────────

// PromptVersionResponse is one version as the API returns it.
//
// source_workspace_id is deliberately absent. It exists in the table as
// withdrawal authority, and returning it would tell every workspace in the
// catalog which organisation authored a prompt — the exact disclosure Owner
// decision D4 rules out. `publisher_display_name` is the global display name
// snapshotted at publish time and is the only attribution shown.
//
// `content` is present only where the reader is entitled to the full text; the
// discovery listing sets it empty (see promptVersionToListItem).
type PromptVersionResponse struct {
	ID       string `json:"id"`
	SeriesID string `json:"series_id"`
	Kind     string `json:"kind"`
	Version  *int32 `json:"version"`

	Name        string   `json:"name"`
	Summary     string   `json:"summary"`
	Audience    string   `json:"audience"`
	Categories  []string `json:"categories"`
	LicenseCode string   `json:"license_code"`
	UsageNotes  string   `json:"usage_notes"`
	Companions  string   `json:"companions"`

	PublisherDisplayName string `json:"publisher_display_name"`

	Content       string `json:"content"`
	ContentSha256 string `json:"content_sha256"`

	State      string `json:"state"`
	Visibility string `json:"visibility"`

	// SourceType and SourceID let the publishing workspace's own UI link a
	// version back to the agent or squad it came from. They are only populated
	// for a reader who manages that source object.
	SourceType string `json:"source_type,omitempty"`
	SourceID   string `json:"source_id,omitempty"`

	ScannerRevision string  `json:"scanner_revision"`
	PublishedAt     *string `json:"published_at"`
	WithdrawnAt     *string `json:"withdrawn_at"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

func promptCategories(raw []byte) []string {
	out := []string{}
	if len(raw) == 0 {
		return out
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		// A malformed categories blob is a display concern, not a reason to
		// fail the read: the column is server-written and validated on write,
		// so this can only be data predating a bug.
		return []string{}
	}
	return out
}

// promptVersionToResponse renders a version for a reader entitled to its full
// text. withSource controls whether the source object linkage is included —
// true only for a reader who manages that source.
func promptVersionToResponse(v db.MarketplacePromptVersion, withSource bool) PromptVersionResponse {
	resp := PromptVersionResponse{
		ID:                   uuidToString(v.ID),
		SeriesID:             uuidToString(v.SeriesID),
		Kind:                 v.Kind,
		Name:                 v.Name,
		Summary:              v.Summary,
		Audience:             v.Audience,
		Categories:           promptCategories(v.Categories),
		LicenseCode:          v.LicenseCode,
		UsageNotes:           v.UsageNotes,
		Companions:           v.Companions,
		PublisherDisplayName: v.PublisherDisplayName,
		Content:              v.Content,
		ContentSha256:        v.ContentSha256,
		State:                v.State,
		Visibility:           v.Visibility,
		ScannerRevision:      v.ScannerRevision,
		PublishedAt:          timestampToPtr(v.PublishedAt),
		WithdrawnAt:          timestampToPtr(v.WithdrawnAt),
		CreatedAt:            timestampToString(v.CreatedAt),
		UpdatedAt:            timestampToString(v.UpdatedAt),
	}
	if v.Version.Valid {
		version := v.Version.Int32
		resp.Version = &version
	}
	if withSource {
		resp.SourceType = v.SourceType
		resp.SourceID = uuidToString(v.SourceID)
	}
	return resp
}

// promptVersionToListItem is the discovery shape: metadata without the prompt
// body. The listing is the one surface that fans out across every workspace,
// so it carries the least it can — a browsing user opens the detail endpoint
// when they actually want to read the text.
func promptVersionToListItem(v db.MarketplacePromptVersion) PromptVersionResponse {
	resp := promptVersionToResponse(v, false)
	resp.Content = ""
	return resp
}

// ── Publisher authority ─────────────────────────────────────────────────────

// promptSource is the resolved source object behind a version: its current
// persisted prompt text plus the identity needed to re-check authority.
type promptSource struct {
	sourceType  string
	kind        string
	id          pgtype.UUID
	workspaceID pgtype.UUID
	content     string
	displayName string
}

// loadPromptSourceForPublisher resolves a source object and re-checks, right
// now, that the caller may manage it.
//
// Authority is re-derived on every publish rather than captured when the draft
// was created: a draft can sit for days, and an author who has since lost
// access to the agent must not be able to publish its prompt.
//
// For Mika the content is agent.instructions — the workspace's own notes half.
// The product-owned system half lives in the server binary and is composed at
// run time (service.ComposeMikaInstructions), so it is structurally absent
// here: there is no path by which it can reach a marketplace row.
func (h *Handler) loadPromptSourceForPublisher(w http.ResponseWriter, r *http.Request, sourceType, sourceID string) (promptSource, bool) {
	kind, ok := promptSourceToKind(sourceType)
	if !ok {
		writeError(w, http.StatusBadRequest, `source_type must be "agent" or "squad"`)
		return promptSource{}, false
	}
	sourceUUID, ok := parseUUIDOrBadRequest(w, sourceID, "source_id")
	if !ok {
		return promptSource{}, false
	}

	switch sourceType {
	case promptSourceAgent:
		agent, err := h.Queries.GetAgent(r.Context(), sourceUUID)
		if err != nil {
			writeError(w, http.StatusNotFound, "agent not found")
			return promptSource{}, false
		}
		if !h.canManageAgent(w, r, agent) {
			return promptSource{}, false
		}
		return promptSource{
			sourceType:  promptSourceAgent,
			kind:        kind,
			id:          agent.ID,
			workspaceID: agent.WorkspaceID,
			content:     agent.Instructions,
			displayName: agent.Name,
		}, true

	case promptSourceSquad:
		squad, err := h.Queries.GetSquad(r.Context(), sourceUUID)
		if err != nil {
			writeError(w, http.StatusNotFound, "squad not found")
			return promptSource{}, false
		}
		// requireWorkspaceMember, not workspaceMember: the latter returns the
		// member the middleware put on the context without checking which
		// workspace it belongs to, so an owner of workspace A calling with a
		// squad id from workspace B would be judged by their A-role. This
		// looks the caller up in the SQUAD's workspace, every time.
		member, ok := h.requireWorkspaceMember(w, r, uuidToString(squad.WorkspaceID), "squad not found")
		if !ok {
			return promptSource{}, false
		}
		if !canManageSquad(member, squad) {
			writeError(w, http.StatusForbidden, "only the squad creator or a workspace admin can manage this squad")
			return promptSource{}, false
		}
		return promptSource{
			sourceType:  promptSourceSquad,
			kind:        kind,
			id:          squad.ID,
			workspaceID: squad.WorkspaceID,
			content:     squad.Instructions,
			displayName: squad.Name,
		}, true
	}
	writeError(w, http.StatusBadRequest, `source_type must be "agent" or "squad"`)
	return promptSource{}, false
}

// requirePromptHumanActor rejects agent actors on every mutating prompt-market
// route.
//
// Publishing puts text into a cross-workspace catalog and applying overwrites a
// prompt after a human reads a diff. Both are confirmation boundaries: an agent
// running under an owner's token satisfies the role check but is not the party
// the confirmation was designed for. Same reasoning, same gate as
// requireWorkspaceMcpWriter.
func (h *Handler) requirePromptHumanActor(w http.ResponseWriter, r *http.Request, workspaceID string) bool {
	if actorType, _ := h.resolveActor(r, requestUserID(r), workspaceID); actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents cannot publish or apply marketplace prompts")
		return false
	}
	return true
}

// ── Validation ──────────────────────────────────────────────────────────────

// promptMetadata is the publisher-supplied half of a version. The prompt text
// is not in here on purpose: it comes from the source object.
type promptMetadata struct {
	Name        string   `json:"name"`
	Summary     string   `json:"summary"`
	Audience    string   `json:"audience"`
	Categories  []string `json:"categories"`
	LicenseCode string   `json:"license_code"`
	UsageNotes  string   `json:"usage_notes"`
	Companions  string   `json:"companions"`
}

func (m *promptMetadata) normalize() {
	m.Name = strings.TrimSpace(m.Name)
	m.Summary = strings.TrimSpace(m.Summary)
	m.Audience = strings.TrimSpace(m.Audience)
	m.LicenseCode = strings.ToLower(strings.TrimSpace(m.LicenseCode))
	m.UsageNotes = strings.TrimSpace(m.UsageNotes)
	m.Companions = strings.TrimSpace(m.Companions)
	cleaned := make([]string, 0, len(m.Categories))
	for _, c := range m.Categories {
		if c = strings.TrimSpace(c); c != "" {
			cleaned = append(cleaned, c)
		}
	}
	m.Categories = cleaned
}

// validate checks the publisher-supplied fields. Length limits mirror the
// column CHECKs so the failure names the field.
func (m promptMetadata) validate() error {
	if m.Name == "" {
		return errors.New("name is required")
	}
	if len([]rune(m.Name)) > promptNameMax {
		return errors.New("name is too long")
	}
	if len([]rune(m.Summary)) > promptSummaryMax {
		return errors.New("summary is too long")
	}
	if len([]rune(m.Audience)) > promptAudienceMax {
		return errors.New("audience is too long")
	}
	if len([]rune(m.UsageNotes)) > promptUsageNotesMax {
		return errors.New("usage notes are too long")
	}
	if len([]rune(m.Companions)) > promptCompanionsMax {
		return errors.New("suggested companions text is too long")
	}
	if len(m.Categories) > promptCategoriesMax {
		return errors.New("too many categories")
	}
	// Required rather than defaulted: a consumer in another workspace decides
	// whether they may reuse the text based on this field, and silently
	// defaulting it would manufacture a permission the author never granted.
	if _, ok := promptLicenseCodes[m.LicenseCode]; !ok {
		return errors.New("license_code must be one of: cc0, cc-by-4.0, internal-only, all-rights-reserved")
	}
	return nil
}

func (m promptMetadata) categoriesJSON() []byte {
	blob, err := json.Marshal(m.Categories)
	if err != nil {
		return []byte("[]")
	}
	return blob
}

// ── Secret gate ─────────────────────────────────────────────────────────────

// promptScanTargets is everything that becomes publicly readable. The prompt
// body is the obvious one, but a credential pasted into "usage notes" is just
// as public, so the gate covers every free-text field. The license code is an
// enum and carries no content.
func promptScanTargets(content string, m promptMetadata) string {
	return strings.Join([]string{
		content, m.Name, m.Summary, m.Audience, m.UsageNotes, m.Companions,
		strings.Join(m.Categories, "\n"),
	}, "\n")
}

// writePromptScanBlocked returns the fail-closed 422.
//
// The body carries category, rule and line only. There is no "publish anyway"
// parameter and no override role: the point of the gate is that a credential
// cannot reach a cross-workspace catalog, and an override is the one shape that
// guarantees it eventually will.
func writePromptScanBlocked(w http.ResponseWriter, res promptscan.Result) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error":            "the prompt appears to contain credentials and cannot be published",
		"code":             "prompt_secret_detected",
		"scanner_revision": res.Revision,
		"findings":         res.Findings,
		"truncated":        res.Truncated,
	})
}

// ── Handlers ────────────────────────────────────────────────────────────────

// CreatePromptVersionRequest starts a draft from a source object.
//
// There is no `content` field, and adding one would break the model: the server
// snapshots the source object's own persisted text, so a draft can only ever
// describe a prompt the publisher actually controls.
type CreatePromptVersionRequest struct {
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id"`
	SeriesID   string `json:"series_id"`
	promptMetadata
}

// CreatePromptVersion opens a draft — POST /api/marketplace/prompt-versions.
//
// A draft is private and not discoverable; it exists so a publisher can fill in
// metadata and re-read the diff before committing to a public, immutable
// snapshot. Content is snapshotted here for preview, and re-read at publish
// time, because the source object can change in between.
func (h *Handler) CreatePromptVersion(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplaceV1(w, r) {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	if !h.requirePromptHumanActor(w, r, workspaceID) {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req CreatePromptVersionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.normalize()
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	src, ok := h.loadPromptSourceForPublisher(w, r, strings.TrimSpace(req.SourceType), strings.TrimSpace(req.SourceID))
	if !ok {
		return
	}

	// A new series for a first publication; an existing series when the
	// publisher is drafting the next version of an asset they already own.
	seriesID := src.id
	if raw := strings.TrimSpace(req.SeriesID); raw != "" {
		parsed, ok := parseUUIDOrBadRequest(w, raw, "series_id")
		if !ok {
			return
		}
		existing, err := h.Queries.ListPromptVersionsBySeries(r.Context(), parsed)
		if err != nil || len(existing) == 0 {
			writeError(w, http.StatusNotFound, "series not found")
			return
		}
		// A series belongs to one source object. Without this a publisher who
		// manages agent B could append a version to agent A's series and
		// inherit its installs.
		head := existing[0]
		if head.SourceType != src.sourceType || uuidToString(head.SourceID) != uuidToString(src.id) {
			writeError(w, http.StatusForbidden, "this series belongs to a different source object")
			return
		}
		seriesID = parsed
	}

	publisherName := h.promptPublisherDisplayName(r, userID)

	version, err := h.Queries.CreatePromptVersionDraft(r.Context(), db.CreatePromptVersionDraftParams{
		SeriesID:             seriesID,
		Kind:                 src.kind,
		SourceWorkspaceID:    src.workspaceID,
		SourceType:           src.sourceType,
		SourceID:             src.id,
		PublisherUserID:      parseUUID(userID),
		PublisherDisplayName: publisherName,
		Content:              src.content,
		ContentSha256:        sha256Hex(src.content),
		Name:                 req.Name,
		Summary:              req.Summary,
		Audience:             req.Audience,
		Categories:           req.categoriesJSON(),
		LicenseCode:          req.LicenseCode,
		UsageNotes:           req.UsageNotes,
		Companions:           req.Companions,
	})
	if err != nil {
		if isUniqueViolation(err) {
			// The partial unique index allows one open draft per series: a
			// second draft would make "which draft becomes v2" ambiguous.
			writeError(w, http.StatusConflict, "this asset already has an open draft")
			return
		}
		slog.Error("prompt version draft failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to create the draft")
		return
	}
	writeJSON(w, http.StatusCreated, promptVersionToResponse(version, true))
}

// promptPublisherDisplayName resolves the attribution shown in the catalog.
//
// Owner decision D4: attribution is the publisher's global display name,
// snapshotted at publish time, and never the source workspace. A later rename
// does not rewrite published versions — the snapshot records who published it
// then, which is what a consumer evaluating the asset is judging.
func (h *Handler) promptPublisherDisplayName(r *http.Request, userID string) string {
	user, err := h.Queries.GetUser(r.Context(), parseUUID(userID))
	if err != nil {
		return ""
	}
	return user.Name
}

// UpdatePromptVersionRequest edits a draft's metadata, and optionally re-reads
// the prompt text from the source object.
type UpdatePromptVersionRequest struct {
	promptMetadata
	// RefreshContent re-snapshots the source object's current text. The
	// publisher asks for this explicitly so an unrelated metadata edit cannot
	// silently pull in prompt changes they have not reviewed.
	RefreshContent bool `json:"refresh_content"`
}

// UpdatePromptVersion edits a draft — PUT /api/marketplace/prompt-versions/{id}.
//
// Only drafts. The SQL is guarded on state = 'draft' as well, so a published
// version cannot be edited even if this check were bypassed.
func (h *Handler) UpdatePromptVersion(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplaceV1(w, r) {
		return
	}
	if !h.requirePromptHumanActor(w, r, h.resolveWorkspaceID(r)) {
		return
	}
	versionUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "version_id")
	if !ok {
		return
	}
	existing, err := h.Queries.GetPromptVersion(r.Context(), versionUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "prompt version not found")
		return
	}
	if existing.State != promptStateDraft {
		writeError(w, http.StatusConflict, "a published version cannot be edited; publish a new version instead")
		return
	}
	src, ok := h.loadPromptSourceForPublisher(w, r, existing.SourceType, uuidToString(existing.SourceID))
	if !ok {
		return
	}

	var req UpdatePromptVersionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.normalize()
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	params := db.UpdatePromptVersionDraftParams{
		ID:          versionUUID,
		Name:        strToText(req.Name),
		Summary:     strToText(req.Summary),
		Audience:    strToText(req.Audience),
		Categories:  req.categoriesJSON(),
		LicenseCode: strToText(req.LicenseCode),
		UsageNotes:  strToText(req.UsageNotes),
		Companions:  strToText(req.Companions),
	}
	if req.RefreshContent {
		params.Content = strToText(src.content)
		params.ContentSha256 = strToText(sha256Hex(src.content))
	}

	updated, err := h.Queries.UpdatePromptVersionDraft(r.Context(), params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "a published version cannot be edited; publish a new version instead")
			return
		}
		slog.Error("prompt version update failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to update the draft")
		return
	}
	writeJSON(w, http.StatusOK, promptVersionToResponse(updated, true))
}

// ScanPromptVersionResponse is the clean-scan answer.
//
// A pass is deliberately narrow, and the wording the UI shows says so: it means
// no rule of this revision matched, never that the text holds no secret. The
// revision travels with it so a publisher can tell which detector set cleared
// their prompt.
type ScanPromptVersionResponse struct {
	ScannerRevision string `json:"scanner_revision"`
	Passed          bool   `json:"passed"`
}

// ScanPromptVersion runs the publish gate WITHOUT publishing —
// POST /api/marketplace/prompt-versions/{id}/scan.
//
// This exists because the wizard has to show the scan result before the
// publisher chooses public or private, and the only way to run the scanner used
// to be to publish. The dialog did exactly that: it published privately to
// scan, which froze the draft, and the later "make it public" call then hit
// PublishPromptVersion's already-published early return and did nothing. A user
// who chose public got a private version, and a user who chose "save as draft"
// got a frozen one they could no longer edit.
//
// It reads the same source text and the same metadata through the same
// promptScanTargets the publish path uses, so a pass here and a block at
// publish can only mean the prompt changed in between — which is the case
// PublishPromptVersion's own re-scan is there to catch. This endpoint is an
// early answer, never the authority: publishing scans again regardless, so
// skipping this call cannot get unscanned text into the catalog.
func (h *Handler) ScanPromptVersion(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplaceV1(w, r) {
		return
	}
	if !h.requirePromptHumanActor(w, r, h.resolveWorkspaceID(r)) {
		return
	}
	versionUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "version_id")
	if !ok {
		return
	}
	draft, err := h.Queries.GetPromptVersion(r.Context(), versionUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "prompt version not found")
		return
	}
	// Authority is re-checked against the source object, exactly as publishing
	// does: this endpoint reads the source's live text, so it must not answer
	// for anyone who could not publish it.
	src, ok := h.loadPromptSourceForPublisher(w, r, draft.SourceType, uuidToString(draft.SourceID))
	if !ok {
		return
	}
	if strings.TrimSpace(src.content) == "" {
		writeError(w, http.StatusBadRequest, "the source object has no prompt text to publish")
		return
	}

	meta := promptMetadata{
		Name: draft.Name, Summary: draft.Summary, Audience: draft.Audience,
		Categories: promptCategories(draft.Categories), LicenseCode: draft.LicenseCode,
		UsageNotes: draft.UsageNotes, Companions: draft.Companions,
	}
	if err := meta.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	scan := promptscan.Scan(promptScanTargets(src.content, meta))
	if !scan.OK() {
		slog.Info("prompt scan blocked", append(logger.RequestAttrs(r),
			"version_id", uuidToString(versionUUID), "findings", len(scan.Findings),
			"scanner_revision", scan.Revision)...)
		writePromptScanBlocked(w, scan)
		return
	}
	writeJSON(w, http.StatusOK, ScanPromptVersionResponse{
		ScannerRevision: scan.Revision,
		Passed:          true,
	})
}

// PublishPromptVersionRequest carries the visibility choice.
type PublishPromptVersionRequest struct {
	// Public makes the version discoverable across workspaces. Owner decision
	// D1: drafts are private, and going public is an explicit act rather than a
	// default, because it is the irreversible half (a withdrawal stops new
	// installs but cannot un-read the text).
	Public bool `json:"public"`
}

// PublishPromptVersion freezes a draft into an immutable version —
// POST /api/marketplace/prompt-versions/{id}/publish.
//
// Order matters and is the whole design:
//
//  1. Re-check the publisher's authority over the source object, NOW.
//  2. Re-read the source text and scan it. The scan runs on what is about to be
//     frozen, never on what the draft happened to hold.
//  3. Inside a transaction, lock the series, confirm the source text has not
//     moved since the scan, assign the next version, and freeze.
//
// Step 3's re-check closes the window where an edit lands between scan and
// commit: without it, a prompt could be scanned clean and published dirty.
func (h *Handler) PublishPromptVersion(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplaceV1(w, r) {
		return
	}
	if !h.requirePromptHumanActor(w, r, h.resolveWorkspaceID(r)) {
		return
	}
	versionUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "version_id")
	if !ok {
		return
	}
	var req PublishPromptVersionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	draft, err := h.Queries.GetPromptVersion(r.Context(), versionUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "prompt version not found")
		return
	}
	if draft.State != promptStateDraft {
		if draft.State == promptStatePublished {
			// A retry asking for the visibility the row already has is the
			// client repeating itself, and answering 200 is right.
			//
			// A retry asking for a DIFFERENT visibility is not a retry, and
			// this used to answer 200 to that too — the caller was told its
			// "make it public" succeeded while the row stayed private, which
			// is how a version nobody could find came to look published. A
			// published row is frozen, visibility included; changing it means
			// withdrawing and publishing a new version.
			want := promptVisibilityPrivate
			if req.Public {
				want = promptVisibilityPublic
			}
			if draft.Visibility != want {
				writeErrorCode(w, http.StatusConflict, "prompt_already_published",
					"this version is already published and its visibility can no longer be changed; publish a new version instead")
				return
			}
			writeJSON(w, http.StatusOK, promptVersionToResponse(draft, true))
			return
		}
		writeError(w, http.StatusConflict, "this version has been withdrawn and cannot be published")
		return
	}

	src, ok := h.loadPromptSourceForPublisher(w, r, draft.SourceType, uuidToString(draft.SourceID))
	if !ok {
		return
	}
	if strings.TrimSpace(src.content) == "" {
		writeError(w, http.StatusBadRequest, "the source object has no prompt text to publish")
		return
	}

	meta := promptMetadata{
		Name: draft.Name, Summary: draft.Summary, Audience: draft.Audience,
		Categories: promptCategories(draft.Categories), LicenseCode: draft.LicenseCode,
		UsageNotes: draft.UsageNotes, Companions: draft.Companions,
	}
	if err := meta.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	scan := promptscan.Scan(promptScanTargets(src.content, meta))
	if !scan.OK() {
		// Log the shape of the block, never the content that produced it.
		slog.Info("prompt publish blocked by secret scan", append(logger.RequestAttrs(r),
			"version_id", uuidToString(versionUUID), "findings", len(scan.Findings),
			"scanner_revision", scan.Revision)...)
		writePromptScanBlocked(w, scan)
		return
	}
	contentHash := sha256Hex(src.content)

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to publish")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	// Serialise concurrent publishes of the same asset. Version assignment
	// reads MAX(version) and writes MAX+1; without this lock two publishes
	// would read the same max, and only the unique index would catch it — as a
	// 500 rather than an ordered pair of versions.
	if _, err := qtx.LockPromptSeries(r.Context(), draft.SeriesID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to publish")
		return
	}

	// The source could have been edited between the scan and this lock. Re-read
	// it and refuse if it moved: the alternative is publishing text that was
	// never scanned.
	current, ok := h.promptSourceContentInTx(r, qtx, draft.SourceType, draft.SourceID)
	if !ok {
		writeError(w, http.StatusNotFound, "the source object no longer exists")
		return
	}
	if sha256Hex(current) != contentHash {
		writeErrorCode(w, http.StatusConflict, "prompt_source_changed",
			"the prompt changed while it was being published; review the new text and publish again")
		return
	}

	next, err := qtx.NextPromptSeriesVersion(r.Context(), draft.SeriesID)
	if err != nil {
		slog.Error("prompt version number failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to publish")
		return
	}

	// Freeze the scanned text and its scan onto the row before flipping state:
	// the published CHECK requires both, so a version can never be discoverable
	// without the scan that cleared it.
	scanBlob, err := json.Marshal(scan.Findings)
	if err != nil {
		scanBlob = []byte("[]")
	}
	if _, err := qtx.UpdatePromptVersionDraft(r.Context(), db.UpdatePromptVersionDraftParams{
		ID:            versionUUID,
		Content:       strToText(current),
		ContentSha256: strToText(contentHash),
	}); err != nil {
		slog.Error("prompt content freeze failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to publish")
		return
	}
	if _, err := qtx.RecordPromptVersionScan(r.Context(), db.RecordPromptVersionScanParams{
		ID:              versionUUID,
		ScannerRevision: scan.Revision,
		ScanResult:      scanBlob,
	}); err != nil {
		slog.Error("prompt scan record failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to publish")
		return
	}

	visibility := promptVisibilityPrivate
	if req.Public {
		visibility = promptVisibilityPublic
	}
	published, err := qtx.PublishPromptVersion(r.Context(), db.PublishPromptVersionParams{
		ID:         versionUUID,
		Visibility: visibility,
		Version:    pgtype.Int4{Int32: next, Valid: true},
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeErrorCode(w, http.StatusConflict, "prompt_version_race",
				"another version of this asset was published at the same time; try again")
			return
		}
		slog.Error("prompt publish failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to publish")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to publish")
		return
	}
	slog.Info("prompt version published", append(logger.RequestAttrs(r),
		"version_id", uuidToString(published.ID), "series_id", uuidToString(published.SeriesID),
		"version", next, "kind", published.Kind, "visibility", visibility)...)
	writeJSON(w, http.StatusOK, promptVersionToResponse(published, true))
}

// promptSourceContentInTx re-reads a source object's prompt text under the
// publish transaction. Returns ok=false when the object has been deleted.
func (h *Handler) promptSourceContentInTx(r *http.Request, qtx *db.Queries, sourceType string, sourceID pgtype.UUID) (string, bool) {
	switch sourceType {
	case promptSourceAgent:
		row, err := qtx.GetAgentPromptStateForUpdate(r.Context(), sourceID)
		if err != nil {
			return "", false
		}
		return row.Instructions, true
	case promptSourceSquad:
		row, err := qtx.GetSquadPromptStateForUpdate(r.Context(), sourceID)
		if err != nil {
			return "", false
		}
		return row.Instructions, true
	}
	return "", false
}

// WithdrawPromptVersion takes a version out of the catalog —
// POST /api/marketplace/prompt-versions/{id}/withdraw.
//
// Withdrawal stops new installs and new applications. It deliberately does NOT
// touch text already applied into an agent or squad: that text is the consuming
// workspace's own prompt now, and reaching across a workspace boundary to
// rewrite it would be a far worse outcome than a stale copy.
func (h *Handler) WithdrawPromptVersion(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplaceV1(w, r) {
		return
	}
	if !h.requirePromptHumanActor(w, r, h.resolveWorkspaceID(r)) {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	versionUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "version_id")
	if !ok {
		return
	}
	version, err := h.Queries.GetPromptVersion(r.Context(), versionUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "prompt version not found")
		return
	}
	if version.State == promptStateWithdrawn {
		writeJSON(w, http.StatusOK, promptVersionToResponse(version, true))
		return
	}
	if !h.canWithdrawPromptVersion(w, r, version) {
		return
	}

	// The withdrawal takes the version's row lock, which is the same lock
	// InstallPrompt and ApplyPrompt take before their own writes. Without it
	// the three run concurrently off states each read before the others
	// started, and an install or apply already past its check would commit
	// after the withdrawal — a new consumption of a version the author had
	// already pulled.
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to withdraw")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	locked, err := qtx.GetPromptVersionForUpdate(r.Context(), versionUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "prompt version not found")
		return
	}
	// Re-checked under the lock: a concurrent withdrawal of the same version is
	// the same retry the pre-check answers, and answering it twice is friendlier
	// than a 409 the client cannot act on.
	if locked.State == promptStateWithdrawn {
		writeJSON(w, http.StatusOK, promptVersionToResponse(locked, true))
		return
	}

	withdrawn, err := qtx.WithdrawPromptVersion(r.Context(), db.WithdrawPromptVersionParams{
		ID:          versionUUID,
		WithdrawnBy: parseUUID(userID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "only a published version can be withdrawn")
			return
		}
		slog.Error("prompt withdraw failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to withdraw")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to withdraw")
		return
	}
	writeJSON(w, http.StatusOK, promptVersionToResponse(withdrawn, true))
}

// canWithdrawPromptVersion decides who may pull a version.
//
// Normally: whoever can still manage the source object. If the source object is
// gone — deleted agent, deleted squad — authority falls back to an owner/admin
// of the source workspace, so a published prompt never becomes unwithdrawable
// just because the thing it came from was deleted.
func (h *Handler) canWithdrawPromptVersion(w http.ResponseWriter, r *http.Request, version db.MarketplacePromptVersion) bool {
	sourceWorkspaceID := uuidToString(version.SourceWorkspaceID)

	switch version.SourceType {
	case promptSourceAgent:
		if agent, err := h.Queries.GetAgent(r.Context(), version.SourceID); err == nil {
			return h.canManageAgent(w, r, agent)
		}
	case promptSourceSquad:
		if squad, err := h.Queries.GetSquad(r.Context(), version.SourceID); err == nil {
			// Looked up in the SOURCE workspace, not taken from the request
			// context: withdrawal authority belongs to the publishing
			// workspace, and a role held somewhere else is not that authority.
			member, ok := h.requireWorkspaceMember(w, r, sourceWorkspaceID, "prompt version not found")
			if !ok {
				return false
			}
			if !canManageSquad(member, squad) {
				writeError(w, http.StatusForbidden, "only the squad creator or a workspace admin can withdraw this")
				return false
			}
			return true
		}
	}

	// Not a member of the publishing workspace: requireWorkspaceMember already
	// says "not found" rather than "forbidden", so probing this endpoint cannot
	// confirm that a given version was published by a workspace the caller
	// cannot see.
	member, ok := h.requireWorkspaceMember(w, r, sourceWorkspaceID, "prompt version not found")
	if !ok {
		return false
	}
	if !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "only a workspace owner or admin can withdraw this")
		return false
	}
	return true
}

// GetPromptVersion returns one version — GET /api/marketplace/prompt-versions/{id}.
//
// A published public version is readable by any workspace member: that is what
// a catalog is. A draft, a private version, or a withdrawn one is readable only
// by someone who can manage the source object — otherwise "draft" would be a
// meaningless state, since anyone could read the text before publication.
func (h *Handler) GetPromptVersion(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplaceV1(w, r) {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	versionUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "version_id")
	if !ok {
		return
	}
	version, err := h.Queries.GetPromptVersion(r.Context(), versionUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "prompt version not found")
		return
	}

	if version.State == promptStatePublished && version.Visibility == promptVisibilityPublic {
		writeJSON(w, http.StatusOK, promptVersionToResponse(version, false))
		return
	}
	// Withdrawn versions stay readable to a workspace that already installed
	// them: the install list has to keep rendering, and the consumer can
	// legitimately still see text they hold.
	if version.State == promptStateWithdrawn {
		if wsUUID, err := util.ParseUUID(workspaceID); err == nil {
			if _, err := h.Queries.GetPromptInstallBySeries(r.Context(), db.GetPromptInstallBySeriesParams{
				WorkspaceID: wsUUID,
				SeriesID:    version.SeriesID,
			}); err == nil {
				writeJSON(w, http.StatusOK, promptVersionToResponse(version, false))
				return
			}
		}
	}
	if !h.promptVersionVisibleToPublisher(r, version) {
		writeError(w, http.StatusNotFound, "prompt version not found")
		return
	}
	writeJSON(w, http.StatusOK, promptVersionToResponse(version, true))
}

// promptVersionVisibleToPublisher reports whether the caller manages the source
// object, without writing an error response — this is a visibility test, and
// its failure has to render as 404 rather than leak that the id exists.
func (h *Handler) promptVersionVisibleToPublisher(r *http.Request, version db.MarketplacePromptVersion) bool {
	member, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      parseUUID(requestUserID(r)),
		WorkspaceID: version.SourceWorkspaceID,
	})
	if err != nil {
		return false
	}
	if roleAllowed(member.Role, "owner", "admin") {
		return true
	}
	switch version.SourceType {
	case promptSourceAgent:
		agent, err := h.Queries.GetAgent(r.Context(), version.SourceID)
		return err == nil && uuidToString(agent.OwnerID) == requestUserID(r)
	case promptSourceSquad:
		squad, err := h.Queries.GetSquad(r.Context(), version.SourceID)
		return err == nil && canManageSquad(member, squad)
	}
	return false
}

// ListPromptVersionsBySource lists what a source object has published or has in
// draft — GET /api/marketplace/prompt-sources/{type}/{id}/versions.
//
// This is what the status strip on the agent/squad prompt tab reads: "published
// as v3", "draft pending". Restricted to someone who manages the source, since
// it exposes drafts.
func (h *Handler) ListPromptVersionsBySource(w http.ResponseWriter, r *http.Request) {
	if !h.requireMarketplaceV1(w, r) {
		return
	}
	src, ok := h.loadPromptSourceForPublisher(w, r, chi.URLParam(r, "type"), chi.URLParam(r, "id"))
	if !ok {
		return
	}
	versions, err := h.Queries.ListPromptVersionsBySource(r.Context(), db.ListPromptVersionsBySourceParams{
		SourceWorkspaceID: src.workspaceID,
		SourceType:        src.sourceType,
		SourceID:          src.id,
	})
	if err != nil {
		slog.Error("prompt source versions failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to load versions")
		return
	}
	resp := make([]PromptVersionResponse, 0, len(versions))
	for _, v := range versions {
		resp = append(resp, promptVersionToResponse(v, true))
	}
	writeJSON(w, http.StatusOK, resp)
}

// ListPromptVersions is discovery —
// GET /api/marketplace/prompt-versions?kind=&q=.
//
// Cross-workspace by design: this is the one place a workspace sees another
// workspace's output. It returns only published public versions, only the
// latest of each series, and no prompt body.
func (h *Handler) ListPromptVersions(w http.ResponseWriter, r *http.Request) {
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

	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	if kind != "" && kind != promptKindAgent && kind != promptKindSquad {
		writeError(w, http.StatusBadRequest, `kind must be "agent_prompt" or "squad_prompt"`)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	params := db.ListPublishedPromptVersionsParams{}
	if kind != "" {
		params.Kind = strToText(kind)
	}
	if query != "" {
		params.Query = strToText(query)
	}
	versions, err := h.Queries.ListPublishedPromptVersions(r.Context(), params)
	if err != nil {
		slog.Error("prompt discovery failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to load the marketplace")
		return
	}

	installs, err := h.Queries.ListPromptInstalls(r.Context(), workspaceUUID)
	if err != nil {
		slog.Error("prompt install list failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to load installed prompts")
		return
	}
	installed := make(map[string]db.WorkspacePromptInstall, len(installs))
	for _, i := range installs {
		installed[uuidToString(i.SeriesID)] = i
	}

	resp := make([]PromptMarketItemResponse, 0, len(versions))
	for _, v := range versions {
		item := PromptMarketItemResponse{PromptVersionResponse: promptVersionToListItem(v)}
		if install, ok := installed[uuidToString(v.SeriesID)]; ok {
			item.Installed = true
			item.InstallID = uuidToString(install.ID)
			installedVersion := install.InstalledVersion
			item.InstalledVersion = &installedVersion
			// An update is available when the catalog's latest version number
			// is ahead of what this workspace installed.
			item.UpdateAvailable = v.Version.Valid && v.Version.Int32 > install.InstalledVersion
		}
		resp = append(resp, item)
	}
	writeJSON(w, http.StatusOK, resp)
}

// PromptMarketItemResponse is a catalog entry plus this workspace's relationship
// to it. Keeping install state on the listing is what lets the market page
// render "Installed" / "Update available" without a second round trip per item.
type PromptMarketItemResponse struct {
	PromptVersionResponse
	Installed        bool   `json:"installed"`
	InstallID        string `json:"install_id,omitempty"`
	InstalledVersion *int32 `json:"installed_version,omitempty"`
	UpdateAvailable  bool   `json:"update_available"`
}
