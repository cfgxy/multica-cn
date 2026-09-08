// User-facing limits enforced on the front-end (UI counter + disabled save) and
// on the back-end (handler validation; some also carry a DB CHECK constraint).
// Kept in core so both apps and the test suite read from one source.
export const AGENT_DESCRIPTION_MAX_LENGTH = 255;

export const AGENT_CONVERSATION_STARTERS_MAX = 3;
export const AGENT_CONVERSATION_STARTER_LABEL_MAX_LENGTH = 80;
export const AGENT_CONVERSATION_STARTER_MAX_LENGTH = 4000;

// Valid range for the per-agent scheduler cap. Kept here so creation,
// duplication, and settings editing cannot silently drift.
export const AGENT_MAX_CONCURRENT_TASKS_MIN = 1;
export const AGENT_MAX_CONCURRENT_TASKS_MAX = 50;

// Session context gate (RUYI-107). When a resumable session has grown past
// `session_compact_pct` of `session_max_context_tokens`, the platform starts a
// fresh session and re-injects a bounded prior-context brief instead.
//
// Zero is a legal ceiling with its own meaning — it turns the gate off — so it
// sits OUTSIDE the [MIN, MAX] range rather than below it, and a UI that clamps
// input into the range would make the off switch unreachable.
export const AGENT_SESSION_MAX_CONTEXT_TOKENS_DISABLED = 0;
export const AGENT_SESSION_MAX_CONTEXT_TOKENS_MIN = 100_000;
export const AGENT_SESSION_MAX_CONTEXT_TOKENS_MAX = 2_000_000;
export const AGENT_SESSION_MAX_CONTEXT_TOKENS_DEFAULT = 400_000;

export const AGENT_SESSION_COMPACT_PCT_MIN = 10;
export const AGENT_SESSION_COMPACT_PCT_MAX = 100;
export const AGENT_SESSION_COMPACT_PCT_DEFAULT = 80;

// The soft switch never fires below this, whatever the percentage works out to.
// At the legal minimum ceiling and the legal minimum percentage the arithmetic
// gives 10,000 tokens — under a single turn's prompt — which would put every
// run on a fresh session. Mirrors agentconfig.MinEffectiveCompactThreshold; the
// UI shows the effective figure so the displayed trigger point is the real one.
export const AGENT_SESSION_COMPACT_MIN_EFFECTIVE_TOKENS = 50_000;

/**
 * The token count the switch actually happens at. Mirrors
 * agentconfig.EffectiveCompactThreshold — keep the two in step.
 */
export function agentSessionEffectiveCompactThreshold(
  maxContextTokens: number,
  compactPct: number,
): number {
  const pct =
    compactPct >= AGENT_SESSION_COMPACT_PCT_MIN &&
    compactPct <= AGENT_SESSION_COMPACT_PCT_MAX
      ? compactPct
      : AGENT_SESSION_COMPACT_PCT_DEFAULT;
  const threshold = Math.floor((maxContextTokens * pct) / 100);
  return Math.min(
    maxContextTokens,
    Math.max(threshold, AGENT_SESSION_COMPACT_MIN_EFFECTIVE_TOKENS),
  );
}
