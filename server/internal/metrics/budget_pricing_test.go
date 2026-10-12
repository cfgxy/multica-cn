package metrics

import (
	"testing"
	"time"
)

// Budget window boundaries are always UTC, whatever the caller's location:
// a day is the UTC calendar day, a week starts Monday 00:00 UTC and a month
// starts on the 1st at 00:00 UTC (RUYI-665, decision adopted from the
// furtherref design). Every boundary case below pins one edge of that rule.

func TestPeriodStartReturnsUTCCalendarDay(t *testing.T) {
	// 23:30 in UTC+8 is still the same UTC day; 00:30 UTC+8 is the previous one.
	got := PeriodStart(time.Date(2026, 10, 11, 23, 30, 0, 0, time.FixedZone("UTC+8", 8*3600)), PeriodDaily)
	want := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("PeriodStart daily = %v, want %v", got, want)
	}
	got = PeriodStart(time.Date(2026, 10, 11, 6, 30, 0, 0, time.FixedZone("UTC+8", 8*3600)), PeriodDaily)
	want = time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("PeriodStart daily across UTC date line = %v, want %v", got, want)
	}
}

func TestPeriodStartWeekStartsMondayUTC(t *testing.T) {
	tests := []struct {
		day  time.Time
		want time.Time
	}{
		// 2026-10-05 is a Monday; 2026-10-11 its Sunday.
		{time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)},
		{time.Date(2026, 10, 11, 23, 59, 0, 0, time.UTC), time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)},
		// 2026-10-12 (Tuesday) starts the next week.
		{time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)},
		// Sunday 2026-10-04 belongs to the week that started Monday 2026-09-28.
		{time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		if got := PeriodStart(tt.day, PeriodWeekly); !got.Equal(tt.want) {
			t.Fatalf("PeriodStart weekly(%s) = %v, want %v", tt.day, got, tt.want)
		}
	}
}

func TestPeriodStartMonthAndYearBoundaries(t *testing.T) {
	tests := []struct {
		day  time.Time
		want time.Time
	}{
		{time.Date(2026, 10, 11, 15, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
		{time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
		// March 1st after a leap-day February.
		{time.Date(2028, 3, 1, 8, 0, 0, 0, time.UTC), time.Date(2028, 3, 1, 0, 0, 0, 0, time.UTC)},
		// A year boundary folds into January.
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		if got := PeriodStart(tt.day, PeriodMonthly); !got.Equal(tt.want) {
			t.Fatalf("PeriodStart monthly(%s) = %v, want %v", tt.day, got, tt.want)
		}
	}
}

func TestNextPeriodStartRollsEveryBoundary(t *testing.T) {
	tests := []struct {
		period Period
		now    time.Time
		want   time.Time
	}{
		{PeriodDaily, time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC), time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)},
		{PeriodWeekly, time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC), time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)},
		// A monthly reset crossing a year boundary must land on Jan 1.
		{PeriodMonthly, time.Date(2026, 12, 15, 9, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)},
		// January's next start is Feb 1 even on the 31st; February's is Mar 1.
		{PeriodMonthly, time.Date(2026, 1, 31, 9, 0, 0, 0, time.UTC), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
		{PeriodMonthly, time.Date(2026, 2, 28, 9, 0, 0, 0, time.UTC), time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		if got := NextPeriodStart(tt.now, tt.period); !got.Equal(tt.want) {
			t.Fatalf("NextPeriodStart(%s, %s) = %v, want %v", tt.period, tt.now, got, tt.want)
		}
	}
}

func TestParsePeriodAcceptsWireSpellingOnly(t *testing.T) {
	for _, s := range []string{"daily", "weekly", "monthly"} {
		if _, ok := ParsePeriod(s); !ok {
			t.Fatalf("ParsePeriod(%q) rejected", s)
		}
	}
	for _, s := range []string{"", "Daily", "yearly", "dail"} {
		if _, ok := ParsePeriod(s); ok {
			t.Fatalf("ParsePeriod(%q) accepted", s)
		}
	}
}

func TestUSDTicksRoundTrip(t *testing.T) {
	if USDToTicks(20) != 200_000_000_000 {
		t.Fatalf("USDToTicks(20) = %d", USDToTicks(20))
	}
	if TicksToUSD(200_000_000_000) != 20 {
		t.Fatalf("TicksToUSD = %v", TicksToUSD(200_000_000_000))
	}
	// Two-decimal amounts must survive float rounding, or a budget the user
	// typed as $0.07 would read back as $0.07-less-a-hair and drift on display.
	if TicksToUSD(USDToTicks(0.07)) != 0.07 {
		t.Fatalf("0.07 round trip = %v", TicksToUSD(USDToTicks(0.07)))
	}
}

func TestEstimateCostTicksPricesUncostedTokensOfKnownModel(t *testing.T) {
	// gpt-5.6-sol: input 5.00, output 30.00, cache read 0.50, cache write 6.25 per 1M.
	got := EstimateCostTicks("openai", "gpt-5.6-sol", 0, 1_000_000, 100_000, 500_000, 200_000)
	// 5.00 + 3.00 + 0.25 + 1.25 = 9.50 USD.
	if want := USDToTicks(9.50); got != want {
		t.Fatalf("ticks = %d, want %d", got, want)
	}
}

func TestEstimateCostTicksKeepsProviderCostAndIgnoresUnpricedTokens(t *testing.T) {
	// A model the table does not know contributes only its provider-reported
	// cost; its uncosted tokens count as zero, which is why budget spend is a
	// lower bound.
	got := EstimateCostTicks("", "some-unknown-model", 1_500, 1_000_000, 1_000_000, 0, 0)
	if got != 1_500 {
		t.Fatalf("ticks = %d, want the provider cost 1500 only", got)
	}
}

func TestEstimateCostTicksAddsProviderCostToEstimate(t *testing.T) {
	got := EstimateCostTicks("openai", "gpt-5.6-sol", USDToTicks(1), 1_000_000, 0, 0, 0)
	if want := USDToTicks(6); got != want {
		t.Fatalf("ticks = %d, want %d", got, want)
	}
}

func TestEstimateCostTicksProviderQualifiedKeyWins(t *testing.T) {
	// The provider-qualified lookup must be the same one PriceForModel uses,
	// so a budget and the usage page never pick different rows for one id.
	price, ok := PriceForModel("gpt-5.6-sol", "openai")
	if !ok {
		t.Fatal("PriceForModel(gpt-5.6-sol, openai) not found")
	}
	got := EstimateCostTicks("openai", "gpt-5.6-sol", 0, 1_000_000, 0, 0, 0)
	if want := USDToTicks(price.InputPerM); got != want {
		t.Fatalf("ticks = %d, want %d (from the same PriceForModel row)", got, want)
	}
}
