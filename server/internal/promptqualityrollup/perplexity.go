package promptqualityrollup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/promptperplexity"
)

// Perplexity is the database seam for D3 (rule perplexity). The judgement
// itself is in pkg/promptperplexity; this file fetches the tiers, calls the
// scorer once per runtime profile, and writes one row per profile.
//
// D3 is keyed on an AGENT version. The other tiers are read live rather than
// from their own version history, because live is what the daemon injects: a
// score answers "how ambiguous is the document a run of this shape receives
// today", and scored_at records when that was true. The project tier is absent
// on purpose — it is chosen per issue at claim time, so no single project
// document belongs to an agent version.
type Perplexity struct {
	Queries   *db.Queries
	Generator promptperplexity.Generator
}

// ProfileOutcome is what happened for one runtime profile. A refusal is
// recorded here and NOT written to the table: a missing row is how the
// dashboard knows the version was never scored, and inventing a neutral band
// would defeat the dimension.
type ProfileOutcome struct {
	Profile promptperplexity.Profile
	Err     error
}

// ScoreAgentVersion scores one agent prompt version under every runtime
// profile. Each profile is scored against its own assembled document and
// stored as its own row; nothing here merges or averages them (Owner Q17).
//
// A profile that cannot be scored does not fail the others: the outcomes are
// returned per profile, and the caller decides what is worth reporting.
func (p Perplexity) ScoreAgentVersion(ctx context.Context, agentID pgtype.UUID, version int32) ([]ProfileOutcome, error) {
	if p.Generator == nil || !p.Generator.Enabled() {
		return nil, promptperplexity.ErrNotScored
	}

	agentVersion, err := p.Queries.GetPromptVersionByScopeVersion(ctx, db.GetPromptVersionByScopeVersionParams{
		Scope:   string(promptVersionScopeAgent),
		ScopeID: agentID,
		Version: version,
	})
	if err != nil {
		return nil, fmt.Errorf("read agent version %d: %w", version, err)
	}

	tiers, err := p.assembleTiers(ctx, agentID, agentVersion.Content)
	if err != nil {
		return nil, err
	}

	outcomes := make([]ProfileOutcome, 0, len(promptperplexity.Profiles))
	for _, profile := range promptperplexity.Profiles {
		scored, err := promptperplexity.ScoreTiers(ctx, p.Generator, profile, tiers)
		if err != nil {
			outcomes = append(outcomes, ProfileOutcome{Profile: profile, Err: err})
			continue
		}
		if err := p.store(ctx, agentVersion.WorkspaceID, agentID, version, scored); err != nil {
			outcomes = append(outcomes, ProfileOutcome{Profile: profile, Err: err})
			continue
		}
		outcomes = append(outcomes, ProfileOutcome{Profile: profile})
	}
	return outcomes, nil
}

// ScoreBudget bounds one orchestration pass. Each version costs two model
// calls (one per runtime profile), so the ceiling is what keeps a first run on
// a deployment with months of history from turning into a few hundred calls in
// one tick. The backlog drains a few versions per pass instead.
const ScoreBudget = 3

// BacklogOutcome reports one orchestration pass.
type BacklogOutcome struct {
	Considered int
	Scored     int
	// Failed counts versions where no profile could be stored. A version that
	// scored one profile and refused the other counts as scored: the stored
	// row is real and the missing one is simply still missing, which is the
	// state the dashboard already knows how to show.
	Failed int
}

