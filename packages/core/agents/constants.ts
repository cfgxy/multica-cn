// User-facing limits enforced symmetrically on the front-end (UI counter +
// disabled save) and the back-end (handler validation + DB CHECK constraint).
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
export const AGENT_SESSION_MAX_CONTEXT_TOKENS_MIN = 10_000;
export const AGENT_SESSION_MAX_CONTEXT_TOKENS_MAX = 10_000_000;
export const AGENT_SESSION_MAX_CONTEXT_TOKENS_DEFAULT = 400_000;

export const AGENT_SESSION_COMPACT_PCT_MIN = 10;
export const AGENT_SESSION_COMPACT_PCT_MAX = 100;
export const AGENT_SESSION_COMPACT_PCT_DEFAULT = 80;
