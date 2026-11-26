package service

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// 2026-10-11 is a Sunday; the weekly window containing it starts Monday
// 2026-10-05 and the monthly window starts 2026-10-01, all 00:00 UTC.
var budgetNow = time.Date(2026, 10, 11, 15, 4, 5, 0, time.UTC)

func budgetRow(userID pgtype.UUID, daily, weekly, monthly int64) db.RuntimeCostBudget {
	row := db.RuntimeCostBudget{UserID: userID}
	set := func(dst *pgtype.Int8, v int64) {
		if v > 0 {
			*dst = pgtype.Int8{Int64: v, Valid: true}
		}
	}
	set(&row.DailyLimitUsdTicks, daily)
	set(&row.WeeklyLimitUsdTicks, weekly)
	set(&row.MonthlyLimitUsdTicks, monthly)
	return row
}

func TestApplicableBudgetRows(t *testing.T) {
	owner := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	other := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	agent := db.Agent{OwnerID: owner}

	rows := []db.RuntimeCostBudget{
		budgetRow(pgtype.UUID{}, 1, 0, 0), // runtime total
		budgetRow(owner, 1, 0, 0),         // the agent owner's own cap
		budgetRow(other, 1, 0, 0),         // someone else's cap
	}

	got := applicableBudgetRows(rows, agent)
	if len(got) != 2 {
		t.Fatalf("applicable = %d rows, want 2 (runtime total + agent owner)", len(got))
	}
	if got[0].scope != RuntimeBudgetScopeRuntime {
		t.Errorf("row 0 scope = %q, want runtime", got[0].scope)
	}
	if got[1].scope != RuntimeBudgetScopeUser || util.UUIDToString(got[1].row.UserID) != util.UUIDToString(owner) {
		t.Errorf("row 1 = scope %q user %v, want user scope for the agent owner", got[1].scope, got[1].row.UserID)
	}

	// An agent nobody owns cannot be held to any per-user cap: the runtime row
	// alone applies, so a runtime with only other users' caps costs no spend query.
	ownerless := db.Agent{}
	got = applicableBudgetRows(rows, ownerless)
	if len(got) != 1 || got[0].scope != RuntimeBudgetScopeRuntime {
		t.Fatalf("ownerless agent: applicable = %+v, want only the runtime row", got)
	}
}

func TestBudgetPeriods(t *testing.T) {
	none := []db.RuntimeCostBudget{budgetRow(pgtype.UUID{}, 0, 0, 0)}
	if got := budgetPeriods(none); len(got) != 0 {
		t.Errorf("no limits: periods = %v, want empty", got)
	}

	dailyOnly := []db.RuntimeCostBudget{budgetRow(pgtype.UUID{}, 1, 0, 0)}
	got := budgetPeriods(dailyOnly)
	if len(got) != 1 || got[0] != obsmetrics.PeriodDaily {
		t.Errorf("daily only: periods = %v, want [daily]", got)
	}

	// Union across rows, kept in AllPeriods order even though the monthly row
	// comes first in the input.
	mixed := []db.RuntimeCostBudget{
		budgetRow(pgtype.UUID{}, 0, 0, 5),
		budgetRow(pgtype.UUID{Bytes: [16]byte{9}, Valid: true}, 2, 0, 0),
	}
	got = budgetPeriods(mixed)
	if len(got) != 2 || got[0] != obsmetrics.PeriodDaily || got[1] != obsmetrics.PeriodMonthly {
		t.Errorf("mixed rows: periods = %v, want [daily monthly]", got)
	}
}

func TestBudgetScanFloor(t *testing.T) {
	daily := budgetScanFloor(budgetNow, []obsmetrics.Period{obsmetrics.PeriodDaily})
	if want := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC); !daily.Equal(want) {
		t.Errorf("daily floor = %v, want %v (midnight, not month start)", daily, want)
	}
	all := budgetScanFloor(budgetNow, []obsmetrics.Period{obsmetrics.PeriodDaily, obsmetrics.PeriodWeekly, obsmetrics.PeriodMonthly})
	if want := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC); !all.Equal(want) {
		t.Errorf("all periods floor = %v, want %v (earliest window start)", all, want)
	}
}

func makeSpend(totalDaily, ownerDaily int64) runtimeSpend {
	owner := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	spend := runtimeSpend{
		total:   map[obsmetrics.Period]int64{},
		byOwner: map[obsmetrics.Period]map[string]int64{},
	}
	for _, p := range obsmetrics.AllPeriods {
		spend.total[p] = totalDaily
		spend.byOwner[p] = map[string]int64{}
	}
	spend.byOwner[obsmetrics.PeriodDaily][util.UUIDToString(owner)] = ownerDaily
	return spend
}

