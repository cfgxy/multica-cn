package taskfailure

import "testing"

// TestClaudePipelineCrash pins the two-signal predicate behind the daemon's
// Claude pipe-crash self-heal (RUYI-659). Both signals are required, and the
// direction of every ambiguity is toward NOT matching: a miss leaves today's
// behaviour (the task fails and a human retries), while a false positive
// discards a healthy session pointer and re-runs the task for nothing.
func TestClaudePipelineCrash(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		errText string
		want    bool
	}{
		// Positive: crash phrase AND the "claude stderr:" wrapper
		// withAgentStderr adds, so the text provably came out of the claude
		// CLI's own pipe.
		{
			name:    "V8 heap OOM with claude stderr wrapper",
			errText: "claude exited with error: exit status 1; claude stderr: FATAL ERROR: Reached heap limit Allocation failed - JavaScript heap out of memory",
			want:    true,
		},
		{
			name:    "stack overflow with claude stderr wrapper",
			errText: "claude exited with error: exit status 134; claude stderr: Maximum call stack size exceeded\n    at REPL32:1:1",
			want:    true,
		},
		{
			name:    "uncaught exception with claude stderr wrapper",
			errText: "claude exited with error: exit status 1; claude stderr: node:internal/process/promises:27\n triggerUncaughtException()\n ^\nTypeError: Cannot read properties of undefined (reading 'filter')",
			want:    true,
		},
		{
			name:    "EPIPE with claude stderr wrapper",
			errText: "claude exited with error: exit status 1; claude stderr: Error: write EPIPE at Socket.<anonymous>",
			want:    true,
		},
		{
			name:    "segfault with claude stderr wrapper",
			errText: "claude exited with error: exit status 139; claude stderr: segmentation fault (core dumped)",
			want:    true,
		},
		{
			name:    "runtime panic with claude exited wrapper",
			errText: "claude exited with error: exit status 2; claude stderr: panic: runtime error: invalid memory address or nil pointer dereference",
			want:    true,
		},

		// Negative: a rejected or otherwise healthy-session resume failure
		// must never match — dropping the pointer is the damage a false
		// positive does, and these failures have their own dedicated paths.
		{
			name:    "resume not found is not a crash",
			errText: "claude exited with error: exit status 1; claude stderr: No conversation found with session ID: sess-dead",
			want:    false,
		},
		{
			name:    "auth failure is not a crash",
			errText: "claude exited with error: exit status 1; claude stderr: Invalid API key provided · Please run /login",
			want:    false,
		},
		{
			name:    "rate limit is not a crash",
			errText: "claude returned an error result without details; claude stderr: API Error: 429 rate_limit_error",
			want:    false,
		},
		{
			name:    "provider 500 is not a crash",
			errText: "claude returned an error result without details; claude stderr: API Error: 500 internal server error",
			want:    false,
		},
		{
			name:    "empty error text never matches",
			errText: "",
			want:    false,
		},

		// Negative: one signal alone must never match.
		{
			// Crash prose without the claude-pipe wrapper: the daemon cannot
			// prove this text came from the claude CLI's pipe, and the same
			// words can appear in a provider's error narrative.
			name:    "crash phrase without claude pipe wrapper",
			errText: "FATAL ERROR: Reached heap limit Allocation failed - JavaScript heap out of memory",
			want:    false,
		},
		{
			// The wrapper without a crash phrase: a non-zero claude exit with
			// ordinary stderr output is every controlled failure the CLI has.
			name:    "claude wrapper without crash phrase",
			errText: "claude exited with error: exit status 1; claude stderr: some ordinary warning text",
			want:    false,
		},
		{
			// Another CLI's crash must not match a Claude-named predicate:
			// its wrapper names its own provider.
			name:    "another backend's crash is not a claude crash",
			errText: "codebuddy exited with error: exit status 1; codebuddy stderr: FATAL ERROR: Reached heap limit Allocation failed - JavaScript heap out of memory",
			want:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClaudePipelineCrash(tc.errText); got != tc.want {
				t.Fatalf("ClaudePipelineCrash(%q) = %v, want %v", tc.errText, got, tc.want)
			}
		})
	}
}
