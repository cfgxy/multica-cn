package service

// RUYI-859 stage B: the entry/recovery judgment functions are pure over
// a terminal-sample reading, so their boundary matrix is pinned here
// without a database. The DB-gated lifecycle (ensure/release, audit,
// event, broadcast) is in agent_backpressure_suppression_db_test.go.

import (
	"math"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

func testPolicy() *protocol.AgentBackpressureConfig {
	return &protocol.AgentBackpressureConfig{
		WindowMinutes:          60,
		ErrorRateThresholdPct:  30,
		MinSamples:             5,
		RecoveryWindowMinutes:  30,
		SuppressOnErrorRate:    true,
		CountModelAPIFailures:  true,
		CountExecutionFailures: true,
	}
}

// classRows builds a reading from raw terminal rows the way
// CountAgentTerminalTasksByReasonInWindow feeds AgentBackpressureReadingFromRows:
// a succeeded row (empty reason) plus one failed row per chosen class.
// Reasons are real wire values; which class each folds into is asserted
// through taskfailure.DisplayClass, not assumed here.
func classRows(t *testing.T, succeeded int, failedCounts map[taskfailure.Reason]int) *AgentBackpressureWindowReading {
	t.Helper()
	cfg := testPolicy()
	var raw []db.CountAgentTerminalTasksByReasonInWindowRow
	if succeeded > 0 {
		raw = append(raw, db.CountAgentTerminalTasksByReasonInWindowRow{Reason: "", TaskCount: int32(succeeded)})
	}
	for reason, count := range failedCounts {
		cls := taskfailure.DisplayClass(reason)
		switch cls {
		case taskfailure.ClassRateLimit, taskfailure.ClassTimeout, taskfailure.ClassRuntime:
		default:
			t.Fatalf("test helper picked an out-of-class reason %q -> %q", reason, cls)
		}
		raw = append(raw, db.CountAgentTerminalTasksByReasonInWindowRow{Reason: string(reason), TaskCount: int32(count)})
	}
	return AgentBackpressureReadingFromRows(cfg, cfg.WindowMinutes, raw)
}

// rateOf reads back the pointer rate, failing the test when absent.
func rateOf(t *testing.T, r *AgentBackpressureWindowReading) float64 {
	t.Helper()
	if r.ErrorRatePct == nil {
		t.Fatalf("reading has no rate: %+v", r)
	}
	return *r.ErrorRatePct
}

func TestEntryArmedBoundaries(t *testing.T) {
	cfg := testPolicy()

	// At the min_samples floor with a high rate: the denominator reaches the
	// floor, so the rate is evaluated and arms (4/5 = 80% >= 30%).
	atFloor := classRows(t, 1, map[taskfailure.Reason]int{
		taskfailure.ReasonAgentProviderCapacityOrRateLimit: 4,
	})
	if !EntryArmedFor(cfg, atFloor) {
		t.Fatalf("at min_samples with 80%% rate: want armed")
	}

	// Below the floor: a 100% rate is never evaluated.
	underFloor := classRows(t, 0, map[taskfailure.Reason]int{
		taskfailure.ReasonAgentProviderCapacityOrRateLimit: 4,
	}) // 4/4 = 100%, but 4 < min_samples 5
	if EntryArmedFor(cfg, underFloor) {
		t.Fatalf("below min_samples: must never arm")
	}

	// No samples at all: never armed.
	empty := AgentBackpressureReadingFromRows(cfg, 60, nil)
	if EntryArmedFor(cfg, empty) {
		t.Fatalf("no samples: must never arm")
	}

	// EXACTLY at the threshold arms (>= is the entry contract): 3/10 = 30.0%.
	atThreshold := classRows(t, 7, map[taskfailure.Reason]int{
		taskfailure.ReasonTimeout: 3,
	})
	if !EntryArmedFor(cfg, atThreshold) {
		t.Fatalf("rate exactly at threshold: must arm")
	}
	if math.Abs(rateOf(t, atThreshold)-30) > 1e-9 {
		t.Fatalf("rate = %v, want 30", rateOf(t, atThreshold))
	}
}

func TestRecoveryClearedStrictlyBelowHalfThreshold(t *testing.T) {
	cfg := testPolicy() // threshold 30 -> recovery line 15

	// Strictly under 15% clears.
	under := classRows(t, 10, map[taskfailure.Reason]int{
		taskfailure.ReasonAgentProviderCapacityOrRateLimit: 1,
	}) // 1/11 = 9.09% < 15
	if !RecoveryClearedFor(cfg, under) {
		t.Fatalf("9%% below the 15%% recovery line: must clear")
	}

	// EXACTLY at 0.5 x threshold does NOT clear: the contract is strictly
	// below, so 15.0% holds the pause.
	atLine := classRows(t, 17, map[taskfailure.Reason]int{
		taskfailure.ReasonAgentProviderCapacityOrRateLimit: 3,
	}) // 3/20 = 15.0% exactly = 0.5 x 30
	if RecoveryClearedFor(cfg, atLine) {
		t.Fatalf("rate exactly at the 0.5x recovery line: must NOT clear (strictly below)")
	}

	// Above the entry threshold itself never clears, even with samples.
	high := classRows(t, 5, map[taskfailure.Reason]int{
		taskfailure.ReasonRuntimeOffline: 5,
	}) // 5/10 = 50%
	if RecoveryClearedFor(cfg, high) {
		t.Fatalf("50%% rate: must NOT clear")
	}

	// No samples: no recovery, ever.
	empty := AgentBackpressureReadingFromRows(cfg, 30, nil)
	if RecoveryClearedFor(cfg, empty) {
		t.Fatalf("no samples: must NOT clear")
	}

	// A below-floor sample count with a clean rate still holds.
	underFloor := classRows(t, 4, nil) // 0/4 = 0% but 4 < min_samples 5
	if RecoveryClearedFor(cfg, underFloor) {
		t.Fatalf("below min_samples: must NOT clear even at 0%%")
	}
}

func TestReadingClassSwitchGatesNumerator(t *testing.T) {
	// Both switches on: model/API and execution classes all count.
	reading := classRows(t, 0, map[taskfailure.Reason]int{
		taskfailure.ReasonAgentProviderCapacityOrRateLimit: 2,
		taskfailure.ReasonRuntimeOffline:                   3,
	}) // numerator 5 / total 5 = 100%
	if reading.SelectedFailures != 5 {
		t.Fatalf("both switches on: selected_failures = %d, want 5", reading.SelectedFailures)
	}

	// Model/API switch off: the rate_limit failures drop out of the
	// numerator (they still count in the denominator and the class split).
	offModel := testPolicy()
	offModel.CountModelAPIFailures = false
	reading = AgentBackpressureReadingFromRows(offModel, 60, []db.CountAgentTerminalTasksByReasonInWindowRow{
		{Reason: string(taskfailure.ReasonAgentProviderCapacityOrRateLimit), TaskCount: 2},
		{Reason: string(taskfailure.ReasonRuntimeOffline), TaskCount: 3},
	})
	if reading.SelectedFailures != 3 {
		t.Fatalf("model switch off: selected_failures = %d, want 3 (execution only)", reading.SelectedFailures)
	}
	if reading.TotalTerminal != 5 {
		t.Fatalf("denominator must stay the full terminal set: %d, want 5", reading.TotalTerminal)
	}

	// Succeeded rows are denominator-only.
	succ := classRows(t, 4, map[taskfailure.Reason]int{
		taskfailure.ReasonAgentProviderCapacityOrRateLimit: 1,
	}) // 1/5 = 20%
	if succ.SelectedFailures != 1 || succ.TotalTerminal != 5 {
		t.Fatalf("succeeded handling: selected=%d total=%d, want 1/5", succ.SelectedFailures, succ.TotalTerminal)
	}
	if math.Abs(rateOf(t, succ)-20) > 1e-9 {
		t.Fatalf("rate = %v, want 20", rateOf(t, succ))
	}
}

func TestBackpressureConfigFromPolicyRow(t *testing.T) {
	cfg := BackpressureConfigFromPolicyRow(db.AgentBackpressurePolicy{
		WindowMinutes:          90,
		ErrorRateThresholdPct:  45,
		MinSamples:             7,
		RecoveryWindowMinutes:  15,
		SuppressOnErrorRate:    true,
		CountModelApiFailures:  true,
		CountExecutionFailures: false,
	})
	if cfg.WindowMinutes != 90 || cfg.ErrorRateThresholdPct != 45 || cfg.MinSamples != 7 {
		t.Fatalf("mapping window/threshold/min: %+v", cfg)
	}
	if cfg.RecoveryWindowMinutes != 15 || cfg.SuppressOnErrorRate != true || cfg.CountModelAPIFailures != true || cfg.CountExecutionFailures != false {
		t.Fatalf("mapping recovery/switches: %+v", cfg)
	}
}
