// Rollback-suggestion generation (RUYI-751, ADR 007 §7).
//
// When a clause version's prompt-attributable failure rate is significantly
// worse than its predecessor's over a full-window comparison, the system
// drafts a rollback proposal into the proposal pool. It never rolls back by
// itself: the draft is an ordinary revise_clause proposal the Owner still has
// to approve, and the comparison is planned to run at most once per version —
// the HasRollbackSuggestionForVersion latch makes a rejected draft stay
// rejected instead of returning on every tick.
//
// The statistics follow the ADR's Owner-decided frame (决策 3): a per-version
// window needs at least MinSampleRuns runs before any percentage conclusion,
// the comparison is a two-proportion z-test at α = 0.05 against the complete
// window of V(n-1), and the numbers are ADR-suggested defaults an
// implementation may refine only with Owner sign-off.
package clausequalityrollup

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// MinSampleRuns is the per-version-window sample gate: below this many runs
// attributable to the version, the window gets no percentage conclusion at
// all ("样本不足"), only counts.
const MinSampleRuns = 30

// Alpha is the two-proportion test's significance level (ADR 007 §7).
const Alpha = 0.05

// RollbackChecker evaluates rollback candidates after a rollup tick.
type RollbackChecker struct {
	Queries *db.Queries
}

// RollbackOutcome reports what one evaluation pass did. Failures carries one
// entry per clause that could not be evaluated — a single clause's failure
// never fails the tick, but it is never silent either.
type RollbackOutcome struct {
	Evaluated int
	Suggested int
	Failures  []string
}

// versionWindow is one version's whole recorded history, summed over days.
type versionWindow struct {
	FinishedRuns   int64
	AttributableFails int64
}

// window loads a version's whole window. found=false means the version has no
// rollup rows at all — a window that has not started.
func (c RollbackChecker) window(ctx context.Context, clauseID pgtype.UUID, version int32) (versionWindow, bool, error) {
	row, err := c.Queries.SumClauseVersionWindow(ctx, db.SumClauseVersionWindowParams{
		ClauseID:      clauseID,
		ClauseVersion: version,
	})
	if err != nil {
		return versionWindow{}, false, err
	}
	return versionWindow{FinishedRuns: row.FinishedRuns, AttributableFails: row.AttributableFailedRuns}, row.FinishedRuns > 0, nil
}

// EvaluateClauses runs the planned comparison for each clause that had rollup
// activity this tick. The candidate version is always the clause's latest;
// the baseline is its complete predecessor window.
func (c RollbackChecker) EvaluateClauses(ctx context.Context, clauseIDs []pgtype.UUID) RollbackOutcome {
	var out RollbackOutcome
	for _, clauseID := range clauseIDs {
		out.Evaluated++
		if err := c.evaluateClause(ctx, clauseID); err != nil {
			out.Failures = append(out.Failures, fmt.Sprintf("clause %s: %s", uuidString(clauseID), err))
			continue
		}
	}
	return out
}

func (c RollbackChecker) evaluateClause(ctx context.Context, clauseID pgtype.UUID) error {
	latest, err := c.Queries.LatestClauseVersion(ctx, clauseID)
	if err != nil {
		return fmt.Errorf("read latest version: %w", err)
	}
	// A first version has no predecessor to compare against.
	if latest < 2 {
		return nil
	}

	latched, err := c.Queries.HasRollbackSuggestionForVersion(ctx, db.HasRollbackSuggestionForVersionParams{
		ClauseID:          clauseID,
		RollbackOfVersion: latest,
	})
	if err != nil {
		return fmt.Errorf("read suggestion latch: %w", err)
	}
	if latched > 0 {
		return nil
	}

	cur, curFound, err := c.window(ctx, clauseID, latest)
	if err != nil || !curFound {
		if err != nil {
			return fmt.Errorf("read current window: %w", err)
		}
		return nil
	}
	base, baseFound, err := c.window(ctx, clauseID, latest-1)
	if err != nil {
		return fmt.Errorf("read baseline window: %w", err)
	}
	if !baseFound {
		return nil
	}

	// Sample gate: either window under the threshold yields no percentage
	// conclusion ("样本不足"), per ADR 007 §7.
	if cur.FinishedRuns < MinSampleRuns || base.FinishedRuns < MinSampleRuns {
		return nil
	}

	z, p := twoProportionZ(base, cur)
	if !(z > 0 && p < Alpha) {
		return nil
	}

	return c.suggestRollback(ctx, clauseID, latest, base, cur, z, p)
}

