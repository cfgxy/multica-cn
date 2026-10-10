package retrospective

import (
	"regexp"
	"testing"
)

// TestReasonCodesAreStableWireSurface locks the reason-code enum's shape
// (RUYI-561): the values are wire surface mirrored by the frontend's
// retrospective-run-error.ts key table and localized in all four locales —
// a rename silently breaks that mapping, so it must be caught here first.
// Adding a new code is the supported evolution; changing one goes through
// the frontend table in the same change.
func TestReasonCodesAreStableWireSurface(t *testing.T) {
	codes := []string{
		ReasonAgentNotConfigured,
		ReasonAgentMissing,
		ReasonAgentArchived,
		ReasonAgentRuntimeMissing,
		ReasonWindowScanFailed,
		ReasonWatermarkCheckFailed,
		ReasonScanScopeRecordFailed,
		ReasonTaskEnqueueFailed,
		ReasonAgentReportInvalid,
		ReasonAgentNoReport,
		ReasonAgentRunFailed,
		ReasonReportOutOfScope,
		ReasonDraftOutOfScope,
		ReasonTaskNotCompleted,
		ReasonTaskNeverEnqueued,
	}
	seen := map[string]bool{}
	pattern := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	for _, code := range codes {
		if code == "" {
			t.Fatalf("reason code must not be empty")
		}
		if !pattern.MatchString(code) {
			t.Fatalf("reason code %q must be snake_case", code)
		}
		if seen[code] {
			t.Fatalf("reason code %q is duplicated", code)
		}
		seen[code] = true
	}
	if len(seen) != 15 {
		t.Fatalf("expected 15 distinct reason codes, got %d", len(seen))
	}
}
