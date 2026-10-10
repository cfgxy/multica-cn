package taskfailure

import "regexp"

// ClaudePipelineCrash reports whether errText indicates the Claude CLI
// process itself crashed mid-pipe — a V8 abort, an uncaught exception, a
// segfault — as opposed to failing in one of the controlled ways the CLI
// reports structured prose for (a rejected resume, an auth failure, a rate
// limit, a provider error). On a RESUMED run that produced no session at
// all, this is the daemon's evidence that the pipe died before the CLI could
// even refuse: the transcript may be perfectly healthy and the crash
// transient, so a fresh session can pick the turn back up (RUYI-659).
//
// Two signals are required, and that is what keeps the predicate narrow:
//
//  1. A crash phrase — process-level death the CLI does not narrate as a
//     controlled failure. V8 fatal errors and heap exhaustion, stack
//     overflows, uncaught exceptions, segfaults, broken pipes, runtime
//     panics. An ordinary non-zero exit says nothing on its own: every
//     controlled failure (including resume rejections and auth errors)
//     exits non-zero too, so exit codes alone would flag exactly the
//     failures a fresh session cannot cure.
//
//  2. The Claude pipe wrapper — the "claude exited with error:" /
//     "claude stderr:" framing the stream-json backend adds around a
//     process-level failure, which proves the crash text came out of the
//     claude CLI's own pipe rather than a provider error narrative that
//     happens to contain similar words. It also scopes the predicate to
//     claude: another backend's crash carries its own provider's wrapper
//     and does not match a Claude-named predicate.
//
// Erring toward NOT matching is the safe direction, same standing rule as
// UnresumableHistory above: a miss leaves today's behaviour (the task fails
// and a human retries), while a false positive discards a session pointer
// that a platform retry could have resumed. A bare non-zero exit with an
// empty or ordinary stderr is therefore deliberately NOT matched.
func ClaudePipelineCrash(errText string) bool {
	if errText == "" {
		return false
	}
	return pipeCrashSignalRe.MatchString(errText) && claudePipeWrapperRe.MatchString(errText)
}

// pipeCrashSignalRe matches the crash phrases observed from Node/V8-based
// CLIs dying mid-pipe. Each entry is positive evidence of process death, not
// of any particular provider condition.
var pipeCrashSignalRe = regexp.MustCompile(`(?i)fatal error|javascript heap out of memory|maximum call stack size exceeded|segmentation fault|core dumped|broken pipe|\bepipe\b|err_internal_assertion|triggeruncaughtexception|uncaught exception|\bpanic:`)

// claudePipeWrapperRe matches the framing pkg/agent adds around a claude
// process-level failure: finalizeStreamResult's "<provider> exited with
// error:" for a non-zero exit, or withAgentStderr's "<provider> stderr:" for
// the captured stderr tail. Keep in sync with those two call sites.
var claudePipeWrapperRe = regexp.MustCompile(`(?i)claude (exited with error|stderr:)`)
