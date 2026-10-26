package handler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/legislation"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestValidateClauseManagementCarrierBatchesChecksReferencesAfterPageBoundary(t *testing.T) {
	workspaceID := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	missingSlug := "cls_deadbeef"
	lastAgentID := pgtype.UUID{Bytes: [16]byte{1, 1, 243}, Valid: true}
	projectID := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	var calls int

	fetch := func(_ context.Context, params db.ListWorkspaceCarrierTextsBatchParams) ([]db.ListWorkspaceCarrierTextsBatchRow, error) {
		calls++
		switch calls {
		case 1:
			if params.AfterScope.Valid || params.AfterScopeID.Valid {
				t.Fatalf("first page cursor = (%+v, %+v), want empty", params.AfterScope, params.AfterScopeID)
			}
			if params.RowLimit != clauseManagementCarrierBatchSize {
				t.Fatalf("row limit = %d, want %d", params.RowLimit, clauseManagementCarrierBatchSize)
			}
			rows := make([]db.ListWorkspaceCarrierTextsBatchRow, clauseManagementCarrierBatchSize)
			for i := range rows {
				rows[i] = db.ListWorkspaceCarrierTextsBatchRow{
					ScopeID: pgtype.UUID{Bytes: [16]byte{1, byte(i >> 8), byte(i)}, Valid: true},
					Scope:   "agent",
				}
			}
			return rows, nil
		case 2:
			if !params.AfterScope.Valid || params.AfterScope.String != "agent" || params.AfterScopeID != lastAgentID {
				t.Fatalf("second page cursor = (%+v, %+v), want (agent, %+v)", params.AfterScope, params.AfterScopeID, lastAgentID)
			}
			return []db.ListWorkspaceCarrierTextsBatchRow{{
				ScopeID:          projectID,
				EffectiveContent: "静态规则引用 " + missingSlug,
				Scope:            "project",
			}}, nil
		case 3:
			if !params.AfterScope.Valid || params.AfterScope.String != "project" || params.AfterScopeID != projectID {
				t.Fatalf("final page cursor = (%+v, %+v), want (project, %+v)", params.AfterScope, params.AfterScopeID, projectID)
			}
			return nil, nil
		default:
			t.Fatalf("unexpected fetch call %d", calls)
			return nil, nil
		}
	}

	err := validateClauseManagementCarrierBatches(context.Background(), workspaceID,
		&legislation.RefUniverse{Headings: make(map[string][]string)}, nil, fetch)
	var refErr *staticClauseReferenceError
	if err == nil || !strings.Contains(err.Error(), missingSlug) || !errors.As(err, &refErr) {
		t.Fatalf("validation error = %v, want static-reference error containing %s", err, missingSlug)
	}
	if calls != 2 {
		t.Fatalf("carrier batch fetch calls = %d, want stop on the invalid second page", calls)
	}
}
