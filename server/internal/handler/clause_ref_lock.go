package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/legislation"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type staticClauseReferenceError struct {
	message string
}

func (e *staticClauseReferenceError) Error() string { return e.message }

func lockClauseReferenceWorkspaceTx(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID) error {
	return qtx.LockClauseReferenceWorkspace(ctx, uuidToString(workspaceID))
}

func checkStaticClauseRefsTx(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID, texts map[carrierRef]string) ([]legislation.Finding, error) {
	rows, err := qtx.ListActiveWorkspaceClauses(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	active := make(map[string]bool, len(rows))
	for _, row := range rows {
		active[row.Slug] = true
	}
	byCarrier := make(map[string]string, len(texts))
	for ref, text := range texts {
		byCarrier[carrierKey(ref.scope, ref.id)] = text
	}
	return legislation.CheckStaticClauseRefs(byCarrier, active), nil
}

func validateStaticClauseRefsTx(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID, texts map[carrierRef]string) error {
	findings, err := checkStaticClauseRefsTx(ctx, qtx, workspaceID, texts)
	if err != nil {
		return err
	}
	if len(findings) > 0 {
		return &staticClauseReferenceError{message: findings[0].Message}
	}
	return nil
}

func allowStaticClauseRefsWrite(w http.ResponseWriter, ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID, texts map[carrierRef]string) bool {
	err := validateStaticClauseRefsTx(ctx, qtx, workspaceID, texts)
	if err != nil {
		var refErr *staticClauseReferenceError
		if errors.As(err, &refErr) {
			writeError(w, http.StatusConflict, refErr.Error())
			return false
		}
		writeError(w, http.StatusInternalServerError, "failed to validate clause references")
		return false
	}
	return true
}

func isFourTierPromptScope(scope promptVersionScope) bool {
	switch scope {
	case promptVersionScopeWorkspace, promptVersionScopeProject, promptVersionScopeSquad, promptVersionScopeAgent:
		return true
	default:
		return false
	}
}
