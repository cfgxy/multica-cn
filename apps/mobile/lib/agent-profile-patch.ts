/**
 * Pure patch builder for the agent profile editor (RUYI-538 ①) — extracted
 * verbatim from edit-profile.tsx's useMemo so the UpdateAgentRequest
 * semantics carry unit coverage:
 *   - a field lands in the patch only when it differs from the saved Agent;
 *   - bounded numbers follow web BoundedNumberField revert semantics (an
 *     out-of-range draft omits the field — never clamped to a value the
 *     operator didn't type);
 *   - tri-state fields (thinking_level / service_tier / voice_runtime_id)
 *     rely on omission = "no change" and "" = explicit clear, matching
 *     UpdateAgentRequest semantics;
 *   - the subagents toggle round-trips through runtime_config
 *     (mergeSubagentAllowance), the same cascade web's editors apply.
 */
import {
  AGENT_MAX_CONCURRENT_TASKS_MAX,
  AGENT_MAX_CONCURRENT_TASKS_MIN,
  AGENT_SESSION_COMPACT_PCT_DEFAULT,
  AGENT_SESSION_COMPACT_PCT_MAX,
  AGENT_SESSION_COMPACT_PCT_MIN,
  AGENT_SESSION_MAX_CONTEXT_TOKENS_DEFAULT,
  AGENT_SESSION_MAX_CONTEXT_TOKENS_DISABLED,
  AGENT_SESSION_MAX_CONTEXT_TOKENS_MAX,
  AGENT_SESSION_MAX_CONTEXT_TOKENS_MIN,
} from "@multica/core/agents/constants";
import {
  allowsSubagents,
  mergeSubagentAllowance,
} from "@multica/core/agents/subagent-tools";
import type { Agent, UpdateAgentRequest } from "@multica/core/types";

/** The editor's full draft state — one string per text/number field. */
export interface AgentProfileDraft {
  name: string;
  description: string;
  instructions: string;
  avatarUrl: string;
  model: string;
  runtimeId: string;
  voiceRuntimeId: string;
  thinking: string;
  tier: string;
  concurrency: string;
  maxContext: string;
  compactPct: string;
  subagents: boolean;
}

/**
 * Integer draft → stored value, or null when the draft must not be stored:
 * non-numeric, or outside [min, max] (an `extra` sentinel such as the 0 =
 * disabled gate is admitted outside the range). Web BoundedNumberField
 * revert semantics — never clamp.
 */
export function parseBounded(
  raw: string,
  min: number,
  max: number,
  extra?: number,
): number | null {
  const parsed = parseInt(raw, 10);
  if (!Number.isFinite(parsed)) return null;
  if (extra !== undefined && parsed === extra) return parsed;
  if (parsed < min || parsed > max) return null;
  return parsed;
}

/**
 * Draft + saved agent → the PUT body. Returns null when the agent row isn't
 * loaded yet (the editor seeds its draft from it) or the name draft is
 * blank — in both cases there is nothing valid to save.
 */
export function buildProfilePatch(
  agent: Agent,
  draft: AgentProfileDraft,
): UpdateAgentRequest | null {
  if (!agent.id) return null;
  const trimmed = draft.name.trim();
  if (!trimmed) return null;
  const next: UpdateAgentRequest = {};
  if (trimmed !== agent.name) next.name = trimmed;
  if (draft.description !== agent.description)
    next.description = draft.description;
  if (draft.instructions !== agent.instructions)
    next.instructions = draft.instructions;
  if (draft.avatarUrl !== (agent.avatar_url ?? ""))
    next.avatar_url = draft.avatarUrl;
  if (draft.runtimeId !== agent.runtime_id) next.runtime_id = draft.runtimeId;
  // Tri-state voice slot (RUYI-425 §4.2): omitted = untouched, "" = unbind,
  // id = bind.
  if (draft.voiceRuntimeId !== (agent.voice_runtime_id ?? ""))
    next.voice_runtime_id = draft.voiceRuntimeId;
  if (draft.model !== agent.model) next.model = draft.model;
  if (draft.thinking !== (agent.thinking_level ?? ""))
    next.thinking_level = draft.thinking;
  if (draft.tier !== (agent.service_tier ?? "")) next.service_tier = draft.tier;
  const conc = parseBounded(
    draft.concurrency,
    AGENT_MAX_CONCURRENT_TASKS_MIN,
    AGENT_MAX_CONCURRENT_TASKS_MAX,
  );
  if (conc !== null && conc !== agent.max_concurrent_tasks)
    next.max_concurrent_tasks = conc;
  const ctx = parseBounded(
    draft.maxContext,
    AGENT_SESSION_MAX_CONTEXT_TOKENS_MIN,
    AGENT_SESSION_MAX_CONTEXT_TOKENS_MAX,
    AGENT_SESSION_MAX_CONTEXT_TOKENS_DISABLED,
  );
  if (
    ctx !== null &&
    ctx !==
      (agent.session_max_context_tokens ??
        AGENT_SESSION_MAX_CONTEXT_TOKENS_DEFAULT)
  )
    next.session_max_context_tokens = ctx;
  const pct = parseBounded(
    draft.compactPct,
    AGENT_SESSION_COMPACT_PCT_MIN,
    AGENT_SESSION_COMPACT_PCT_MAX,
  );
  if (
    pct !== null &&
    pct !== (agent.session_compact_pct ?? AGENT_SESSION_COMPACT_PCT_DEFAULT)
  )
    next.session_compact_pct = pct;
  if (draft.subagents !== allowsSubagents(agent.runtime_config)) {
    next.runtime_config = mergeSubagentAllowance(
      agent.runtime_config,
      draft.subagents,
    );
  }
  return next;
}
