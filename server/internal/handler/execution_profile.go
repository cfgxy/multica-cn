package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ---------------------------------------------------------------------------
// Execution profiles (RUYI-57)
//
// A workspace-level named set of per-agent execution configuration. Activating
// a profile writes runtime_id / model / thinking_level onto every agent the
// profile names; agents outside the profile keep what they had. Only future
// task dispatch is affected — a running task already carries its runtime.
//
// Naming: the UI says "Profile" for this and for runtime_profile (MUL-3284),
// which is a different concept (how a custom runtime is launched). Code, tables
// and API paths say execution_profile so the two never collide.
//
// Iron rule: an entry is stored only when it is COMPLETE — runtime and model
// both present and both valid for the workspace. A partially-filled entry
// would be activatable and would silently leave the agent on its old runtime,
// so the API refuses it rather than persisting a half state the activate path
// would have to interpret.
// ---------------------------------------------------------------------------

const (
	executionProfileNameMaxLen        = 50
	executionProfileDescriptionMaxLen = 500
	executionProfileModelMaxLen       = 200
)

// Per-entry activation outcomes. `applied` is the only one that wrote to the
// agent; the other two are reported so a partial activation can name exactly
// which member did not move and why, instead of collapsing into one number.
const (
	executionProfileEntryApplied = "applied"
	executionProfileEntrySkipped = "skipped"
	executionProfileEntryFailed  = "failed"
)

type ExecutionProfileEntryResponse struct {
	AgentID   string `json:"agent_id"`
	RuntimeID string `json:"runtime_id"`
	Model     string `json:"model"`
	// Tri-state, matching the single-agent API: null means the profile has no
	// opinion and activation leaves the agent's level alone; "" means the
	// profile says "runtime default" and activation clears the agent's level;
	// a value is written as-is. Collapsing the first two is what let a stale
	// `high` survive an activation that overwrote runtime and model.
	ThinkingLevel *string `json:"thinking_level"`
	UpdatedAt     string  `json:"updated_at"`
}

type ExecutionProfileResponse struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	CreatedBy   *string `json:"created_by"`
	// IsActive marks the profile the workspace pointer names. Display state
	// only: it says which profile last wrote the agents, not that the agents
	// still match it (a later single-agent edit can drift from the profile).
	IsActive        bool                            `json:"is_active"`
	EntryCount      int64                           `json:"entry_count"`
	LastActivatedAt *string                         `json:"last_activated_at"`
	CreatedAt       string                          `json:"created_at"`
	UpdatedAt       string                          `json:"updated_at"`
	Entries         []ExecutionProfileEntryResponse `json:"entries"`
}

// ExecutionProfileActivationResult is one member's outcome, always present for
// every entry in the profile — including the ones that did nothing. The
// dropdown's result dialog renders this list verbatim.
type ExecutionProfileActivationResult struct {
	AgentID string `json:"agent_id"`
	Status  string `json:"status"`
	// Reason is machine-readable and set for every non-applied entry.
	Reason string `json:"reason,omitempty"`
}

type ExecutionProfileActivationResponse struct {
	Profile ExecutionProfileResponse           `json:"profile"`
	Applied int                                `json:"applied"`
	Skipped int                                `json:"skipped"`
	Failed  int                                `json:"failed"`
	Results []ExecutionProfileActivationResult `json:"results"`
}

func executionProfileEntryToResponse(e db.ExecutionProfileEntry) ExecutionProfileEntryResponse {
	return ExecutionProfileEntryResponse{
		AgentID:       uuidToString(e.AgentID),
		RuntimeID:     uuidToString(e.RuntimeID),
		Model:         e.Model,
		ThinkingLevel: textToPtr(e.ThinkingLevel),
		UpdatedAt:     timestampToString(e.UpdatedAt),
	}
}

func executionProfileToResponse(
	p db.ExecutionProfile,
	entries []db.ExecutionProfileEntry,
	entryCount int64,
	activeID pgtype.UUID,
) ExecutionProfileResponse {
	resp := ExecutionProfileResponse{
		ID:          uuidToString(p.ID),
		WorkspaceID: uuidToString(p.WorkspaceID),
		Name:        p.Name,
		Description: textToPtr(p.Description),
		CreatedBy:   uuidToPtr(p.CreatedBy),
		IsActive:    activeID.Valid && uuidToString(activeID) == uuidToString(p.ID),
		EntryCount:  entryCount,
		CreatedAt:   timestampToString(p.CreatedAt),
		UpdatedAt:   timestampToString(p.UpdatedAt),
		Entries:     make([]ExecutionProfileEntryResponse, 0, len(entries)),
	}
	if p.LastActivatedAt.Valid {
		at := timestampToString(p.LastActivatedAt)
		resp.LastActivatedAt = &at
	}
	for _, e := range entries {
		resp.Entries = append(resp.Entries, executionProfileEntryToResponse(e))
	}
	return resp
}

