// Package promptquizsweep runs the periodic quiz: it tops up the measurement
// sample of every agent's current prompt version, and folds the runs that have
// since finished into prompt_quiz_result (RUYI-185).
//
// One tick does collect-then-enqueue, in that order. Collecting first means the
// enqueue step counts the runs that just landed, so a sweep cannot over-order
// against a sample it is about to be given.
//
// Nothing here can block a prompt version from being published. The sweep only
// writes prompt_quiz_result and agent_task_queue rows; the publish path
// (handler.commitPromptGovernanceVersion) reads neither. That is the Owner Q10
// ruling expressed as a dependency direction, and it is asserted mechanically
// in handler/prompt_quiz_publish_test.go.
package promptquizsweep

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
	"github.com/multica-ai/multica/server/pkg/promptquiz"
)

// ScopeLimit bounds how many agents one tick looks at, the same bounded-step
// discipline promptqualityrollup.BucketLimit applies to a backfill.
const ScopeLimit = 100

// CollectLimit bounds how many finished runs one tick folds in.
const CollectLimit = 500

// EnqueueLimit bounds how many new measurements one tick orders. A fresh
// deployment would otherwise queue NewVersionSampleSize runs for every agent at
// once and starve real work; spreading the fill over several ticks costs a few
// extra cadence periods before the first verdict and nothing else.
const EnqueueLimit = 20

// InFlightLimit is the back-pressure ceiling. Above it the tick collects but
// orders nothing: a deployment whose agents run slower than the cadence must
// not accumulate quiz runs faster than it drains them.
const InFlightLimit = 40

// Runner executes one sweep against one database.
type Runner struct {
	Queries *db.Queries
}

// Outcome reports what a tick did, for the scheduler's audit row.
type Outcome struct {
	Collected int
	Enqueued  int
}

// Run processes one tick. It is idempotent: collection upserts on task_id, and
// enqueueing is driven by how far each version's stored sample is from N, so a
// tick that dies after ordering some runs orders only the remainder next time.
func (r Runner) Run(ctx context.Context) (Outcome, error) {
	started := time.Now()
	out, err := r.sweep(ctx)

	// The audit row records the tick either way. A failure here is logged by
	// the caller and must not mask the sweep's own error.
	rec := db.RecordPromptQuizSweepParams{
		StartedAt: pgtype.Timestamptz{Time: started, Valid: true},
		Enqueued:  int32(out.Enqueued),
		Collected: int32(out.Collected),
	}
	if err != nil {
		rec.LastError = pgtype.Text{String: err.Error(), Valid: true}
	}
	if recErr := r.Queries.RecordPromptQuizSweep(ctx, rec); recErr != nil && err == nil {
		return out, fmt.Errorf("record sweep: %w", recErr)
	}
	return out, err
}

func (r Runner) sweep(ctx context.Context) (Outcome, error) {
	var out Outcome

	collected, err := r.collect(ctx)
	out.Collected = collected
	if err != nil {
		return out, err
	}

	active, err := r.Queries.CountActivePromptQuizTasks(ctx)
	if err != nil {
		return out, fmt.Errorf("count active quiz runs: %w", err)
	}
	if int(active) >= InFlightLimit {
		return out, nil
	}

	budget := EnqueueLimit
	if room := InFlightLimit - int(active); room < budget {
		budget = room
	}
	enqueued, err := r.enqueue(ctx, budget)
	out.Enqueued = enqueued
	return out, err
}

