package metrics

import (
	"math"
	"time"
)

// Period is one budget window of a runtime cost budget (RUYI-665). Boundaries
// are always computed in UTC, whatever the caller's location: a day is the UTC
// calendar day, a week starts Monday 00:00 UTC and a month starts on the 1st
// at 00:00 UTC. User and runtime timezone preferences do not affect budgets —
// a cap that resets "at midnight" must mean the same instant for every
// observer, and the UI labels the reset time as UTC for exactly that reason.
type Period string

const (
	PeriodDaily   Period = "daily"
	PeriodWeekly  Period = "weekly"
	PeriodMonthly Period = "monthly"
)

// AllPeriods lists the periods in display order.
var AllPeriods = []Period{PeriodDaily, PeriodWeekly, PeriodMonthly}

// ParsePeriod accepts the wire spelling of a period.
func ParsePeriod(s string) (Period, bool) {
	switch Period(s) {
	case PeriodDaily, PeriodWeekly, PeriodMonthly:
		return Period(s), true
	default:
		return "", false
	}
}

// PeriodStart returns the UTC start of the period containing now.
func PeriodStart(now time.Time, p Period) time.Time {
	u := now.UTC()
	y, m, d := u.Date()
	switch p {
	case PeriodWeekly:
		// time.Weekday counts Sunday as 0; shift so Monday is day 0.
		daysSinceMonday := (int(u.Weekday()) + 6) % 7
		return time.Date(y, m, d-daysSinceMonday, 0, 0, 0, 0, time.UTC)
	case PeriodMonthly:
		return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	default:
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
}

// NextPeriodStart returns the UTC instant the current period resets.
func NextPeriodStart(now time.Time, p Period) time.Time {
	start := PeriodStart(now, p)
	switch p {
	case PeriodWeekly:
		return start.AddDate(0, 0, 7)
	case PeriodMonthly:
		return start.AddDate(0, 1, 0)
	default:
		return start.AddDate(0, 0, 1)
	}
}

// USDToTicks converts a dollar amount to 1e-10 USD ticks — the scale of
// task_usage.cost_usd_ticks (CostUSDTicksPerUSD) — rounding to the nearest
// tick so two-decimal inputs are exact.
func USDToTicks(usd float64) int64 {
	return int64(math.Round(usd * CostUSDTicksPerUSD))
}

// TicksToUSD is the inverse of USDToTicks.
func TicksToUSD(ticks int64) float64 {
	return float64(ticks) / CostUSDTicksPerUSD
}

// EstimateCostTicks is the single server-side cost formula for runtime cost
// budgets: the provider-reported cost (authoritative where present) plus the
// rate-table estimate for the tokens the provider did not price. A model
// without a rate row contributes only its provider cost; its uncosted tokens
// count as zero, which is why budget spend is a lower bound — the budget UI
// says so instead of hiding it.
//
// Resolution goes through the same PriceForModel call the usage pages use, so
// a budget and the Cost-by-owner tab can never pick different rate rows for
// one usage row. Browser-local custom prices never reach the server and never
// affect enforcement.
func EstimateCostTicks(provider, model string, costTicks, uncostedInput, uncostedOutput, uncostedCacheRead, uncostedCacheWrite int64) int64 {
	price, ok := PriceForModel(model, provider)
	if !ok {
		return costTicks
	}
	usd := tokenCostUSD(uncostedInput, price.InputPerM) +
		tokenCostUSD(uncostedOutput, price.OutputPerM) +
		tokenCostUSD(uncostedCacheRead, price.CacheReadPerM) +
		tokenCostUSD(uncostedCacheWrite, price.CacheWritePerM)
	return costTicks + USDToTicks(usd)
}
