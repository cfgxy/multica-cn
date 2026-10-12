package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/outerloop"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Per-issue metadata is a small JSONB KV map agents use to record pipeline
// state (PR number, pipeline_status, waiting_on, ...). Three rules govern
// the V1 surface — they're enforced both in the handler and at the DB:
//
//   - keys match `^[a-zA-Z_][a-zA-Z0-9_.-]{0,63}$` (handler)
//   - at most 50 keys per issue (handler)
//   - values are primitive: string / number / bool (handler)
//   - JSONB column is an object and ≤ 8KB (DB CHECK; defense in depth)
//
// All mutations are single-key atomic. UpdateIssue does NOT touch metadata —
// any whole-blob overwrite would race with concurrent agent writes (see the
// design discussion on MUL-2017).
const (
	maxIssueMetadataKeys = 50
)

var issueMetadataKeyRE = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.-]{0,63}$`)

// SetIssueMetadataKeyRequest carries the JSON value to write under the key
// named in the URL. Value is a RawMessage so we can preserve numeric vs.
// string typing through to PostgreSQL — once decoded into `any`, JSON
// numbers all collapse to float64 and we'd lose integer fidelity.
type SetIssueMetadataKeyRequest struct {
	Value json.RawMessage `json:"value"`
}

func validateIssueMetadataKey(key string) error {
	if key == "" {
		return errors.New("key is required")
	}
	if !issueMetadataKeyRE.MatchString(key) {
		return errors.New("key must match ^[a-zA-Z_][a-zA-Z0-9_.-]{0,63}$")
	}
	return nil
}

// validateIssueMetadataValue rejects anything other than a primitive JSON
// scalar. Null, arrays, and objects are not allowed — the V1 surface is
// flat KV. Removing a key uses DELETE, not a null value.
func validateIssueMetadataValue(raw json.RawMessage) error {
	if len(raw) == 0 {
		return errors.New("value is required")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("value must be valid JSON: %w", err)
	}
	switch v.(type) {
	case string, bool, float64:
		return nil
	case nil:
		return errors.New("value cannot be null (use DELETE to remove a key)")
	default:
		return errors.New("value must be a primitive: string, number, or bool")
	}
}

// parseIssueMetadata decodes the JSONB bytes from db.Issue.Metadata into a
// Go map suitable for response serialization. Empty or unparseable blobs
// degrade to an empty map — the DB CHECK guarantees object shape, so this
// path is only hit on rows somehow predating the migration. Shared with the
// service-layer broadcast rendering (service.IssueToMap) so both ways of
// describing an issue agree on what an unset bag looks like on the wire.
func parseIssueMetadata(raw []byte) map[string]any {
	return util.JSONObjectOrEmpty(raw)
}

// outerLoopPlanKey reports whether key participates in the outer-loop plan
// surface (RUYI-660) and is subject to the plan permission matrix below.
func outerLoopPlanKey(key string) bool {
	return key == outerloop.MetaGoal || outerloop.IsControlPlaneKey(key) || outerloop.IsServerOnlyKey(key)
}

// gateOuterLoopMetaWrite enforces the outer-loop plan permission matrix on
// the public metadata API. It returns the value to store — rewritten for the
// max-rounds clamp — or false after writing the 4xx response. The gate is
// inert unless the outer_loop flag is on: with the loop disabled the keys
// are ordinary metadata, preserving the pre-RUYI-660 status quo.
//
// Matrix:
//   - outerloop_round / outerloop_wait_reason are server-only: written
//     exclusively by server logic through the internal SQL path, 403 for
//     every API caller.
//   - agents may write only outerloop_goal, and only as a string — they
//     never hold the loop's control plane.
//   - members own the control plane: state must name a valid plan state,
//     max_rounds must be a positive integer within the hard cap and only
//     rises, agent must be a UUID.
func (h *Handler) gateOuterLoopMetaWrite(w http.ResponseWriter, r *http.Request, actorType, key string, value json.RawMessage, existing map[string]any) (json.RawMessage, bool) {
	if !featureflags.OuterLoopEnabled(r.Context(), h.FeatureFlags) || !outerLoopPlanKey(key) {
		return value, true
	}
	if outerloop.IsServerOnlyKey(key) {
		writeError(w, http.StatusForbidden, fmt.Sprintf("%s is server-managed and cannot be written through the API", key))
		return nil, false
	}
	if actorType == "agent" {
		if key != outerloop.MetaGoal {
			writeError(w, http.StatusForbidden, fmt.Sprintf("agents may only write %s", outerloop.MetaGoal))
			return nil, false
		}
		return value, requireStringMetaValue(w, value)
	}
	switch key {
	case outerloop.MetaGoal:
		return value, requireStringMetaValue(w, value)
	case outerloop.MetaState:
		var s string
		if err := json.Unmarshal(value, &s); err != nil || !outerloop.ValidState(s) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("%s must be one of %s|%s|%s", key, outerloop.StateRunning, outerloop.StateWaitingHuman, outerloop.StateStopped))
			return nil, false
		}
		return value, true
	case outerloop.MetaAgent:
		var s string
		if err := json.Unmarshal(value, &s); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("%s must be a string", key))
			return nil, false
		}
		if _, err := util.ParseUUID(s); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("%s must be an agent UUID", key))
			return nil, false
		}
		return value, true
	case outerloop.MetaMaxRounds:
		var n float64
		if err := json.Unmarshal(value, &n); err != nil || n != float64(int(n)) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("%s must be an integer", key))
			return nil, false
		}
		clamped, ok := outerloop.ClampMaxRounds(int(n))
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("%s must be between 1 and %d", key, outerloop.HardMaxRounds))
			return nil, false
		}
		if stored, present := storedMetaInt(existing, key); present && clamped < stored {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("%s only rises (stored: %d)", key, stored))
			return nil, false
		}
		out, err := json.Marshal(clamped)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to encode %s", key))
			return nil, false
		}
		return out, true
	}
	return value, true
}

// gateOuterLoopMetaDelete applies the delete-side of the plan matrix:
// server-only keys are not deletable by anyone, and agents may not remove
// plan keys at all — retiring a plan is the member owner's move.
func (h *Handler) gateOuterLoopMetaDelete(w http.ResponseWriter, r *http.Request, actorType, key string) bool {
	if !featureflags.OuterLoopEnabled(r.Context(), h.FeatureFlags) || !outerLoopPlanKey(key) {
		return true
	}
	if outerloop.IsServerOnlyKey(key) || actorType == "agent" {
		writeError(w, http.StatusForbidden, fmt.Sprintf("%s is part of the outer-loop plan and cannot be deleted by agents", key))
		return false
	}
	return true
}

// storedMetaInt reads an integer previously stored in the metadata bag. The
// public API only admits primitives, so numbers arrive as float64.
func storedMetaInt(existing map[string]any, key string) (int, bool) {
	n, ok := existing[key].(float64)
	if !ok {
		return 0, false
	}
	return int(n), true
}

func requireStringMetaValue(w http.ResponseWriter, value json.RawMessage) bool {
	var s string
	if err := json.Unmarshal(value, &s); err != nil {
		writeError(w, http.StatusBadRequest, "value must be a string")
		return false
	}
	return true
}

// parseMetadataFilterParam reads the `metadata` query parameter (a JSON
// object) and returns it as the JSONB filter blob passed to ListIssues /
// CountIssues / ListOpenIssues. Empty input means "no filter" and returns
// a nil []byte, which the SQL layer interprets as "skip the @> check".
//
// Validates that the filter is itself a flat object of primitives, mirroring
// the constraints we apply at write time — querying for `{key: {nested}}`
// would never match since written values are primitive by construction.
func parseMetadataFilterParam(w http.ResponseWriter, raw string) ([]byte, bool) {
	if raw == "" {
		return nil, true
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		writeError(w, http.StatusBadRequest, "metadata filter must be a JSON object")
		return nil, false
	}
	for k, v := range parsed {
		if err := validateIssueMetadataKey(k); err != nil {
			writeError(w, http.StatusBadRequest, "metadata filter "+err.Error())
			return nil, false
		}
		switch v.(type) {
		case string, bool, float64:
			// ok
		default:
			writeError(w, http.StatusBadRequest, "metadata filter values must be primitives (string, number, bool)")
			return nil, false
		}
	}
	// Re-marshal so we send canonical JSON to PG (and not the raw, possibly
	// whitespace-padded user input).
	buf, err := json.Marshal(parsed)
	if err != nil {
		writeError(w, http.StatusBadRequest, "metadata filter is invalid")
		return nil, false
	}
	return buf, true
}

func (h *Handler) ListIssueMetadata(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"metadata": parseIssueMetadata(issue.Metadata)})
}

func (h *Handler) SetIssueMetadataKey(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	key := chi.URLParam(r, "key")
	if err := validateIssueMetadataKey(key); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req SetIssueMetadataKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateIssueMetadataValue(req.Value); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := uuidToString(issue.WorkspaceID)
	actorType, actorID := h.resolveActor(r, userID, workspaceID)

	// Enforce the key-count cap in the handler. The DB only guards size,
	// and a clear 4xx for "too many keys" beats a CHECK violation that
	// happens to fire on the size cap once enough keys accumulate.
	existing := parseIssueMetadata(issue.Metadata)
	if _, present := existing[key]; !present && len(existing) >= maxIssueMetadataKeys {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("metadata cannot exceed %d keys", maxIssueMetadataKeys))
		return
	}

	req.Value, ok = h.gateOuterLoopMetaWrite(w, r, actorType, key, req.Value, existing)
	if !ok {
		return
	}

	updated, err := h.Queries.SetIssueMetadataKey(r.Context(), db.SetIssueMetadataKeyParams{
		ID:          issue.ID,
		WorkspaceID: issue.WorkspaceID,
		Key:         key,
		Value:       []byte(req.Value),
	})
	if err != nil {
		if isCheckViolation(err) {
			writeError(w, http.StatusBadRequest, "metadata exceeds the 8KB size limit")
			return
		}
		slog.Warn("SetIssueMetadataKey failed", append(logger.RequestAttrs(r), "error", err, "issue_id", issueID, "key", key)...)
		writeError(w, http.StatusInternalServerError, "failed to set metadata key")
		return
	}

	metadata := parseIssueMetadata(updated.Metadata)
	h.publish(protocol.EventIssueMetadataChanged, workspaceID, actorType, actorID, map[string]any{
		"issue_id":       uuidToString(updated.ID),
		"metadata":       metadata,
		"issue_revision": updated.Revision,
	})
	writeJSON(w, http.StatusOK, map[string]any{"metadata": metadata, "issue_revision": updated.Revision})
}

func (h *Handler) DeleteIssueMetadataKey(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	key := chi.URLParam(r, "key")
	if err := validateIssueMetadataKey(key); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := uuidToString(issue.WorkspaceID)
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	if !h.gateOuterLoopMetaDelete(w, r, actorType, key) {
		return
	}

	updated, err := h.Queries.DeleteIssueMetadataKey(r.Context(), db.DeleteIssueMetadataKeyParams{
		ID:          issue.ID,
		WorkspaceID: issue.WorkspaceID,
		Key:         key,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "issue not found")
			return
		}
		slog.Warn("DeleteIssueMetadataKey failed", append(logger.RequestAttrs(r), "error", err, "issue_id", issueID, "key", key)...)
		writeError(w, http.StatusInternalServerError, "failed to delete metadata key")
		return
	}

	metadata := parseIssueMetadata(updated.Metadata)
	h.publish(protocol.EventIssueMetadataChanged, workspaceID, actorType, actorID, map[string]any{
		"issue_id":       uuidToString(updated.ID),
		"metadata":       metadata,
		"issue_revision": updated.Revision,
	})
	writeJSON(w, http.StatusOK, map[string]any{"metadata": metadata, "issue_revision": updated.Revision})
}