// validateExecutionProfileName enforces the same bounds as the CHECK
// constraint so a rejected name comes back as a field-level 400 rather than a
// raw 23514.
func validateExecutionProfileName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errors.New("name is required")
	}
	if utf8.RuneCountInString(trimmed) > executionProfileNameMaxLen {
		return "", errors.New("name must be 50 characters or fewer")
	}
	return trimmed, nil
}

func validateExecutionProfileDescription(description string) error {
	if utf8.RuneCountInString(description) > executionProfileDescriptionMaxLen {
		return errors.New("description must be 500 characters or fewer")
	}
	return nil
}

// isExecutionProfileNameConflict recognises the unique index from migration
// 455 so a duplicate name returns 409 instead of a 500 leaking the constraint.
func isExecutionProfileNameConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == "idx_execution_profile_workspace_name"
}

// executionProfileWorkspace resolves the workspace from the URL and returns its
// row, which carries the active-profile pointer every response needs. The
// router has already gated the role; this only re-reads what the handler uses.
func (h *Handler) executionProfileWorkspace(w http.ResponseWriter, r *http.Request) (db.Workspace, bool) {
	wsID := strings.TrimSpace(chi.URLParam(r, "id"))
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace id")
	if !ok {
		return db.Workspace{}, false
	}
	ws, err := h.Queries.GetWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return db.Workspace{}, false
	}
	return ws, true
}

// loadExecutionProfileWithEntries assembles the full response for one profile.
func (h *Handler) loadExecutionProfileWithEntries(
	ctx context.Context,
	p db.ExecutionProfile,
	activeID pgtype.UUID,
) (ExecutionProfileResponse, error) {
	entries, err := h.Queries.ListExecutionProfileEntries(ctx, p.ID)
	if err != nil {
		return ExecutionProfileResponse{}, err
	}
	return executionProfileToResponse(p, entries, int64(len(entries)), activeID), nil
}

// ListExecutionProfiles returns every profile in the workspace with its entry
// count. Entries themselves are omitted here — the dropdown only needs name,
// count and the active flag; the manage drawer fetches one profile at a time.
func (h *Handler) ListExecutionProfiles(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.executionProfileWorkspace(w, r)
	if !ok {
		return
	}

	profiles, err := h.Queries.ListExecutionProfiles(r.Context(), ws.ID)
	if err != nil {
		slog.Warn("list execution profiles failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to list execution profiles")
		return
	}
	counts, err := h.Queries.CountExecutionProfileEntriesByProfile(r.Context(), ws.ID)
	if err != nil {
		slog.Warn("count execution profile entries failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to count execution profile entries")
		return
	}
	countByProfile := make(map[string]int64, len(counts))
	for _, c := range counts {
		countByProfile[uuidToString(c.ProfileID)] = c.EntryCount
	}

	resp := make([]ExecutionProfileResponse, len(profiles))
	for i, p := range profiles {
		resp[i] = executionProfileToResponse(p, nil, countByProfile[uuidToString(p.ID)], ws.ActiveExecutionProfileID)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"execution_profiles":          resp,
		"active_execution_profile_id": uuidToPtr(ws.ActiveExecutionProfileID),
	})
}

