package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/featureflags"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Runtime cost budgets (RUYI-665): per-runtime model spend caps over UTC
// daily/weekly/monthly windows, enforced as a hard refusal at enqueue time.
// Design decisions adopted from the furtherref evaluation (RUYI-653):
// no running counters — every check re-aggregates from task_usage over the
// window, so pricing-table updates and late usage reports never drift from the
// enforcement figure, and a lowered cap takes effect on the next enqueue.

// RuntimeBudgetScope names which row refused a run.
type RuntimeBudgetScope string

const (
	RuntimeBudgetScopeRuntime RuntimeBudgetScope = "runtime"
	RuntimeBudgetScopeUser    RuntimeBudgetScope = "user"
)

// RuntimeBudgetExceededError is returned by the enqueue helpers when a
// configured limit for the target runtime is already spent. Handlers map it to
// dispatch.ReasonBudgetExceeded with errors.As; it is never matched by string.
type RuntimeBudgetExceededError struct {
	Scope       RuntimeBudgetScope
	Period      obsmetrics.Period
	RuntimeID   pgtype.UUID
	UserID      pgtype.UUID
	UsedTicks   int64
	LimitTicks  int64
	PeriodStart time.Time
	ResetAt     time.Time
}

func (e *RuntimeBudgetExceededError) Error() string {
	return fmt.Sprintf("runtime cost budget exceeded: %s %s limit %.2f USD, used %.2f USD, resets %s",
		e.Scope, e.Period, obsmetrics.TicksToUSD(e.LimitTicks), obsmetrics.TicksToUSD(e.UsedTicks), e.ResetAt.UTC().Format(time.RFC3339))
}

// PublicReason names the refusal without any amount. Use it everywhere the
// message is persisted or broadcast beyond the people who set the budget:
// agent_task_queue.error (which task:failed republishes to every workspace
// subscriber). A limit or a running total in one of those strings would hand
// the figures to the whole workspace. Error() keeps the amounts for server
// logs and the recipient-scoped inbox notice keeps them for the people
// entitled to see them.
func (e *RuntimeBudgetExceededError) PublicReason() string {
	return fmt.Sprintf("runtime cost budget reached (%s %s)", e.Scope, e.Period)
}

// budgetLimit returns the configured limit of one period on a row, or false.
func budgetLimit(row db.RuntimeCostBudget, p obsmetrics.Period) (int64, bool) {
	var v pgtype.Int8
	switch p {
	case obsmetrics.PeriodDaily:
		v = row.DailyLimitUsdTicks
	case obsmetrics.PeriodWeekly:
		v = row.WeeklyLimitUsdTicks
	case obsmetrics.PeriodMonthly:
		v = row.MonthlyLimitUsdTicks
	}
	return v.Int64, v.Valid
}

// runtimeSpend is the priced spend of one runtime, for every budget period,
// built once from a single ListRuntimeSpendByOwner query. total covers every
// task on the runtime including agents nobody owns; byOwner is keyed by the
// agent owner's uuid string and backs the per-user scopes.
type runtimeSpend struct {
	total   map[obsmetrics.Period]int64
	byOwner map[obsmetrics.Period]map[string]int64
}

// ticks returns the spend of one scope in one period. An invalid ownerUserID
// asks for the runtime total; a user with no spend reads as zero. Only the
// periods loadRuntimeSpend was asked for are complete — a period outside that
// set was never scanned back to its own start, so it reads low.
func (s runtimeSpend) ticks(p obsmetrics.Period, ownerUserID pgtype.UUID) int64 {
	if !ownerUserID.Valid {
		return s.total[p]
	}
	return s.byOwner[p][util.UUIDToString(ownerUserID)]
}

