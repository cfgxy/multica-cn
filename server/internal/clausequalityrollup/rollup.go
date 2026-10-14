// Package clausequalityrollup recomputes clause_quality_daily buckets from
// clause_injection events joined to the platform's run data (RUYI-751, ADR 007
// §7).
//
// The bucket key is (workspace, clause, clause_version, day): the window key
// is the clause version at injection time and the aggregate day is the run's
// terminal day, exactly as the ADR pins them. One tick reads the watermark,
// lists the buckets a run finished in since then, and rewrites each one from
// scratch — the same "list dirty, recompute whole bucket" contract as the
// prompt rollup, so an upsert that overwrites converges no matter how often a
// bucket is re-scanned.
//
// The folding itself is pkg/promptquality.Aggregate, reused whole: a run that
// had a clause injected is still just a run, and the D1/D2/D4/D5/D6/D7 rules
// must not fork between the carrier-level and clause-level views. This file is
// the database seam: fetch, convert, call, write. D1's static half is the
// injected clause's own rendered text (clause_version.content), not the whole
// carrier.
package clausequalityrollup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/promptdiscipline"
	"github.com/multica-ai/multica/server/pkg/promptquality"
)

// BucketLimit bounds one tick. A deployment that has been idle for weeks walks
// its history in bounded steps instead of holding one enormous read.
const BucketLimit = 200

// Runner recomputes buckets against one database.
type Runner struct {
	Queries *db.Queries
}

// Outcome reports what a tick did, for the scheduler's audit row. ClauseIDs
// lists the distinct clauses that had activity this tick, in first-seen
// order — the rollback checker's candidate list, so the planned per-version
// comparison only runs for clauses whose data actually moved.
type Outcome struct {
	Buckets         int
	ClauseIDs       []pgtype.UUID
	WatermarkBefore time.Time
	WatermarkAfter  time.Time
}

// Run processes one tick. It advances the watermark only to the newest
// completed_at it actually consumed, so a bucket that arrived after the list
// query is picked up next tick rather than skipped.
func (r Runner) Run(ctx context.Context) (Outcome, error) {
	state, err := r.Queries.GetClauseQualityWatermark(ctx)
	if err != nil {
		return Outcome{}, fmt.Errorf("read watermark: %w", err)
	}

	buckets, err := r.Queries.ListClauseQualityDirtyBuckets(ctx, db.ListClauseQualityDirtyBucketsParams{
		Watermark: state.WatermarkAt,
		RowLimit:  BucketLimit,
	})
	if err != nil {
		return Outcome{}, fmt.Errorf("list dirty buckets: %w", err)
	}

	out := Outcome{WatermarkBefore: state.WatermarkAt.Time}
	if len(buckets) == 0 {
		out.WatermarkAfter = state.WatermarkAt.Time
		return out, nil
	}

	seen := map[[16]byte]bool{}
	highest := state.WatermarkAt
	for _, b := range buckets {
		if err := r.recompute(ctx, b); err != nil {
			// Stop at the first failure without advancing: the buckets
			// already written are correct, and the ones behind them are
			// re-listed next tick because the watermark did not move.
			return out, fmt.Errorf("recompute bucket clause %s v%d: %w", uuidString(b.ClauseID), b.ClauseVersion, err)
		}
		out.Buckets++
		if !seen[b.ClauseID.Bytes] {
			seen[b.ClauseID.Bytes] = true
			out.ClauseIDs = append(out.ClauseIDs, b.ClauseID)
		}
		if b.MaxCompletedAt.Valid && (!highest.Valid || b.MaxCompletedAt.Time.After(highest.Time)) {
			highest = b.MaxCompletedAt
		}
	}

	if err := r.Queries.AdvanceClauseQualityWatermark(ctx, db.AdvanceClauseQualityWatermarkParams{
		Watermark:    highest,
		RowsAffected: int64(out.Buckets),
	}); err != nil {
		return out, fmt.Errorf("advance watermark: %w", err)
	}
	out.WatermarkAfter = highest.Time
	return out, nil
}

