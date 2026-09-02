/**
 * Mirror of `packages/views/agents/components/tabs/task-failure.ts:REASON_LABEL`.
 *
 * Why mirror: mobile cannot import from packages/views per the apps/mobile
 * CLAUDE.md sharing rule. Only the human copy is mobile-owned.
 *
 * Keyed by the raw wire value rather than a closed enum, same as the web map:
 * `failure_reason` is an open string that grows as classifier rules land, and
 * an installed build will meet reasons it predates. Before MUL-5370 this was a
 * `Record<TaskFailureReason, string>` holding only the six pre-MUL-1949 coarse
 * values, so every refined `agent_error.*` the backend has written since
 * missed the lookup and rendered a bare "Failed".
 *
 * Divergence from web, deliberate: the web helper falls back to the raw wire
 * value, which is machine-y but searchable — right for an operator reading the
 * execution log. This one backs a chat bubble read by the person who just sent
 * a message, so an unrecognised reason degrades to a plain "Failed" instead of
 * leaking an enum string at them.
 */
import i18n from "i18next";

// 英文兜底表——i18n key 缺失时回落到这里，保留不删（照 lib/issue-status.ts
// 的 *_EN 范式）。
const LABELS: Record<string, string> = {
  // Platform / scheduler side.
  queued_expired: "Expired in queue",
  runtime_offline: "Daemon offline",
  runtime_recovery: "Daemon restarted",
  timeout: "Task timed out",
  iteration_limit: "Hit the iteration limit",
  agent_blocked: "Waiting on human input",
  api_invalid_request: "Rejected by the model API",
  skill_bundle_unavailable: "Couldn't download the agent's skills",
  runtime_cli_timeout: "Local runtime CLI timed out",

  // Agent process side — provider.
  "agent_error.provider_auth_or_access": "Provider auth failed",
  "agent_error.provider_quota_limit": "Provider quota exhausted",
  "agent_error.provider_capacity_or_rate_limit": "Rate limited by provider",
  "agent_error.provider_server_error": "Provider server error",
  "agent_error.provider_network": "Network error reaching provider",

  // Agent process side — agent / runner.
  "agent_error.process_failure": "Agent process crashed",
  "agent_error.empty_or_unparseable_output": "Agent returned no usable output",
  "agent_error.agent_timeout": "Agent timed out",
  "agent_error.context_overflow": "Context window exceeded",
  "agent_error.missing_config": "Missing API key or configuration",
  "agent_error.model_not_found_or_unavailable": "Model unavailable",
  "agent_error.runtime_version_unsupported": "Runner CLI version unsupported",
  "agent_error.runtime_missing_executable": "Runner CLI not installed",
  "agent_error.unknown": "Agent execution error",

  // Pre-MUL-1949 coarse values, still present on historical rows.
  agent_error: "Agent execution error",
  codex_semantic_inactivity: "Codex semantic inactivity timeout",
  manual: "Cancelled by user",
};

/**
 * wire 值 → i18n key 片段。`failure_reason` 里的点号（`agent_error.provider_network`）
 * 会被 i18next 当作嵌套层级分隔符，直接拼进 key 会去找一个并不存在的
 * `provider_network` 子对象；统一换成双下划线，与资源文件里的写法对齐。
 */
function reasonKeySegment(reason: string): string {
  return reason.replace(/\./g, "__");
}

export function failureReasonLabel(reason: string | null | undefined): string {
  if (!reason) return i18n.t("chat:mobile.failure_reason.fallback", "Failed");
  const en = LABELS[reason];
  // 未识别的 reason 不落到 i18n 查找——那会拼出一个不存在的 key，i18next
  // 在缺失时返回 key 本身，等于把 wire 值泄漏给用户（本文件顶部注释里
  // 刻意与 web 分道扬镳的那一点）。走同一条 fallback 分支。
  if (en == null) return i18n.t("chat:mobile.failure_reason.fallback", "Failed");
  return i18n.t(`chat:mobile.failure_reason.${reasonKeySegment(reason)}`, en);
}

/**
 * Whether the reason has a human label in the map above. Callers that must
 * distinguish "recognised" from "generic Failed fallback" (e.g. the run-detail
 * cancel-reason classification, which would otherwise read
 * "Cancelled · Failed") gate on this instead of string-comparing the fallback.
 */
export function isKnownFailureReason(reason: string | null | undefined): boolean {
  if (!reason) return false;
  return Object.prototype.hasOwnProperty.call(LABELS, reason);
}