// ScoreBacklog scores agent prompt versions that have runs behind them but no
// complete set of D3 scores.
//
// It is driven off prompt_quality_daily, so a version nobody ever ran is never
// scored: D3 exists to explain the other six dimensions, and there is nothing
// to explain where there are no runs. A disabled model client makes this a
// no-op rather than an error — D3 not being scored is a state the dashboard
// renders, not a failure of the rollup.
func (p Perplexity) ScoreBacklog(ctx context.Context) (BacklogOutcome, error) {
	if p.Generator == nil || !p.Generator.Enabled() {
		return BacklogOutcome{}, nil
	}

	rows, err := p.Queries.ListPromptPerplexityUnscoredVersions(ctx, db.ListPromptPerplexityUnscoredVersionsParams{
		ProfileCount: int32(len(promptperplexity.Profiles)),
		RowLimit:     ScoreBudget,
	})
	if err != nil {
		return BacklogOutcome{}, fmt.Errorf("list unscored versions: %w", err)
	}

	out := BacklogOutcome{Considered: len(rows)}
	for _, row := range rows {
		outcomes, err := p.ScoreAgentVersion(ctx, row.AgentID, row.Version)
		if err != nil {
			// One version's failure does not abandon the rest: the backlog is
			// re-listed next pass, and a version that keeps failing keeps
			// showing as unscored rather than blocking the ones behind it.
			out.Failed++
			continue
		}
		stored := false
		for _, o := range outcomes {
			if o.Err == nil {
				stored = true
			}
		}
		if stored {
			out.Scored++
		} else {
			out.Failed++
		}
	}
	return out, nil
}

// promptVersionScopeAgent is the scope literal the prompt_version table uses.
// It is duplicated rather than imported because internal/handler owns the
// dispatch table and importing it here would invert the dependency.
const promptVersionScopeAgent = "agent"

func (p Perplexity) assembleTiers(ctx context.Context, agentID pgtype.UUID, agentContent string) (promptperplexity.Tiers, error) {
	tiers := promptperplexity.Tiers{Agent: agentContent}

	scopes, err := p.Queries.GetPromptPerplexityTierScopes(ctx, agentID)
	if err != nil {
		return tiers, fmt.Errorf("resolve tier scopes: %w", err)
	}

	ws, err := p.Queries.GetWorkspace(ctx, scopes.WorkspaceID)
	if err != nil && !isNoRows(err) {
		return tiers, fmt.Errorf("read workspace context: %w", err)
	}
	if err == nil && ws.Context.Valid {
		tiers.Workspace = ws.Context.String
	}

	if scopes.SquadID.Valid {
		squad, err := p.Queries.GetSquad(ctx, scopes.SquadID)
		if err != nil && !isNoRows(err) {
			return tiers, fmt.Errorf("read squad instructions: %w", err)
		}
		if err == nil {
			tiers.Squad = squad.Instructions
		}
	}
	return tiers, nil
}

func (p Perplexity) store(ctx context.Context, workspaceID, agentID pgtype.UUID, version int32, scored promptperplexity.Scored) error {
	// The stored evidence is the per-sub-dimension items, which carry locations
	// and justifications only. The scorer's prompt forbids quotation and the
	// parser truncates every free-text field, so no prompt body reaches this
	// column (RUYI-184 source-B rule).
	evidence, err := json.Marshal(scored.Score.Items)
	if err != nil {
		return fmt.Errorf("encode evidence: %w", err)
	}

	low, err := numericFromFloat(scored.Score.PercentLow)
	if err != nil {
		return fmt.Errorf("encode percent_low: %w", err)
	}
	high, err := numericFromFloat(scored.Score.PercentHigh)
	if err != nil {
		return fmt.Errorf("encode percent_high: %w", err)
	}

	_, err = p.Queries.UpsertPromptPerplexityScore(ctx, db.UpsertPromptPerplexityScoreParams{
		WorkspaceID:    workspaceID,
		Scope:          promptVersionScopeAgent,
		ScopeID:        agentID,
		Version:        version,
		RuntimeProfile: string(scored.Profile),
		Band:           string(scored.Score.Band),
		PercentLow:     low,
		PercentHigh:    high,
		Evidence:       evidence,
		Model:          scored.Model,
	})
	if err != nil {
		return fmt.Errorf("upsert score: %w", err)
	}
	return nil
}

var errPercentOutOfRange = errors.New("percent outside 0..100")

func numericFromFloat(v float64) (pgtype.Numeric, error) {
	if v < 0 || v > 100 {
		return pgtype.Numeric{}, errPercentOutOfRange
	}
	var n pgtype.Numeric
	if err := n.Scan(fmt.Sprintf("%.2f", v)); err != nil {
		return pgtype.Numeric{}, err
	}
	return n, nil
}
