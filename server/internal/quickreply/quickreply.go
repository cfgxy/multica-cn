// Package quickreply owns the workspace quick-reply catalog (RUYI-435).
//
// MODEL. Each workspace has an ordered list of reply templates the issue
// comment composer offers behind a single "快速回复" button. Every client
// surface (web settings tab, MCP tools, the three composers) reads the SAME
// rows — nothing is hardcoded outside this package's seed.
//
// SEEDING. The 5 default templates are seeded when a workspace is created
// (handler.CreateWorkspace) and self-healed on the first list read for
// workspaces created before this feature shipped — the same rolling-deploy
// posture as the issue status catalog. Seeding is idempotent and never
// overwrites an admin's edits: a conflict on (workspace_id, name) skips the
// row rather than erroring.
package quickreply

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

// Querier is the slice of the generated query set this package needs. Taking
// an interface keeps callers testable without a live database.
type Querier interface {
	SeedQuickReplies(ctx context.Context, workspaceID pgtype.UUID) error
}

// Ensure idempotently seeds the 5 default quick replies for a workspace.
// Safe to run inside the CreateWorkspace transaction (new workspace) and on
// the list read path (self-heal for pre-existing workspaces).
func Ensure(ctx context.Context, q Querier, workspaceID pgtype.UUID) error {
	return q.SeedQuickReplies(ctx, workspaceID)
}
