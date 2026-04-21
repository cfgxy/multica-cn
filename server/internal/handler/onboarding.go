package handler

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// CompleteOnboarding marks the authenticated user as having completed
// onboarding. Idempotent: the underlying query uses COALESCE so the
// original timestamp is preserved if called more than once. Also
// clears onboarding_current_step since "current step" has no meaning
// once complete.
func (h *Handler) CompleteOnboarding(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	user, err := h.Queries.MarkUserOnboarded(r.Context(), parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark onboarded")
		return
	}
	writeJSON(w, http.StatusOK, userToResponse(user))
}

type patchOnboardingRequest struct {
	CurrentStep   *string          `json:"current_step,omitempty"`
	Questionnaire *json.RawMessage `json:"questionnaire,omitempty"`
}

// PatchOnboarding persists partial onboarding progress for the
// authenticated user. Fields are independent:
//   - current_step: the furthest step the user has reached
//   - questionnaire: JSONB blob of Q1/Q2/Q3 answers
//
// Either or both may be omitted; omitted fields are preserved.
func (h *Handler) PatchOnboarding(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req patchOnboardingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	params := db.PatchUserOnboardingParams{ID: parseUUID(userID)}
	if req.CurrentStep != nil {
		params.CurrentStep = pgtype.Text{String: *req.CurrentStep, Valid: true}
	}
	if req.Questionnaire != nil {
		params.Questionnaire = []byte(*req.Questionnaire)
	}
	user, err := h.Queries.PatchUserOnboarding(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update onboarding")
		return
	}
	writeJSON(w, http.StatusOK, userToResponse(user))
}