// collect folds finished quiz runs into measurements.
//
// A run whose item is gone, whose claim recorded no agent-tier version, or whose
// runtime is unknown is SKIPPED rather than failing the tick: it measured
// something that cannot be placed on an axis the comparison uses, and one such
// run must not stop the rest of the sweep. Skipping leaves the row unmatched, so
// it is retried next tick and drops out naturally once the cause is permanent.
func (r Runner) collect(ctx context.Context) (int, error) {
	runs, err := r.Queries.ListFinishedPromptQuizTasks(ctx, CollectLimit)
	if err != nil {
		return 0, fmt.Errorf("list finished quiz runs: %w", err)
	}

	n := 0
	for _, run := range runs {
		version, ok := agentTierVersion(run.PromptVersions)
		if !ok {
			continue
		}
		// Migration 251 allows a terminal queue row to have no runtime. Storing
		// such a run would put a reading on the curve without saying what
		// measured it, and every later reading would then have to be compared
		// against an unknown instrument (A1).
		if !run.RuntimeID.Valid {
			continue
		}
		item, err := r.Queries.GetPromptQuizItem(ctx, db.GetPromptQuizItemParams{
			ID:          run.ItemID,
			WorkspaceID: run.WorkspaceID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return n, fmt.Errorf("read quiz item: %w", err)
		}

		batch, ok := batchIDFromContext(run.Context)
		if !ok {
			continue
		}

		params := db.UpsertPromptQuizResultParams{
			WorkspaceID:    run.WorkspaceID,
			Scope:          "agent",
			ScopeID:        run.AgentID,
			Version:        version,
			ItemID:         run.ItemID,
			ItemRevision:   item.Revision,
			ItemBodySha256: promptquiz.BodyDigest(item.Body),
			RuntimeID:      run.RuntimeID,
			BatchID:        batch,
			TaskID:         run.TaskID,
			Outcome:        promptquiz.OutcomeForStatus(run.Status),
		}
		// Absent when the daemon reported no usage. Left NULL rather than
		// defaulted to the agent's configured model: the configured value is not
		// evidence of what ran, and a wrong instrument label is worse than a
		// missing one — it would merge two cohorts instead of separating them.
		if len(run.RunModel) > 0 {
			params.RunModel = pgtype.Text{String: string(run.RunModel), Valid: true}
		}
		// UsageMeasured, not RunTokens > 0: a run that really reported zero
		// cost is a measurement, while a run whose usage rows never arrived is
		// not one, and only the second may be stored as NULL.
		if run.UsageMeasured {
			params.RunTokens = pgtype.Int8{Int64: run.RunTokens, Valid: true}
		}
		if run.StartedAt.Valid && run.CompletedAt.Valid {
			ms := run.CompletedAt.Time.Sub(run.StartedAt.Time).Milliseconds()
			if ms >= 0 {
				params.DurationMs = pgtype.Int8{Int64: ms, Valid: true}
			}
		}
		// Grading (RUYI-286): only a run that produced an answer can be
		// graded, and only against checks that exist. Every "not graded"
		// path leaves Score NULL — the column's "measured but not graded"
		// state — so an outage or a check-less item can never read as 0.
		// Cost attribution above stays unconditional: an errored run's
		// tokens are still what it cost, even though it never enters a
		// sample.
		//
		// GradedAt rides on every row the collector writes (RUYI-325): the
		// stamp means "the grading pass has judged this task", whether that
		// judgement produced a score or found the run not gradeable. A row
		// with graded_at NULL is by contrast one an older, score-incapable
		// build stored; ListFinishedPromptQuizTasks keeps serving those back
		// so the grade gets filled in, which is what keeps a mixed-version
		// shared database from accumulating permanent NULL placeholders.
		// The stamp is also what retires a judged row: without it the
		// not-gradeable NULLs would be re-collected every tick and crowd
		// the bounded collection window.
		params.GradedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		if run.Status == "completed" {
			grade, detail := r.gradeTask(ctx, run.TaskID, item.RubricChecks)
			if grade.Graded {
				params.Score = pgtype.Float4{Float32: float32(grade.Score), Valid: true}
				params.ScoreDetail = detail
			}
		}
		if _, err := r.Queries.UpsertPromptQuizResult(ctx, params); err != nil {
			return n, fmt.Errorf("store quiz measurement: %w", err)
		}
		n++
	}
	return n, nil
}

// gradeTask grades one finished run: the item's checks against the run's last
// text message.
//
// Both reads are allowed to come up empty without failing the tick — no
// checks on the item, no text message from the run, or a stored check set the
// write gate would have refused all grade to Graded=false, which the caller
// stores as a NULL score. The tick keeps folding the other runs; the row
// keeps saying "measured, not graded" instead of growing a fake 0.
func (r Runner) gradeTask(ctx context.Context, taskID pgtype.UUID, checksJSON []byte) (promptquiz.GradeResult, []byte) {
	if len(checksJSON) == 0 {
		return promptquiz.GradeResult{}, nil
	}
	answer, err := r.Queries.GetPromptQuizTaskAnswer(ctx, taskID)
	if err != nil {
		return promptquiz.GradeResult{}, nil
	}
	grade := promptquiz.Grade(checksJSON, answer.String)
	if !grade.Graded {
		return promptquiz.GradeResult{}, nil
	}
	detail, err := json.Marshal(grade.Detail)
	if err != nil {
		return promptquiz.GradeResult{}, nil
	}
	return grade, detail
}

// enqueue orders measurements for the versions furthest from N.
func (r Runner) enqueue(ctx context.Context, budget int) (int, error) {
	if budget <= 0 {
		return 0, nil
	}
	scopes, err := r.Queries.ListPromptQuizScopesForSweep(ctx, ScopeLimit)
	if err != nil {
		return 0, fmt.Errorf("list quiz scopes: %w", err)
	}

	batch := newUUID()
	n := 0
	for _, s := range scopes {
		if n >= budget {
			break
		}
		have, err := r.Queries.CountPromptQuizMeasurementsForVersion(ctx, db.CountPromptQuizMeasurementsForVersionParams{
			Scope:   "agent",
			ScopeID: s.AgentID,
			Version: s.Version,
		})
		if err != nil {
			return n, fmt.Errorf("count measurements: %w", err)
		}
		// Stored plus already-ordered. Counting only the stored ones would make
		// the sweep re-order the same remainder on every tick until the first
		// run finishes, which is the difference between "idempotent" and
		// "eventually reaches N".
		missing := promptquiz.NewVersionSampleSize - int(have.Graded) - int(have.InFlight)
		if missing <= 0 {
			continue
		}

		items, err := r.Queries.ListActivePromptQuizItemsForProfile(ctx, db.ListActivePromptQuizItemsForProfileParams{
			WorkspaceID:    s.WorkspaceID,
			RuntimeProfile: "member",
		})
		if err != nil {
			return n, fmt.Errorf("list quiz items: %w", err)
		}
		if len(items) == 0 {
			continue
		}

		for i := 0; i < missing && n < budget; i++ {
			item := items[i%len(items)]
			payload, err := taskContext(item, batch)
			if err != nil {
				return n, err
			}
			_, err = r.Queries.CreatePromptQuizTask(ctx, db.CreatePromptQuizTaskParams{
				AgentID: s.AgentID,
				// Quiz runs sit below everything a human is waiting on. A
				// measurement that lands a cadence later is still a valid
				// measurement; a real task that waits behind one is not.
				RuntimeID: s.RuntimeID,
				Priority:  0,
				Context:   payload,
				ItemID:    item.ID,
			})
			// No row means the fence refused: the agent or the runtime behind this
			// scope is being deleted, or its workspace is. There is nothing left to
			// measure for the scope, so stop ordering runs against it rather than
			// failing the whole tick — the other scopes' samples are still valid.
			if errors.Is(err, pgx.ErrNoRows) {
				break
			}
			if err != nil {
				return n, fmt.Errorf("enqueue quiz run: %w", err)
			}
			n++
		}
	}
	return n, nil
}

// taskContext is the payload the daemon receives. It carries the question and
// nothing else: no issue, no workspace entity, no prior measurement, and not the
// item's private half. That is what makes two measurements of the same item
// comparable — the run's whole input is the prompt under test plus a fixed
// string.
//
// The A2 guarantee is structural, not a matter of care here: the parameter type
// is the row ListActivePromptQuizItemsForProfile returns, which has no rubric
// field, so this function could not send the answer key even by mistake.
// sweep_test.go asserts the payload's field set rather than trusting that.
func taskContext(item db.ListActivePromptQuizItemsForProfileRow, batch pgtype.UUID) ([]byte, error) {
	return TaskContextPayload(uuid.UUID(item.ID.Bytes).String(), item.Body, batch)
}

// TaskContextPayload builds the payload a quiz run's daemon receives, exported
// for the batch endpoint (RUYI-286), which orders runs outside a sweep tick.
//
// It carries the question and nothing else: no issue, no workspace entity, no
// prior measurement, and none of the item's private halves. That is what makes
// two measurements of the same item comparable — the run's whole input is the
// prompt under test plus a fixed string. The guarantee is the caller's to keep
// on this path: pass the body and the ids, nothing more.
func TaskContextPayload(itemID, body string, batch pgtype.UUID) ([]byte, error) {
	payload, err := json.Marshal(map[string]any{
		"kind":          promptquiz.TaskKind,
		"quiz_batch_id": uuid.UUID(batch.Bytes).String(),
		"quiz_item_id":  itemID,
		"quiz_prompt":   body,
	})
	if err != nil {
		return nil, fmt.Errorf("encode quiz context: %w", err)
	}
	return payload, nil
}

// agentTierVersion reads the agent-tier prompt version the run was claimed
// with. A key that is absent means the tier was not injected (migration 917's
// convention), which is not version 0 — such a run measured no version and is
// skipped rather than attributed to one.
func agentTierVersion(raw []byte) (int32, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var versions map[string]int32
	if err := json.Unmarshal(raw, &versions); err != nil {
		return 0, false
	}
	v, ok := versions["agent"]
	if !ok || v <= 0 {
		return 0, false
	}
	return v, true
}

func batchIDFromContext(raw []byte) (pgtype.UUID, bool) {
	if len(raw) == 0 {
		return pgtype.UUID{}, false
	}
	var ctxPayload struct {
		BatchID string `json:"quiz_batch_id"`
	}
	if err := json.Unmarshal(raw, &ctxPayload); err != nil {
		return pgtype.UUID{}, false
	}
	parsed, err := uuid.Parse(ctxPayload.BatchID)
	if err != nil {
		return pgtype.UUID{}, false
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, true
}

func newUUID() pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.New(), Valid: true}
}
