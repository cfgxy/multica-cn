package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/fileaccess"
)

type fileReadPolicyRequest struct {
	Version int64                 `json:"version"`
	Rules   []fileaccess.DenyRule `json:"rules"`
}

func (h *Handler) loadMCPFileReadPolicy(ctx context.Context) (fileaccess.DenyPolicy, error) {
	row, err := h.Queries.GetMCPFileReadPolicy(ctx)
	if err != nil {
		return fileaccess.DenyPolicy{}, err
	}
	var rules []fileaccess.DenyRule
	if json.Unmarshal(row.Rules, &rules) != nil {
		return fileaccess.DenyPolicy{}, fileaccess.ErrorPermissionDenied
	}
	policy := fileaccess.DenyPolicy{Version: row.Version, Rules: rules}
	if err := fileaccess.ValidateDenyPolicy(policy); err != nil {
		return fileaccess.DenyPolicy{}, err
	}
	return policy, nil
}

func (h *Handler) AdminGetFileReadPolicy(w http.ResponseWriter, r *http.Request) {
	policy, err := h.loadMCPFileReadPolicy(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "file read policy unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": policy.Version, "rules": policy.Rules})
}

func (h *Handler) AdminUpdateFileReadPolicy(w http.ResponseWriter, r *http.Request) {
	var input fileReadPolicyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	h.updateFileReadPolicy(w, r, input, false)
}

func (h *Handler) AdminRestoreFileReadPolicy(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Version int64 `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	defaults := fileaccess.DefaultDenyPolicy()
	h.updateFileReadPolicy(w, r, fileReadPolicyRequest{Version: input.Version, Rules: defaults.Rules}, true)
}

func (h *Handler) updateFileReadPolicy(w http.ResponseWriter, r *http.Request, input fileReadPolicyRequest, restored bool) {
	policy := fileaccess.DenyPolicy{Version: input.Version, Rules: input.Rules}
	if input.Version < 1 || fileaccess.ValidateDenyPolicy(policy) != nil {
		writeError(w, http.StatusBadRequest, "invalid file read policy")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	rules, err := json.Marshal(input.Rules)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid file read policy")
		return
	}
	row, err := h.Queries.UpdateMCPFileReadPolicy(r.Context(), db.UpdateMCPFileReadPolicyParams{
		ExpectedVersion: input.Version,
		Rules:           rules,
		UpdatedBy:       parseUUID(userID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "file read policy changed; refresh before saving")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save file read policy")
		return
	}
	h.writeAdminAudit(r.Context(), userID, "file_read_policy_updated", "instance_file_read_policy", pgtype.UUID{}, pgtype.UUID{}, pgtype.Text{}, map[string]any{
		"version": row.Version, "rule_count": len(input.Rules), "restored_defaults": restored,
	})
	var savedRules []fileaccess.DenyRule
	if json.Unmarshal(row.Rules, &savedRules) != nil {
		writeError(w, http.StatusInternalServerError, "saved file read policy is invalid")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": row.Version, "rules": savedRules})
}