// GetExecutionProfile returns one profile including its entries.
func (h *Handler) GetExecutionProfile(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.executionProfileWorkspace(w, r)
	if !ok {
		return
	}
	profileUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "profileId"), "profile id")
	if !ok {
		return
	}

	profile, err := h.Queries.GetExecutionProfileForWorkspace(r.Context(), db.GetExecutionProfileForWorkspaceParams{
		ID:          profileUUID,
		WorkspaceID: ws.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "execution profile not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load execution profile")
		return
	}
	resp, err := h.loadExecutionProfileWithEntries(r.Context(), profile, ws.ActiveExecutionProfileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load execution profile entries")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

type createExecutionProfileRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

// CreateExecutionProfile creates an empty profile. Entries are added through
// the entry endpoints, which validate each one against the workspace.
func (h *Handler) CreateExecutionProfile(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.executionProfileWorkspace(w, r)
	if !ok {
		return
	}

	var req createExecutionProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name, err := validateExecutionProfileName(req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	description := pgtype.Text{}
	if req.Description != nil {
		if err := validateExecutionProfileDescription(*req.Description); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		description = pgtype.Text{String: *req.Description, Valid: true}
	}

	profile, err := h.Queries.CreateExecutionProfile(r.Context(), db.CreateExecutionProfileParams{
		WorkspaceID: ws.ID,
		Name:        name,
		Description: description,
		CreatedBy:   parseUUID(requestUserID(r)),
	})
	if err != nil {
		if isExecutionProfileNameConflict(err) {
			writeError(w, http.StatusConflict, "a profile with this name already exists in this workspace")
			return
		}
		slog.Warn("create execution profile failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to create execution profile")
		return
	}
	writeJSON(w, http.StatusCreated, executionProfileToResponse(profile, nil, 0, ws.ActiveExecutionProfileID))
}

type updateExecutionProfileRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// UpdateExecutionProfile renames a profile or edits its description. Entries
// are not touched here — they move through the entry endpoints so each write
// carries its own workspace validation.
func (h *Handler) UpdateExecutionProfile(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.executionProfileWorkspace(w, r)
	if !ok {
		return
	}
	profileUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "profileId"), "profile id")
	if !ok {
		return
	}

	var req updateExecutionProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	params := db.UpdateExecutionProfileParams{ID: profileUUID, WorkspaceID: ws.ID}
	if req.Name != nil {
		name, err := validateExecutionProfileName(*req.Name)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		params.Name = pgtype.Text{String: name, Valid: true}
	}
	if req.Description != nil {
		if err := validateExecutionProfileDescription(*req.Description); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		params.Description = pgtype.Text{String: *req.Description, Valid: true}
	}

	profile, err := h.Queries.UpdateExecutionProfile(r.Context(), params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "execution profile not found")
			return
		}
		if isExecutionProfileNameConflict(err) {
			writeError(w, http.StatusConflict, "a profile with this name already exists in this workspace")
			return
		}
		slog.Warn("update execution profile failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to update execution profile")
		return
	}
	resp, err := h.loadExecutionProfileWithEntries(r.Context(), profile, ws.ActiveExecutionProfileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load execution profile entries")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// DeleteExecutionProfile removes a profile and its entries. Deleting the
// ACTIVE profile only clears the workspace pointer: the configuration the
// activation wrote onto the agents stays, because rolling it back would move
// agents the user never asked to move (the old values are recoverable from the
// activity log instead).
func (h *Handler) DeleteExecutionProfile(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.executionProfileWorkspace(w, r)
	if !ok {
		return
	}
	profileUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "profileId"), "profile id")
	if !ok {
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	if _, err := qtx.GetExecutionProfileForWorkspace(r.Context(), db.GetExecutionProfileForWorkspaceParams{
		ID:          profileUUID,
		WorkspaceID: ws.ID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "execution profile not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load execution profile")
		return
	}

	if err := qtx.DeleteExecutionProfileEntriesByProfile(r.Context(), profileUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete execution profile entries")
		return
	}
	if err := qtx.DeleteExecutionProfile(r.Context(), db.DeleteExecutionProfileParams{
		ID:          profileUUID,
		WorkspaceID: ws.ID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete execution profile")
		return
	}
	// Guarded on the profile id: a delete of some other profile must never
	// clear a pointer that names this one.
	if err := qtx.ClearWorkspaceActiveExecutionProfile(r.Context(), db.ClearWorkspaceActiveExecutionProfileParams{
		ID:                       ws.ID,
		ActiveExecutionProfileID: profileUUID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear active execution profile")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete execution profile")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type upsertExecutionProfileEntryRequest struct {
	AgentID   string `json:"agent_id"`
	RuntimeID string `json:"runtime_id"`
	Model     string `json:"model"`
	// Tri-state, same shape as UpdateAgent: omitted / null = no opinion,
	// "" = clear to the runtime default on activation, value = write it.
	ThinkingLevel *string `json:"thinking_level"`
}

// UpsertExecutionProfileEntry stores one member's configuration in a profile.
//
// Everything is validated here rather than at activation time: the agent and
// the runtime must both belong to this workspace, the model must be non-empty,
// and the thinking level must be a token the runtime's provider recognises.
// That is what makes the stored entry activatable by construction — the
// alternative (accept anything, fail at activation) is what turns a one-click
// switch into a per-member error list the user cannot fix from the dropdown.
func (h *Handler) UpsertExecutionProfileEntry(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.executionProfileWorkspace(w, r)
	if !ok {
		return
	}
	profileUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "profileId"), "profile id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetExecutionProfileForWorkspace(r.Context(), db.GetExecutionProfileForWorkspaceParams{
		ID:          profileUUID,
		WorkspaceID: ws.ID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "execution profile not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load execution profile")
		return
	}

	var req upsertExecutionProfileEntryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	agentUUID, ok := parseUUIDOrBadRequest(w, req.AgentID, "agent_id")
	if !ok {
		return
	}
	runtimeUUID, ok := parseUUIDOrBadRequest(w, req.RuntimeID, "runtime_id")
	if !ok {
		return
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if utf8.RuneCountInString(model) > executionProfileModelMaxLen {
		writeError(w, http.StatusBadRequest, "model must be 200 characters or fewer")
		return
	}

	// Workspace isolation: both references are re-resolved inside this
	// workspace, so an id copied from another workspace is a 400 here rather
	// than a cross-workspace write at activation time.
	if _, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID:          agentUUID,
		WorkspaceID: ws.ID,
	}); err != nil {
		writeError(w, http.StatusBadRequest, "invalid agent_id")
		return
	}
	runtime, err := h.Queries.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{
		ID:          runtimeUUID,
		WorkspaceID: ws.ID,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid runtime_id")
		return
	}
	// Same gate CreateAgent / UpdateAgent apply: a profile must not become a
	// way to move agents onto someone else's private runtime.
	member, ok := h.workspaceMember(w, r, uuidToString(ws.ID))
	if !ok {
		return
	}
	if !canUseRuntimeForAgent(member, runtime) {
		writeError(w, http.StatusForbidden, "this runtime is private; only its owner can move agents onto it")
		return
	}

	// Tri-state storage: SQL NULL = no opinion, '' = explicit "runtime
	// default" (cleared on activation), value = write that level.
	thinkingLevel := pgtype.Text{}
	if req.ThinkingLevel != nil {
		value := *req.ThinkingLevel
		if value != "" {
			if !agent.IsKnownThinkingValue(runtime.Provider, value) {
				writeError(w, http.StatusBadRequest, thinkingLevelRejection(runtime.Provider, value))
				return
			}
			// Same capability gate UpdateAgent applies. Without it a profile
			// is a way to persist a level onto a runtime whose daemon never
			// advertised a reasoning effort — a configuration the
			// single-agent API refuses outright.
			switch h.acpThinkingDecision(r.Context(), runtime.Provider, runtimeUUID) {
			case acpEffortAbsent:
				writeError(w, http.StatusBadRequest, thinkingCapabilityRejection(runtime.Provider))
				return
			case acpEffortUnknown:
				writeError(w, http.StatusBadRequest, thinkingCapabilityUnknownRejection(runtime.Provider))
				return
			}
		}
		thinkingLevel = pgtype.Text{String: value, Valid: true}
	}

	entry, err := h.Queries.UpsertExecutionProfileEntry(r.Context(), db.UpsertExecutionProfileEntryParams{
		ProfileID:     profileUUID,
		AgentID:       agentUUID,
		RuntimeID:     runtimeUUID,
		Model:         model,
		ThinkingLevel: thinkingLevel,
	})
	if err != nil {
		slog.Warn("upsert execution profile entry failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to save execution profile entry")
		return
	}
	writeJSON(w, http.StatusOK, executionProfileEntryToResponse(entry))
}