// budgetPeriods returns the periods that carry a configured limit on at least
// one of the given rows, in AllPeriods order. Those are exactly the periods a
// caller ever reads back out of the loaded spend — evaluateBudgetRow and
// scopeStatus both skip a period with no limit — so they are also the only
// ones the spend query has to reach back for.
func budgetPeriods(rows []db.RuntimeCostBudget) []obsmetrics.Period {
	out := make([]obsmetrics.Period, 0, len(obsmetrics.AllPeriods))
	for _, p := range obsmetrics.AllPeriods {
		for _, row := range rows {
			if _, ok := budgetLimit(row, p); ok {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// budgetScanFloor returns the earliest window start any caller will read back:
// the earliest PeriodStart among the periods that carry a limit. A runtime
// whose only budget is a daily cap scans from midnight, not from a month ago.
func budgetScanFloor(now time.Time, periods []obsmetrics.Period) time.Time {
	floor := obsmetrics.PeriodStart(now, periods[0])
	for _, p := range periods[1:] {
		if start := obsmetrics.PeriodStart(now, p); start.Before(floor) {
			floor = start
		}
	}
	return floor
}

// loadRuntimeSpend reads the spend of every scope in one query, so a check or a
// status read costs one aggregate over task_usage instead of one per (budget
// row, period).
//
// periods names which windows the caller will actually read; it comes from
// budgetPeriods, and only those periods are guaranteed correct in the result.
// It decides the query's scan floor: a runtime whose only budget is a daily cap
// scans from midnight, not from the LEAST of all three period starts, which
// would have read a month of task_usage on every enqueue to answer a question
// about today. The three window starts themselves are unchanged, so each
// requested period still picks its own rows out of that scan.
//
// The database returns raw sums per (owner, UTC pricing date, provider, model);
// pricing stays in Go so the rate table, not the query, decides what an
// uncosted token costs.
func loadRuntimeSpend(ctx context.Context, q *db.Queries, runtimeID pgtype.UUID, now time.Time, periods []obsmetrics.Period) (runtimeSpend, error) {
	spend := runtimeSpend{
		total:   make(map[obsmetrics.Period]int64, len(obsmetrics.AllPeriods)),
		byOwner: make(map[obsmetrics.Period]map[string]int64, len(obsmetrics.AllPeriods)),
	}
	for _, p := range obsmetrics.AllPeriods {
		spend.byOwner[p] = map[string]int64{}
	}
	// No period carries a limit, so nothing will be read: an all-zero spend is
	// the right answer and costs no query. Both callers have a fast path that
	// returns before this, so it only guards against a row whose three limits
	// are NULL (which the API deletes rather than stores).
	if len(periods) == 0 {
		return spend, nil
	}
	scanFloor := budgetScanFloor(now, periods)
	rows, err := q.ListRuntimeSpendByOwner(ctx, db.ListRuntimeSpendByOwnerParams{
		RuntimeID: runtimeID,
		ScanFloor: pgtype.Timestamptz{Time: scanFloor, Valid: true},
	})
	if err != nil {
		return runtimeSpend{}, fmt.Errorf("list runtime spend: %w", err)
	}
	add := func(p obsmetrics.Period, owner pgtype.UUID, ticks int64) {
		spend.total[p] += ticks
		if owner.Valid {
			spend.byOwner[p][util.UUIDToString(owner)] += ticks
		}
	}
	for _, r := range rows {
		occurredAt := r.PricingDate.Time
		ticks := obsmetrics.EstimateCostTicks(
			r.Provider,
			r.Model,
			r.CostUsdTicks,
			r.UncostedInputTokens,
			r.UncostedOutputTokens,
			r.UncostedCacheReadTokens,
			r.UncostedCacheWriteTokens,
		)
		for _, p := range obsmetrics.AllPeriods {
			if !occurredAt.Before(obsmetrics.PeriodStart(now, p)) {
				add(p, r.OwnerID, ticks)
			}
		}
	}
	return spend, nil
}

// evaluateBudgetRow checks every configured period of one row against the
// already-loaded spend and returns the first reached limit.
func evaluateBudgetRow(row db.RuntimeCostBudget, scope RuntimeBudgetScope, spend runtimeSpend, now time.Time) *RuntimeBudgetExceededError {
	for _, p := range obsmetrics.AllPeriods {
		limit, ok := budgetLimit(row, p)
		if !ok {
			continue
		}
		used := spend.ticks(p, row.UserID)
		if used >= limit {
			return &RuntimeBudgetExceededError{
				Scope: scope, Period: p, RuntimeID: row.RuntimeID, UserID: row.UserID,
				UsedTicks: used, LimitTicks: limit, PeriodStart: obsmetrics.PeriodStart(now, p),
				ResetAt: obsmetrics.NextPeriodStart(now, p),
			}
		}
	}
	return nil
}

// scopedBudgetRow pairs a budget row with the scope it enforces for one agent.
type scopedBudgetRow struct {
	row   db.RuntimeCostBudget
	scope RuntimeBudgetScope
}

// applicableBudgetRows selects the rows of a runtime that bind one agent: the
// runtime-total row (user_id NULL) always, plus the row scoped to the agent's
// owner if there is one. Rows belonging to other owners are dropped, which is
// what makes a runtime whose budgets all belong to someone else cost no spend
// query at all — the caller returns on an empty result before pricing anything.
func applicableBudgetRows(rows []db.RuntimeCostBudget, agent db.Agent) []scopedBudgetRow {
	applicable := make([]scopedBudgetRow, 0, len(rows))
	for _, row := range rows {
		scope := RuntimeBudgetScopeUser
		if !row.UserID.Valid {
			scope = RuntimeBudgetScopeRuntime
		} else if !agent.OwnerID.Valid || util.UUIDToString(row.UserID) != util.UUIDToString(agent.OwnerID) {
			continue
		}
		applicable = append(applicable, scopedBudgetRow{row: row, scope: scope})
	}
	return applicable
}

// budgetRowsOf drops the scopes, for budgetPeriods.
func budgetRowsOf(applicable []scopedBudgetRow) []db.RuntimeCostBudget {
	rows := make([]db.RuntimeCostBudget, 0, len(applicable))
	for _, a := range applicable {
		rows = append(rows, a.row)
	}
	return rows
}

// firstReachedBudget evaluates one agent's applicable rows against
// already-loaded spend and returns the row that refused with its refusal, or a
// nil error when every configured limit still has room. Pure: it neither reads
// the database nor decides what a refusal means, so the enqueue gate (and the
// phase-2 deferred sweep) can share one evaluation over one loaded spend.
func firstReachedBudget(applicable []scopedBudgetRow, spend runtimeSpend, now time.Time) (db.RuntimeCostBudget, *RuntimeBudgetExceededError) {
	for _, a := range applicable {
		if exceeded := evaluateBudgetRow(a.row, a.scope, spend, now); exceeded != nil {
			return a.row, exceeded
		}
	}
	return db.RuntimeCostBudget{}, nil
}

// budgetWarnOnly reports whether budget enforcement is degraded to warn-only
// (featureflags.RuntimeCostBudgetWarnOnly, env FF_RUNTIME_COST_BUDGET_WARN_ONLY).
// The default is enforce, and the nil-safe flag service keeps a nil or
// unreadable provider on that default, so degradation is always a deliberate
// switch — never an accident of configuration.
func (s *TaskService) budgetWarnOnly(ctx context.Context) bool {
	return featureflags.RuntimeCostBudgetWarnOnlyEnabled(ctx, s.FeatureFlags)
}

// checkRuntimeCostBudget refuses the enqueue when the agent's runtime total or
// the agent owner's per-user budget is spent for the current UTC period. It
// runs after attribution in every enqueue helper. Workspaces without budgets
// pay one indexed lookup and return immediately; a runtime whose only budgets
// belong to other owners costs the same, because the spend query is issued
// only once a row that applies to this agent is known. A database error is
// returned as-is so the enqueue fails closed rather than silently spending.
//
// In warn-only mode a spent budget is logged and noticed but does not refuse:
// the branch returns nil so every caller (enqueue helpers, autopilot dispatch,
// the deferred sweep) degrades together through this single point.
func (s *TaskService) checkRuntimeCostBudget(ctx context.Context, q *db.Queries, agent db.Agent, now time.Time) error {
	if !agent.RuntimeID.Valid {
		return nil
	}
	rows, err := q.ListRuntimeCostBudgets(ctx, agent.RuntimeID)
	if err != nil {
		return fmt.Errorf("list runtime cost budgets: %w", err)
	}
	applicable := applicableBudgetRows(rows, agent)
	if len(applicable) == 0 {
		return nil
	}
	spend, err := loadRuntimeSpend(ctx, q, agent.RuntimeID, now, budgetPeriods(budgetRowsOf(applicable)))
	if err != nil {
		return err
	}
	row, exceeded := firstReachedBudget(applicable, spend, now)
	if exceeded == nil {
		return nil
	}
	if s.budgetWarnOnly(ctx) {
		slog.Warn("runtime cost budget reached: warn-only mode allows the run",
			"runtime_id", util.UUIDToString(row.RuntimeID), "scope", exceeded.Scope, "period", exceeded.Period,
			"used_ticks", exceeded.UsedTicks, "limit_ticks", exceeded.LimitTicks)
		// s.Queries, never q: q may be the caller's transaction, and the warn
		// path can run inside transactions (autopilot create_issue) that must
		// keep committing.
		s.notifyRuntimeBudgetExceeded(ctx, s.Queries, row, exceeded, agent.OwnerID, true)
		return nil
	}
	slog.Info("task enqueue refused: runtime cost budget reached",
		"runtime_id", util.UUIDToString(row.RuntimeID), "scope", exceeded.Scope, "period", exceeded.Period,
		"used_ticks", exceeded.UsedTicks, "limit_ticks", exceeded.LimitTicks)
	// s.Queries, never q: q may be the caller's transaction, and the refusal
	// returned below always rolls that transaction back.
	s.notifyRuntimeBudgetExceeded(ctx, s.Queries, row, exceeded, agent.OwnerID, false)
	return exceeded
}

// notifyRuntimeBudgetExceeded creates one "limit reached" inbox item per
// period for the scope that refused the run. Recipients: the owner of the
// agent whose run was refused (blockedOwnerID) and the runtime owner,
// de-duplicated. Both scopes use that same set. Keying the first recipient on
// exceeded.UserID instead would notify nobody but the runtime owner for a
// runtime-total refusal, because that scope's row has no user_id — and the
// person whose run was refused is exactly who needs to know. The recipient set
// and notice details are built first, and MarkRuntimeCostBudgetNotified only
// claims the period once a recipient is known — an empty recipient set (a
// runtime-scope budget on a runtime with no owner) or a lookup/marshal
// failure must not burn the claim, or a later refusal that could resolve a
// recipient would never retry. Once claimed, concurrent refusals produce one
// notice.
//
// It must NOT use the caller's transaction. checkRuntimeCostBudget refuses the
// enqueue right after calling this, and on the chat paths that refusal rolls
// the enclosing transaction back — a notification or MarkRuntimeCostBudgetNotified
// marker written on that handle would be discarded with it. Callers pass the
// auto-commit s.Queries, and the implementation must keep using the handle it
// is given rather than reaching for a transaction of the enqueue.
//
// warnOnly adjusts the copy: the enforce body tells the recipient runs are
// refused, which would be false while the warn-only switch is on. The inbox
// type and the details map stay the same shape, with warn_only added so a
// client can tell the modes apart without parsing the body.
func (s *TaskService) notifyRuntimeBudgetExceeded(ctx context.Context, q *db.Queries, row db.RuntimeCostBudget, exceeded *RuntimeBudgetExceededError, blockedOwnerID pgtype.UUID, warnOnly bool) {
	rt, err := RuntimeLookup{Queries: q, Metrics: s.Metrics, Source: obsmetrics.RuntimeLookupSourceTask}.Get(ctx, row.RuntimeID)
	if err != nil {
		slog.Warn("runtime budget notice: load runtime failed", "runtime_id", util.UUIDToString(row.RuntimeID), "error", err)
		return
	}
	recipients := map[string]pgtype.UUID{}
	if blockedOwnerID.Valid {
		recipients[util.UUIDToString(blockedOwnerID)] = blockedOwnerID
	}
	if rt.OwnerID.Valid {
		recipients[util.UUIDToString(rt.OwnerID)] = rt.OwnerID
	}
	if len(recipients) == 0 {
		slog.Debug("runtime budget notice: no recipients, not claiming period",
			"runtime_id", util.UUIDToString(row.RuntimeID), "scope", exceeded.Scope, "period", exceeded.Period)
		return
	}
	// inbox_item.details is a flat string map on the wire (InboxItem.details is
	// Record<string, string> in packages/core/types/inbox.ts), so amounts are
	// formatted here and an absent user_id is omitted rather than sent as null.
	detailFields := map[string]string{
		"scope":        string(exceeded.Scope),
		"period":       string(exceeded.Period),
		"runtime_id":   util.UUIDToString(row.RuntimeID),
		"used_usd":     strconv.FormatFloat(obsmetrics.TicksToUSD(exceeded.UsedTicks), 'f', 2, 64),
		"limit_usd":    strconv.FormatFloat(obsmetrics.TicksToUSD(exceeded.LimitTicks), 'f', 2, 64),
		"period_start": exceeded.PeriodStart.UTC().Format(time.RFC3339),
		"reset_at":     exceeded.ResetAt.UTC().Format(time.RFC3339),
	}
	if exceeded.UserID.Valid {
		detailFields["user_id"] = util.UUIDToString(exceeded.UserID)
	}
	if warnOnly {
		detailFields["warn_only"] = "true"
	}
	details, err := json.Marshal(detailFields)
	if err != nil {
		slog.Warn("runtime budget notice: marshal details failed", "error", err)
		return
	}
	claimed, err := q.MarkRuntimeCostBudgetNotified(ctx, db.MarkRuntimeCostBudgetNotifiedParams{
		Period:      string(exceeded.Period),
		PeriodStart: pgtype.Timestamptz{Time: exceeded.PeriodStart, Valid: true},
		ID:          row.ID,
	})
	if err != nil {
		slog.Warn("runtime budget notice claim failed", "budget_id", util.UUIDToString(row.ID), "error", err)
		return
	}
	if claimed == 0 {
		return
	}
	scopeLabel := "This runtime"
	if exceeded.Scope == RuntimeBudgetScopeUser {
		scopeLabel = "Your agents on this runtime"
	}
	var body string
	if warnOnly {
		body = fmt.Sprintf("%s reached the %s cost limit of $%.2f (used $%.2f). Budget enforcement is in warn-only mode, so new runs are still allowed until %s UTC.",
			scopeLabel, exceeded.Period, obsmetrics.TicksToUSD(exceeded.LimitTicks), obsmetrics.TicksToUSD(exceeded.UsedTicks),
			exceeded.ResetAt.UTC().Format("Jan 2, 15:04"))
	} else {
		body = fmt.Sprintf("%s reached the %s cost limit of $%.2f (used $%.2f). New runs are refused until %s UTC.",
			scopeLabel, exceeded.Period, obsmetrics.TicksToUSD(exceeded.LimitTicks), obsmetrics.TicksToUSD(exceeded.UsedTicks),
			exceeded.ResetAt.UTC().Format("Jan 2, 15:04"))
	}
	for _, recipient := range recipients {
		item, err := q.CreateInboxItem(ctx, db.CreateInboxItemParams{
			ID: dbid.NewV7(), WorkspaceID: row.WorkspaceID,
			RecipientType: "member", RecipientID: recipient,
			Type: "runtime_budget_exceeded", Severity: "attention", IssueID: pgtype.UUID{},
			Title: "Runtime cost budget reached", Body: pgtype.Text{String: body, Valid: true},
			ActorType: pgtype.Text{String: "system", Valid: true}, ActorID: pgtype.UUID{},
			Details: details,
		})
		if err != nil {
			slog.Warn("runtime budget notice: create inbox item failed", "error", err)
			continue
		}
		if s.Bus != nil {
			s.Bus.Publish(events.Event{
				Type: protocol.EventInboxNew, WorkspaceID: util.UUIDToString(item.WorkspaceID), ActorType: "system",
				Payload: map[string]any{"item": map[string]any{
					"id": util.UUIDToString(item.ID), "workspace_id": util.UUIDToString(item.WorkspaceID),
					"recipient_type": item.RecipientType, "recipient_id": util.UUIDToString(item.RecipientID),
					"type": item.Type, "severity": item.Severity, "issue_id": nil,
					"issue_status": nil, "issue_priority": nil,
					"title": item.Title, "body": util.TextToPtr(item.Body), "read": item.Read,
					"archived": item.Archived, "created_at": util.TimestampToString(item.CreatedAt),
					"actor_type": util.TextToPtr(item.ActorType), "actor_id": nil,
					"details": json.RawMessage(item.Details),
				}},
			})
		}
	}
}

// RuntimeBudgetPeriodStatus is one configured period of one scope.
type RuntimeBudgetPeriodStatus struct {
	LimitTicks  int64
	UsedTicks   int64
	PeriodStart time.Time
	ResetAt     time.Time
	Reached     bool
}

// RuntimeBudgetScopeStatus is the runtime total (UserID invalid) or one user.
type RuntimeBudgetScopeStatus struct {
	UserID  pgtype.UUID
	Periods map[obsmetrics.Period]*RuntimeBudgetPeriodStatus
}

// RuntimeBudgetStatus is the read model behind GET /api/runtimes/{id}/budget.
type RuntimeBudgetStatus struct {
	Runtime *RuntimeBudgetScopeStatus
	Users   []RuntimeBudgetScopeStatus
}

// scopeStatus reports every period of one budget row from the already-loaded
// spend. A period with no configured limit maps to a nil entry, which the API
// renders as null.
func scopeStatus(row db.RuntimeCostBudget, spend runtimeSpend, now time.Time) RuntimeBudgetScopeStatus {
	out := RuntimeBudgetScopeStatus{UserID: row.UserID, Periods: map[obsmetrics.Period]*RuntimeBudgetPeriodStatus{}}
	for _, p := range obsmetrics.AllPeriods {
		limit, ok := budgetLimit(row, p)
		if !ok {
			out.Periods[p] = nil
			continue
		}
		used := spend.ticks(p, row.UserID)
		out.Periods[p] = &RuntimeBudgetPeriodStatus{
			LimitTicks: limit, UsedTicks: used, PeriodStart: obsmetrics.PeriodStart(now, p),
			ResetAt: obsmetrics.NextPeriodStart(now, p), Reached: used >= limit,
		}
	}
	return out
}

// RuntimeCostBudgetStatus loads every budget row of a runtime with its
// current-period spend. Spend is computed on demand, never stored: one grouped
// query covers every scope and period, so the cost does not grow with the
// number of per-user rows.
func (s *TaskService) RuntimeCostBudgetStatus(ctx context.Context, runtimeID pgtype.UUID, now time.Time) (RuntimeBudgetStatus, error) {
	rows, err := s.Queries.ListRuntimeCostBudgets(ctx, runtimeID)
	if err != nil {
		return RuntimeBudgetStatus{}, fmt.Errorf("list runtime cost budgets: %w", err)
	}
	status := RuntimeBudgetStatus{Users: []RuntimeBudgetScopeStatus{}}
	if len(rows) == 0 {
		return status, nil
	}
	spend, err := loadRuntimeSpend(ctx, s.Queries, runtimeID, now, budgetPeriods(rows))
	if err != nil {
		return RuntimeBudgetStatus{}, err
	}
	for _, row := range rows {
		sc := scopeStatus(row, spend, now)
		if row.UserID.Valid {
			status.Users = append(status.Users, sc)
		} else {
			total := sc
			status.Runtime = &total
		}
	}
	return status, nil
}