func (r Runner) recompute(ctx context.Context, b db.ListClauseQualityDirtyBucketsRow) error {
	runs, err := r.Queries.ListClauseQualityBucketRuns(ctx, db.ListClauseQualityBucketRunsParams{
		ClauseID:      b.ClauseID,
		ClauseVersion: b.ClauseVersion,
		Day:           b.Day,
	})
	if err != nil {
		return fmt.Errorf("list runs: %w", err)
	}

	taskIDs := make([]pgtype.UUID, 0, len(runs))
	issueSet := map[[16]byte]pgtype.UUID{}
	in := promptquality.Input{
		Runs:       make([]promptquality.Run, 0, len(runs)),
		Discipline: map[string]promptdiscipline.Result{},
	}
	for _, run := range runs {
		taskIDs = append(taskIDs, run.TaskID)
		if run.IssueID.Valid {
			issueSet[run.IssueID.Bytes] = run.IssueID
		}
		in.Runs = append(in.Runs, promptquality.Run{
			TaskID:        uuidString(run.TaskID),
			IssueID:       uuidString(run.IssueID),
			Status:        run.Status,
			FailureReason: run.FailureReason,
			Attempt:       int(run.Attempt),
			UsageMeasured: run.UsageMeasured,
			RunTokens:     run.RunTokens,
		})
	}

	if len(taskIDs) > 0 {
		counts, err := r.Queries.CountPromptQualityToolResults(ctx, taskIDs)
		if err != nil {
			return fmt.Errorf("count tool results: %w", err)
		}
		in.ToolResults = promptquality.ToolResultCounts{Measured: counts.Measured, Errored: counts.Errored}

		messages, err := r.Queries.ListPromptQualityDisciplineMessages(ctx, taskIDs)
		if err != nil {
			return fmt.Errorf("list discipline messages: %w", err)
		}
		in.Discipline = scoreDiscipline(messages)
	}

	if len(issueSet) > 0 {
		issueIDs := make([]pgtype.UUID, 0, len(issueSet))
		for _, id := range issueSet {
			issueIDs = append(issueIDs, id)
		}
		reviews, err := r.Queries.ListPromptQualityIssueReviewOutcomes(ctx, issueIDs)
		if err != nil {
			return fmt.Errorf("list review outcomes: %w", err)
		}
		for _, rev := range reviews {
			in.Reviews = append(in.Reviews, promptquality.IssueReview{
				IssueID:       uuidString(rev.IssueID),
				EnteredReview: int(rev.EnteredReview),
				SentBack:      int(rev.SentBack),
			})
		}
	}

	// D1's static half for the clause view: the tokens of the clause text as
	// actually injected, estimated from the version's content. A version whose
	// content row is gone — pruned history — leaves injected_tokens NULL
	// rather than 0.
	content, err := r.Queries.GetClauseVersion(ctx, db.GetClauseVersionParams{
		ClauseID: b.ClauseID,
		Version:  b.ClauseVersion,
	})
	if err == nil {
		n := promptquality.EstimateTokens(content.Content)
		in.InjectedTokens = &n
	} else if !isNoRows(err) {
		return fmt.Errorf("read clause version content: %w", err)
	}

	result := promptquality.Aggregate(in)

	deductions, err := json.Marshal(result.Deductions)
	if err != nil {
		return fmt.Errorf("encode deductions: %w", err)
	}
	if result.Deductions == nil {
		deductions = []byte("[]")
	}
	reasons, err := json.Marshal(result.FailureReasonCounts)
	if err != nil {
		return fmt.Errorf("encode failure reasons: %w", err)
	}

	return r.Queries.UpsertClauseQualityDaily(ctx, db.UpsertClauseQualityDailyParams{
		WorkspaceID:            b.WorkspaceID,
		ClauseID:               b.ClauseID,
		ClauseVersion:          b.ClauseVersion,
		Day:                    b.Day,
		FinishedRuns:           int32(result.FinishedRuns),
		InjectedTokens:         nullInt8(result.InjectedTokens),
		RunTokensMedian:        nullInt8(result.RunTokensMedian),
		DisciplineScoreMedian:  nullNumeric(result.DisciplineScoreMedian),
		DisciplineCoveredRuns:  int32(result.DisciplineCoveredRuns),
		DisciplineDeductions:   deductions,
		ToolResultsMeasured:    result.ToolResultsMeasured,
		ToolResultsError:       result.ToolResultsError,
		AttemptTotal:           int32(result.AttemptTotal),
		RetriedRuns:            int32(result.RetriedRuns),
		AttributableFailedRuns: int32(result.AttributableFailedRuns),
		ExcludedFailedRuns:     int32(result.ExcludedFailedRuns),
		FailureReasonCounts:    reasons,
		FirstPassIssues:        int32(result.FirstPassIssues),
		ReviewedIssues:         int32(result.ReviewedIssues),
	})
}

// scoreDiscipline groups the bucket's tool_use rows by run and scores each run
// on its own. A run whose input JSON will not decode contributes an
// uninspectable message rather than failing the tick: one malformed row must
// not stop a whole day's rollup.
func scoreDiscipline(messages []db.ListPromptQualityDisciplineMessagesRow) map[string]promptdiscipline.Result {
	byTask := map[string][]promptdiscipline.Message{}
	for _, m := range messages {
		msg := promptdiscipline.Message{
			Seq:  int(m.Seq),
			Type: m.Type,
			Tool: m.Tool,
		}
		if len(m.Input) > 0 {
			var input map[string]any
			if err := json.Unmarshal(m.Input, &input); err == nil {
				msg.Input = input
			}
		}
		id := uuidString(m.TaskID)
		byTask[id] = append(byTask[id], msg)
	}
	out := make(map[string]promptdiscipline.Result, len(byTask))
	for id, msgs := range byTask {
		out[id] = promptdiscipline.Score(msgs)
	}
	return out
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}

func nullInt8(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}

func nullNumeric(v *int) pgtype.Numeric {
	if v == nil {
		return pgtype.Numeric{}
	}
	var n pgtype.Numeric
	if err := n.Scan(fmt.Sprintf("%d", *v)); err != nil {
		return pgtype.Numeric{}
	}
	return n
}