func TestEvaluateBudgetRow(t *testing.T) {
	ticks := obsmetrics.USDToTicks

	// Under the limit: no refusal.
	row := budgetRow(pgtype.UUID{}, ticks(10), 0, 0)
	if got := evaluateBudgetRow(row, RuntimeBudgetScopeRuntime, makeSpend(ticks(9.99), 0), budgetNow); got != nil {
		t.Errorf("9.99/10 daily: got %v, want nil", got)
	}

	// At exactly the limit: refused — the cap is a ceiling, the run that lands
	// on it is the one that would push spend past it.
	got := evaluateBudgetRow(row, RuntimeBudgetScopeRuntime, makeSpend(ticks(10), 0), budgetNow)
	if got == nil {
		t.Fatal("10/10 daily: want refusal, got nil")
	}
	if got.Period != obsmetrics.PeriodDaily || got.Scope != RuntimeBudgetScopeRuntime {
		t.Errorf("refusal = %s/%s, want runtime/daily", got.Scope, got.Period)
	}
	if got.UsedTicks != ticks(10) || got.LimitTicks != ticks(10) {
		t.Errorf("refusal used/limit = %d/%d ticks, want %d/%d", got.UsedTicks, got.LimitTicks, ticks(10), ticks(10))
	}
	if want := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC); !got.ResetAt.Equal(want) {
		t.Errorf("ResetAt = %v, want %v", got.ResetAt, want)
	}

	// A monthly-only cap refuses on the monthly period even though the same
	// spend leaves every daily window far under anything: periods without a
	// limit never refuse, and the configured one reads its own window.
	row = budgetRow(pgtype.UUID{}, 0, 0, ticks(5))
	got = evaluateBudgetRow(row, RuntimeBudgetScopeRuntime, makeSpend(ticks(500), 0), budgetNow)
	if got == nil || got.Period != obsmetrics.PeriodMonthly {
		t.Fatalf("want monthly refusal, got %+v", got)
	}
	if want := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC); !got.ResetAt.Equal(want) {
		t.Errorf("monthly ResetAt = %v, want %v", got.ResetAt, want)
	}
}

func TestFirstReachedBudgetOrder(t *testing.T) {
	ticks := obsmetrics.USDToTicks
	owner := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	agent := db.Agent{OwnerID: owner}

	// Both rows refuse; the runtime-total row wins because it is evaluated
	// first (applicableBudgetRows lists it first), and the runtime refusal is
	// the one whose reset announcement applies to everyone.
	rows := []db.RuntimeCostBudget{
		budgetRow(pgtype.UUID{}, ticks(1), 0, 0),
		budgetRow(owner, 0, ticks(2), 0),
	}
	applicable := applicableBudgetRows(rows, agent)
	spend := makeSpend(ticks(3), 0)
	row, exceeded := firstReachedBudget(applicable, spend, budgetNow)
	if exceeded == nil {
		t.Fatal("want refusal, got nil")
	}
	if exceeded.Scope != RuntimeBudgetScopeRuntime {
		t.Errorf("scope = %q, want runtime (first applicable row wins)", exceeded.Scope)
	}
	if row.UserID.Valid {
		t.Errorf("refusing row should be the runtime row, got user %v", util.UUIDToString(row.UserID))
	}

	// Only the per-user row refuses: the runtime total has room, the agent
	// owner's own weekly cap is spent. Per-user spend is that user's own spend
	// only — other owners' usage never leaks into their scope.
	rows[0] = budgetRow(pgtype.UUID{}, ticks(50), 0, 0)
	rows[1] = budgetRow(owner, 0, ticks(2), 0)
	applicable = applicableBudgetRows(rows, agent)
	spend = makeSpend(ticks(30), ticks(2))
	spend.byOwner[obsmetrics.PeriodWeekly][util.UUIDToString(owner)] = ticks(2)
	_, exceeded = firstReachedBudget(applicable, spend, budgetNow)
	if exceeded == nil || exceeded.Scope != RuntimeBudgetScopeUser {
		t.Fatalf("want user-scope refusal, got %+v", exceeded)
	}
	spend.byOwner[obsmetrics.PeriodWeekly]["other-owner"] = ticks(999)
	if got := spend.ticks(obsmetrics.PeriodWeekly, owner); got != ticks(2) {
		t.Errorf("user weekly ticks = %d, want %d (own spend only)", got, ticks(2))
	}
	if got := spend.ticks(obsmetrics.PeriodWeekly, pgtype.UUID{}); got != ticks(30) {
		t.Errorf("runtime weekly ticks = %d, want %d (invalid owner reads the total)", got, ticks(30))
	}
}

func TestBudgetExceededErrorMessages(t *testing.T) {
	e := &RuntimeBudgetExceededError{
		Scope: RuntimeBudgetScopeRuntime, Period: obsmetrics.PeriodMonthly,
		UsedTicks: obsmetrics.USDToTicks(123.45), LimitTicks: obsmetrics.USDToTicks(100),
		ResetAt: budgetNow,
	}
	// PublicReason is persisted into agent_task_queue.error and republished to
	// every workspace subscriber: it must not carry either figure.
	if got := e.PublicReason(); strings.ContainsAny(got, "0123456789") {
		t.Errorf("PublicReason = %q, want no digits (amount-free)", got)
	}
	// Error() is for server logs only and keeps both figures.
	if got := e.Error(); !strings.Contains(got, "123.45") || !strings.Contains(got, "100.00") {
		t.Errorf("Error() = %q, want both amounts for the server log", got)
	}
}

func TestScopeStatusReached(t *testing.T) {
	ticks := obsmetrics.USDToTicks
	row := budgetRow(pgtype.UUID{}, ticks(10), 0, ticks(100))
	spend := makeSpend(ticks(10), 0)
	st := scopeStatus(row, spend, budgetNow)
	if st.Periods[obsmetrics.PeriodWeekly] != nil {
		t.Error("unconfigured weekly should map to nil")
	}
	if !st.Periods[obsmetrics.PeriodDaily].Reached {
		t.Error("daily at exactly the limit should report Reached")
	}
	if st.Periods[obsmetrics.PeriodMonthly].Reached {
		t.Error("monthly 0/100 should not report Reached")
	}
}
