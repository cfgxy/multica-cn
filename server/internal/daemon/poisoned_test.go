package daemon

import "testing"

func TestClassifyPoisonedOutput(t *testing.T) {
	cases := []struct {
		name       string
		output     string
		wantOK     bool
		wantReason string
	}{
		{
			name:       "iteration limit canonical",
			output:     "I reached the iteration limit and couldn't generate a summary.",
			wantOK:     true,
			wantReason: FailureReasonIterationLimit,
		},
		{
			name:       "iteration limit case insensitive",
			output:     "I REACHED THE ITERATION LIMIT and stopped",
			wantOK:     true,
			wantReason: FailureReasonIterationLimit,
		},
		{
			name:       "fallback meta message",
			output:     "Put your final update inside the content string. Keep it concise.",
			wantOK:     true,
			wantReason: FailureReasonAgentFallbackMsg,
		},
		{
			name:   "real conclusion is not poisoned",
			output: "Fixed the bug in auth.go and pushed PR #42.",
			wantOK: false,
		},
		{
			name:   "empty output",
			output: "",
			wantOK: false,
		},
		{
			name:   "mentions iteration but not the marker",
			output: "Each iteration of the loop processes one record.",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, ok := classifyPoisonedOutput(tc.output)
			if ok != tc.wantOK {
				t.Fatalf("classifyPoisonedOutput(%q) ok=%v, want %v", tc.output, ok, tc.wantOK)
			}
			if ok && reason != tc.wantReason {
				t.Fatalf("classifyPoisonedOutput(%q) reason=%q, want %q", tc.output, reason, tc.wantReason)
			}
		})
	}
}