// DeleteExecutionProfileEntry removes one member from a profile. The member
// keeps whatever configuration it currently has.
func (h *Handler) DeleteExecutionProfileEntry(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.executionProfileWorkspace(w, r)
	if !ok {
		return
	}
	profileUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "profileId"), "profile id")
	if !ok {
		return
	}
	agentUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "agentId"), "agent id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetExecutionProfileForWorkspace(r.Context(), db.GetExecutionProfileForWorkspaceParams{
		ID:          profileUUID,
		WorkspaceID: ws.ID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "execution profile not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load execution profile")
		return
	}

	if err := h.Queries.DeleteExecutionProfileEntry(r.Context(), db.DeleteExecutionProfileEntryParams{
		ProfileID: profileUUID,
		AgentID:   agentUUID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete execution profile entry")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ActivateExecutionProfile writes every entry onto its agent.
//
// All-or-nothing on the POINTER, per-entry on the WRITES. Each entry is
// re-validated against the workspace as it is applied, so an agent archived
// after the entry was saved becomes a reported skip rather than an error that
// abandons the other members. But if NOTHING applied, the whole transaction
// rolls back and the pointer does not move — "activated" must never name a
// profile that changed no agent.
func (h *Handler) ActivateExecutionProfile(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.executionProfileWorkspace(w, r)
	if !ok {
		return
	}
	profileUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "profileId"), "profile id")
	if !ok {
		return
	}

	// Resolved once and passed down: every entry is re-authorised against the
	// caller who is activating now, not against whoever saved the entry.
	member, ok := h.workspaceMember(w, r, uuidToString(ws.ID))
	if !ok {
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	// FOR UPDATE on the profile row: two activations of the same profile would
	// otherwise interleave their per-agent writes.
	profile, err := qtx.LockExecutionProfileForActivation(r.Context(), db.LockExecutionProfileForActivationParams{
		ID:          profileUUID,
		WorkspaceID: ws.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "execution profile not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load execution profile")
		return
	}

	entries, err := qtx.ListExecutionProfileEntries(r.Context(), profileUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load execution profile entries")
		return
	}
	if len(entries) == 0 {
		// Nothing to write, so there is nothing to call "active". Refused
		// rather than recorded so the pointer always names a profile that
		// actually configured the workspace.
		writeError(w, http.StatusBadRequest, "this profile has no member configuration to activate")
		return
	}

	results := make([]ExecutionProfileActivationResult, 0, len(entries))
	applied, skipped, failed := 0, 0, 0
	for _, entry := range entries {
		result := h.applyExecutionProfileEntry(r, qtx, ws.ID, member, entry)
		results = append(results, result)
		switch result.Status {
		case executionProfileEntryApplied:
			applied++
		case executionProfileEntrySkipped:
			skipped++
		default:
			failed++
		}
	}

	if applied == 0 {
		// Roll back: no agent moved, so neither the pointer nor
		// last_activated_at may record this attempt. The per-entry reasons
		// still reach the client — they are the whole answer here.
		writeJSON(w, http.StatusOK, ExecutionProfileActivationResponse{
			Profile: executionProfileToResponse(profile, entries, int64(len(entries)), ws.ActiveExecutionProfileID),
			Applied: 0,
			Skipped: skipped,
			Failed:  failed,
			Results: results,
		})
		return
	}

	if err := qtx.SetWorkspaceActiveExecutionProfile(r.Context(), db.SetWorkspaceActiveExecutionProfileParams{
		ID:                       ws.ID,
		ActiveExecutionProfileID: profileUUID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record the active execution profile")
		return
	}
	activated, err := qtx.MarkExecutionProfileActivated(r.Context(), db.MarkExecutionProfileActivatedParams{
		ID:          profileUUID,
		WorkspaceID: ws.ID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record the activation")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		slog.Warn("activate execution profile commit failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to activate execution profile")
		return
	}

	activeID := profileUUID
	writeJSON(w, http.StatusOK, ExecutionProfileActivationResponse{
		Profile: executionProfileToResponse(activated, entries, int64(len(entries)), activeID),
		Applied: applied,
		Skipped: skipped,
		Failed:  failed,
		Results: results,
	})
}

// applyExecutionProfileEntry writes one entry onto its agent and records the
// pre-overwrite configuration in the activity log, which is the documented
// recovery path (Owner decision B=①: audit trail, no backup profile).
func (h *Handler) applyExecutionProfileEntry(
	r *http.Request,
	qtx *db.Queries,
	workspaceID pgtype.UUID,
	member db.Member,
	entry db.ExecutionProfileEntry,
) ExecutionProfileActivationResult {
	result := ExecutionProfileActivationResult{AgentID: uuidToString(entry.AgentID)}

	// Re-validate against the workspace: the entry was validated when saved,
	// but an agent can be archived or a runtime removed in between.
	existing, err := qtx.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID:          entry.AgentID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		result.Status = executionProfileEntrySkipped
		result.Reason = "agent_not_found"
		return result
	}
	if existing.ArchivedAt.Valid {
		result.Status = executionProfileEntrySkipped
		result.Reason = "agent_archived"
		return result
	}
	runtime, err := qtx.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{
		ID:          entry.RuntimeID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		result.Status = executionProfileEntryFailed
		result.Reason = "runtime_unavailable"
		return result
	}
	// Re-check the private-runtime rule against the CURRENT caller and the
	// runtime's CURRENT visibility. Saving the entry checked this once, but a
	// runtime can be flipped public -> private afterwards; without this an
	// admin could activate an old profile and bind agents to someone else's
	// machine, credentials and files (MUL-6126 has no admin override).
	if !canUseRuntimeForAgent(member, runtime) {
		result.Status = executionProfileEntryFailed
		result.Reason = "runtime_forbidden"
		return result
	}
	// The thinking token is re-checked because the entry may have been saved
	// against a different provider's runtime, or the runtime's provider may
	// have changed. Writing a literal-invalid token would smuggle it to the
	// daemon, which the single-agent path refuses outright. The capability
	// decision is re-run for the same reason: a runtime whose catalog now
	// advertises no reasoning effort must not be handed a level here when
	// UpdateAgent would reject it.
	if entry.ThinkingLevel.Valid && entry.ThinkingLevel.String != "" {
		if !agent.IsKnownThinkingValue(runtime.Provider, entry.ThinkingLevel.String) {
			result.Status = executionProfileEntryFailed
			result.Reason = "thinking_level_unsupported"
			return result
		}
		switch h.acpThinkingDecision(r.Context(), runtime.Provider, entry.RuntimeID) {
		case acpEffortAbsent, acpEffortUnknown:
			result.Status = executionProfileEntryFailed
			result.Reason = "thinking_level_unsupported"
			return result
		}
	}

	updated, err := qtx.ApplyExecutionProfileEntryToAgent(r.Context(), db.ApplyExecutionProfileEntryToAgentParams{
		ID:          entry.AgentID,
		WorkspaceID: workspaceID,
		RuntimeID:   entry.RuntimeID,
		RuntimeMode: runtime.RuntimeMode,
		Model:       pgtype.Text{String: entry.Model, Valid: true},
		// Present drives the CASE in the query: an entry that stores '' is an
		// explicit "runtime default" and must clear the agent's level, not
		// leave the old one standing next to a new runtime and model.
		ThinkingLevelPresent: entry.ThinkingLevel.Valid,
		ThinkingLevel:        pgtype.Text{String: entry.ThinkingLevel.String, Valid: entry.ThinkingLevel.Valid && entry.ThinkingLevel.String != ""},
	})
	if err != nil {
		slog.Warn("apply execution profile entry failed",
			append(logger.RequestAttrs(r), "error", err, "agent_id", uuidToString(entry.AgentID))...)
		result.Status = executionProfileEntryFailed
		result.Reason = "update_failed"
		return result
	}

	// Audit the overwritten values. issue_id is NULL — this is a workspace
	// configuration event, not issue activity — so it never surfaces on a
	// timeline; it exists to make the previous configuration recoverable.
	details, _ := json.Marshal(map[string]any{
		"profile_id":          uuidToString(entry.ProfileID),
		"agent_id":            uuidToString(entry.AgentID),
		"from_runtime_id":     uuidToString(existing.RuntimeID),
		"from_model":          existing.Model.String,
		"from_thinking_level": existing.ThinkingLevel.String,
		"to_runtime_id":       uuidToString(updated.RuntimeID),
		"to_model":            updated.Model.String,
		"to_thinking_level":   updated.ThinkingLevel.String,
	})
	if _, err := qtx.CreateActivity(r.Context(), db.CreateActivityParams{
		WorkspaceID: workspaceID,
		ActorType:   pgtype.Text{String: "member", Valid: true},
		ActorID:     parseUUID(requestUserID(r)),
		Action:      "execution_profile_activated",
		Details:     details,
	}); err != nil {
		// The agent write is the product outcome; losing its audit row must
		// not undo it. Logged loudly so the gap is visible in triage.
		slog.Error("execution profile audit write failed",
			append(logger.RequestAttrs(r), "error", err, "agent_id", uuidToString(entry.AgentID))...)
	}

	result.Status = executionProfileEntryApplied
	return result
}
