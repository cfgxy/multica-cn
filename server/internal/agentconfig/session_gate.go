package agentconfig

import "fmt"

// Session context gate (RUYI-107).
//
// A resumed (agent, issue) session grows every turn. Left alone it eventually
// hits the provider's context window and the run dies mid-turn, which is both
// a lost turn and a poisoned session. The gate stops that one turn earlier:
// once the conversation is measurably close to the configured ceiling, the
// next run starts a FRESH session carrying a bounded brief instead of the
// whole transcript.
const (
	// DefaultSessionMaxContextTokens is decision D2 A: the ceiling a session
	// is measured against when the agent owner has not set one.
	DefaultSessionMaxContextTokens int64 = 400_000
	// DisabledSessionMaxContextTokens turns the gate off entirely, restoring
	// pre-RUYI-107 behavior (only the poisoned-session filters end a resume).
	DisabledSessionMaxContextTokens int64 = 0
	// MinSessionMaxContextTokens keeps a configured ceiling far enough above
	// a single prompt that the gate cannot fire on every turn and reduce the
	// agent to a permanently amnesiac one. Zero is still accepted separately
	// as the explicit "off" value.
	MinSessionMaxContextTokens int64 = 100_000
	MaxSessionMaxContextTokens int64 = 2_000_000

	// MinEffectiveCompactThreshold is the frozen spec's interlock: the soft
	// threshold is raised to this floor whenever pct×ceiling lands below it.
	// Without it, a legal 100_000 ceiling at the legal minimum 10% would
	// compact at 10_000 tokens — under one turn's worth of prompt — and the
	// agent would start every run from a brief. The hard ceiling is NOT
	// floored: it is the value the owner explicitly set as the limit.
	MinEffectiveCompactThreshold int64 = 50_000

	// DefaultSessionCompactPct is decision D2 A's "switch at 80%".
	DefaultSessionCompactPct int32 = 80
	// MinSessionCompactPct: below this the gate would compact while the
	// session is still mostly empty, throwing away context for nothing.
	MinSessionCompactPct int32 = 10
	// MaxSessionCompactPct is 100 — compact only on reaching the ceiling
	// itself. Anything higher would be unreachable and silently disable the
	// gate under a value that reads as "enabled".
	MaxSessionCompactPct int32 = 100
)

// SessionResumeDecision is the claim-time verdict for one candidate session.
type SessionResumeDecision string

const (
	// SessionResumeAllowed: the session is comfortably under the threshold.
	SessionResumeAllowed SessionResumeDecision = "resume"
	// SessionResumeGateDisabled: no ceiling configured, so nothing to judge.
	SessionResumeGateDisabled SessionResumeDecision = "gate_disabled"
	// SessionResumeSizeUnknown: no context reading exists for the candidate
	// session. Decision D4 A — resume rather than guess, and let the existing
	// overflow protections handle a session that really was too big.
	SessionResumeSizeUnknown SessionResumeDecision = "size_unknown"
	// SessionResumeCompactSoft: at or past the compaction percentage but
	// still under the ceiling — the planned, early switch.
	SessionResumeCompactSoft SessionResumeDecision = "compact_soft"
	// SessionResumeCompactHard: at or past the ceiling itself. Reported
	// separately from the soft case purely so operators can tell a normal
	// early switch from one where the early switch never happened (a session
	// whose readings jumped, or whose thresholds were raised mid-flight).
	SessionResumeCompactHard SessionResumeDecision = "compact_hard"
)

// ShouldStartFreshSession reports whether the decision requires abandoning the
// candidate session. Callers must not test for equality against the individual
// compact constants: the soft/hard split is observability, not behavior.
func (d SessionResumeDecision) ShouldStartFreshSession() bool {
	return d == SessionResumeCompactSoft || d == SessionResumeCompactHard
}

// DecideSessionResume judges one candidate session.
//
// contextTokens/known come from the context-size snapshot of the SAME task the
// candidate session was taken from; known is false when no provider reading
// exists. maxContextTokens/compactPct are the owning agent's settings.
//
// The function is total and never errors: a claim must always reach a verdict,
// and every unusable input resolves to a "keep resuming" answer so a bad
// setting degrades into today's behavior rather than into an agent that
// forgets everything on every turn.
func DecideSessionResume(contextTokens int64, known bool, maxContextTokens int64, compactPct int32) SessionResumeDecision {
	if maxContextTokens <= DisabledSessionMaxContextTokens {
		return SessionResumeGateDisabled
	}
	if !known || contextTokens <= 0 {
		return SessionResumeSizeUnknown
	}
	if contextTokens >= maxContextTokens {
		return SessionResumeCompactHard
	}
	if contextTokens >= EffectiveCompactThreshold(maxContextTokens, compactPct) {
		return SessionResumeCompactSoft
	}
	return SessionResumeAllowed
}

// EffectiveCompactThreshold is the token count the soft switch actually fires
// at: pct% of the ceiling, but never below MinEffectiveCompactThreshold.
//
// Exported because the setting UI and the tests both need to state the same
// number the gate uses; a second copy of this arithmetic is how the displayed
// trigger point and the real one drift apart.
func EffectiveCompactThreshold(maxContextTokens int64, compactPct int32) int64 {
	// An out-of-range percentage falls back to the default instead of being
	// clamped to its nearest bound: clamping a stored 1% to 10% would still
	// compact almost every turn, whereas the default is the behavior the
	// product actually specified.
	if !IsValidSessionCompactPct(compactPct) {
		compactPct = DefaultSessionCompactPct
	}
	// Multiply before dividing so the threshold is exact for ceilings that
	// are not multiples of 100.
	threshold := maxContextTokens * int64(compactPct) / 100
	if threshold < MinEffectiveCompactThreshold {
		threshold = MinEffectiveCompactThreshold
	}
	// The floor must never overtake the ceiling it protects: a ceiling below
	// the floor (only reachable from legacy rows written before this range
	// existed) would otherwise have no soft stage at all, and the hard check
	// above already covers it.
	if threshold > maxContextTokens {
		threshold = maxContextTokens
	}
	return threshold
}

func IsValidSessionMaxContextTokens(value int64) bool {
	if value == DisabledSessionMaxContextTokens {
		return true
	}
	return value >= MinSessionMaxContextTokens && value <= MaxSessionMaxContextTokens
}

func ValidateSessionMaxContextTokens(value int64) error {
	if !IsValidSessionMaxContextTokens(value) {
		return fmt.Errorf(
			"must be 0 (disabled) or between %d and %d",
			MinSessionMaxContextTokens,
			MaxSessionMaxContextTokens,
		)
	}
	return nil
}

func IsValidSessionCompactPct(value int32) bool {
	return value >= MinSessionCompactPct && value <= MaxSessionCompactPct
}

func ValidateSessionCompactPct(value int32) error {
	if !IsValidSessionCompactPct(value) {
		return fmt.Errorf(
			"must be between %d and %d",
			MinSessionCompactPct,
			MaxSessionCompactPct,
		)
	}
	return nil
}
