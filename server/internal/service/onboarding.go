package service

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// MarkCompleteResult bundles every per-call output from
// OnboardingService.MarkComplete. Callers care about FirstCompletion to gate
// `onboarding_completed` analytics emission, and SeededIssue* to publish the
// EventIssueCreated WS event when the fallback install-runtime issue was
// actually inserted by this call.
type MarkCompleteResult struct {
	User               db.User
	FirstCompletion    bool
	SeededIssue        db.Issue
	SeededIssueCreated bool
}

// OnboardingService is the single authority for "user finished onboarding".
// Every transition of users.onboarded_at from NULL → now() now flows through
// MarkComplete — previously this was scattered across BootstrapOnboarding-
// Runtime, BootstrapOnboardingNoRuntime, AcceptInvitation, and CompleteOnboarding
// each calling MarkUserOnboarded directly, and each independently deciding
// whether to seed the install-runtime issue and claim starter_content_state.
//
// Domain-specific side effects (creating the Helper agent in
// BootstrapOnboardingRuntime, adding the membership row in AcceptInvitation)
// stay in their respective handlers — this service only owns what must
// happen for every onboarding completion.
type OnboardingService struct {
	WorkspaceContent *WorkspaceContentService
}

func NewOnboardingService(wc *WorkspaceContentService) *OnboardingService {
	return &OnboardingService{WorkspaceContent: wc}
}

// MarkComplete runs inside the caller's transaction (`q` is typically a
// `qtx`). Idempotent — re-invocations on an already-onboarded user preserve
// the original onboarded_at timestamp via COALESCE in MarkUserOnboarded.
//
// When `workspaceID` is valid, the install-runtime issue is seeded as a
// fallback if the workspace has no runtime yet (mirrors the pre-refactor
// `ensureNoRuntimeOnboardingIssue` gate). When zero, this step is skipped
// — used by completion paths that have no workspace context.
//
// starter_content_state is claimed unconditionally to suppress the legacy
// import dialog on older desktop builds (which gate on NULL).
func (s *OnboardingService) MarkComplete(
	ctx context.Context,
	q *db.Queries,
	userID pgtype.UUID,
	workspaceID pgtype.UUID,
) (MarkCompleteResult, error) {
	before, err := q.GetUser(ctx, userID)
	if err != nil {
		return MarkCompleteResult{}, err
	}
	firstCompletion := !before.OnboardedAt.Valid

	updatedUser, err := q.MarkUserOnboarded(ctx, userID)
	if err != nil {
		return MarkCompleteResult{}, err
	}

	if err := s.claimStarterContentStateIfUnset(ctx, q, userID, updatedUser.StarterContentState); err != nil {
		return MarkCompleteResult{}, err
	}

	var seeded db.Issue
	seededCreated := false
	if workspaceID.Valid {
		seeded, seededCreated, err = s.WorkspaceContent.EnsureInstallRuntimeIssue(
			ctx, q, workspaceID, userID, before.Language,
		)
		if err != nil {
			return MarkCompleteResult{}, err
		}
	}

	return MarkCompleteResult{
		User:               updatedUser,
		FirstCompletion:    firstCompletion,
		SeededIssue:        seeded,
		SeededIssueCreated: seededCreated,
	}, nil
}

// ClaimStarterContentStateIfUnset transitions starter_content_state from NULL
// to 'imported'. Exposed separately so callers that need the claim outside
// MarkComplete's flow (e.g. CreateWorkspace, which doesn't mark onboarded but
// must still suppress the legacy dialog) can invoke it directly.
func (s *OnboardingService) ClaimStarterContentStateIfUnset(
	ctx context.Context,
	q *db.Queries,
	userID pgtype.UUID,
	current pgtype.Text,
) error {
	return s.claimStarterContentStateIfUnset(ctx, q, userID, current)
}

func (s *OnboardingService) claimStarterContentStateIfUnset(
	ctx context.Context,
	q *db.Queries,
	userID pgtype.UUID,
	current pgtype.Text,
) error {
	if current.Valid {
		return nil
	}
	_, err := q.SetStarterContentState(ctx, db.SetStarterContentStateParams{
		ID:                  userID,
		StarterContentState: pgtype.Text{String: "imported", Valid: true},
	})
	return err
}