func (c RollbackChecker) suggestRollback(ctx context.Context, clauseID pgtype.UUID, version int32, base, cur versionWindow, z, p float64) error {
	// The clause row carries the workspace id and the display title; the
	// active binding carries the carrier the draft must target.
	clauseRow, err := c.Queries.GetClauseByIDDirect(ctx, clauseID)
	if err != nil {
		return fmt.Errorf("read clause: %w", err)
	}
	binding, err := c.Queries.GetActiveClauseBinding(ctx, clauseID)
	if err != nil {
		// Nothing bound → nothing injected → no rollback to suggest.
		if isNoRows(err) {
			return nil
		}
		return fmt.Errorf("read active binding: %w", err)
	}
	baseVersion, err := c.Queries.GetClauseVersion(ctx, db.GetClauseVersionParams{
		ClauseID: clauseID,
		Version:  version - 1,
	})
	if err != nil {
		return fmt.Errorf("read baseline version content: %w", err)
	}

	stats := fmt.Sprintf(
		"双比例检验：V%d 归因失败率 %.2f%%（%d/%d） 对 V%d %.2f%%（%d/%d），z=%.2f，p=%.4f（α=%.2f，样本门槛 %d）。",
		version, pct(cur), cur.AttributableFails, cur.FinishedRuns,
		version-1, pct(base), base.AttributableFails, base.FinishedRuns, z, p, Alpha, MinSampleRuns,
	)
	audit, err := json.Marshal([]map[string]any{{
		"action":         "rollback_suggested",
		"actor_type":     "system",
		"rollback_of":    version,
		"baseline_of":    version - 1,
		"current_rate":   rate(cur),
		"baseline_rate":  rate(base),
		"z":              z,
		"p":              p,
		"sample_minimum": MinSampleRuns,
	}})
	if err != nil {
		return fmt.Errorf("encode audit: %w", err)
	}

	_, err = c.Queries.CreatePromptProposal(ctx, db.CreatePromptProposalParams{
		WorkspaceID:   clauseRow.WorkspaceID,
		CarrierScope:  binding.Scope,
		CarrierScopeID: binding.ScopeID,
		// revise_clause re-renders the clause at its existing marker
		// position; target_section is unused on this path.
		TargetSection: "",
		ChangeKind:    "revise_clause",
		ClauseName:    clauseRow.Title,
		// Restoring V(n-1) as new content keeps the version line append-only.
		ClauseText: baseVersion.Content,
		ClauseID:   clauseID,
		Conditions: binding.Conditions,
		GateAnswerLayer:     "系统回滚建议：由条款质量统计自动生成，层级与原条款一致",
		GateAnswerRetention: "系统回滚建议：恢复 V" + fmt.Sprint(version-1) + " 文本，保留条款身份",
		GateAnswerCost:      stats,
		GateAnswerConflict:  "系统回滚建议：恢复既往已审文本，不引入新冲突面",
		GateAnswerDedup:     "每版本仅此一次计划内对比，同版本不会重复生成",
		Problem:        pgtype.Text{String: "版本 V" + fmt.Sprint(version) + " 经检验显著劣化。", Valid: true},
		ExpectedEffect: pgtype.Text{String: "回滚至 V" + fmt.Sprint(version-1) + " 文本与行为。", Valid: true},
		ExpectedGain:   pgtype.Text{String: stats, Valid: true},
		EvidenceAnchors: []byte("[]"),
		Source:          "system",
		CreatedByType:   "system",
		CreatedByID:     pgtype.UUID{},
		AuditLog:        audit,
		RollbackOfVersion: pgtype.Int4{Int32: version, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("create rollback draft: %w", err)
	}
	return nil
}

// twoProportionZ is the pooled two-proportion z-test, one-sided for
// degradation (new worse than base). It returns the z statistic and the
// one-sided p-value; zero variance (both windows all-or-nothing at the same
// rate) yields z = 0, never a NaN.
func twoProportionZ(base, cur versionWindow) (z, p float64) {
	pb := rate(base)
	pc := rate(cur)
	pool := float64(base.AttributableFails+cur.AttributableFails) / float64(base.FinishedRuns+cur.FinishedRuns)
	se := math.Sqrt(pool * (1 - pool) * (1/float64(base.FinishedRuns) + 1/float64(cur.FinishedRuns)))
	if se == 0 {
		return 0, 0.5
	}
	z = (pc - pb) / se
	p = 0.5 * math.Erfc(z/math.Sqrt2)
	return z, p
}

func rate(w versionWindow) float64 {
	if w.FinishedRuns == 0 {
		return 0
	}
	return float64(w.AttributableFails) / float64(w.FinishedRuns)
}

func pct(w versionWindow) float64 { return rate(w) * 100 }
