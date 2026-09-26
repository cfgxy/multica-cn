// Package promptqualityrollup recomputes prompt_quality_daily buckets from the
// platform's own run data (RUYI-184).
//
// One tick reads the watermark, lists the buckets a run finished in since
// then, and rewrites each one from scratch. Recomputing rather than
// incrementing is deliberate: an upsert that overwrites converges no matter how
// often a bucket is re-scanned, which is what lets this rollup run off a bare
// watermark with no dirty queue (migration 925 records the same reasoning).
//
// The folding itself lives in pkg/promptquality and is pure. This file is the
// database seam: fetch, convert, call, write.
package promptqualityrollup

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

// Outcome reports what a tick did, for the scheduler's audit row.
type Outcome struct {
	Buckets         int
	WatermarkBefore time.Time
	WatermarkAfter  time.Time
}

// Run processes one tick. It advances the watermark only to the newest
// completed_at it actually consumed, so a bucket that arrived after the list
// query is picked up next tick rather than skipped.
func (r Runner) Run(ctx context.Context) (Outcome, error) {
	state, err := r.Queries.GetPromptQualityWatermark(ctx)
	if err != nil {
		return Outcome{}, fmt.Errorf("read watermark: %w", err)
	}

	buckets, err := r.Queries.ListPromptQualityDirtyBuckets(ctx, db.ListPromptQualityDirtyBucketsParams{
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

	highest := state.WatermarkAt
	for _, b := range buckets {
		if err := r.recompute(ctx, b); err != nil {
			// Stop at the first failure without advancing: the buckets
			// already written are correct, and the ones behind them are
			// re-listed next tick because the watermark did not move.
			return out, fmt.Errorf("recompute bucket %s/%s v%d: %w", b.Scope, uuidString(b.ScopeID), b.Version, err)
		}
		out.Buckets++
		if b.MaxCompletedAt.Valid && (!highest.Valid || b.MaxCompletedAt.Time.After(highest.Time)) {
			highest = b.MaxCompletedAt
		}
	}

	if err := r.Queries.AdvancePromptQualityWatermark(ctx, db.AdvancePromptQualityWatermarkParams{
		Watermark:    highest,
		RowsAffected: int64(out.Buckets),
	}); err != nil {
		return out, fmt.Errorf("advance watermark: %w", err)
	}
	out.WatermarkAfter = highest.Time
	return out, nil
}

func (r Runner) recompute(ctx context.Context, b db.ListPromptQualityDirtyBucketsRow) error {
	runs, err := r.Queries.ListPromptQualityBucketRuns(ctx, db.ListPromptQualityBucketRunsParams{
		Day:     b.Day,
		Scope:   b.Scope,
		Version: b.Version,
		ScopeID: b.ScopeID,
	})
	if err != nil {
		return fmt.Errorf("list runs: %w", err)
	}

	taskIDs := make([]pgtype.UUID, 0, len(runs))
	issueSet := map[[16]byte]pgtype.UUID{}
	for _, run := range runs {
		taskIDs = append(taskIDs, run.TaskID)
		if run.IssueID.Valid {
			issueSet[run.IssueID.Bytes] = run.IssueID
		}
	}

	in := promptquality.Input{
		Runs:       make([]promptquality.Run, 0, len(runs)),
		Discipline: map[string]promptdiscipline.Result{},
	}
	for _, run := range runs {
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

	// D1's static half. A version whose content row is gone — deleted scope,
	// pruned history — leaves injected_tokens NULL rather than 0.
	content, err := r.Queries.GetPromptVersionContent(ctx, db.GetPromptVersionContentParams{
		Scope:   b.Scope,
		ScopeID: b.ScopeID,
		Version: b.Version,
	})
	if err == nil {
		n := promptquality.EstimateTokens(content)
		in.InjectedTokens = &n
	} else if !isNoRows(err) {
		return fmt.Errorf("read version content: %w", err)
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

	return r.Queries.UpsertPromptQualityDaily(ctx, db.UpsertPromptQualityDailyParams{
		WorkspaceID:            b.WorkspaceID,
		Scope:                  b.Scope,
		ScopeID:                b.ScopeID,
		Version:                b.Version,
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
