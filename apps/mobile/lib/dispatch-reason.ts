/**
 * Mobile-owned mirror of `packages/core/api/client.ts:dispatchReasonCode`.
 *
 * Why mirror instead of import: the core version narrows on core's own
 * `ApiError` class, and mobile throws `apps/mobile/data/api.ts:ApiError` — a
 * different constructor, so `instanceof` never matches across the two. The
 * extraction itself is identical: read the stable `reason_code` the admission
 * gate puts on a structured rejection body.
 */
import i18n from "i18next";

export function dispatchReasonCode(err: unknown): string | undefined {
  const body = (err as { body?: unknown } | null)?.body;
  if (body && typeof body === "object") {
    const code = (body as { reason_code?: unknown }).reason_code;
    if (typeof code === "string" && code.length > 0) return code;
  }
  return undefined;
}

/**
 * User-facing sentence for a refused send. `invocation_not_allowed` is the
 * revoked-permission case (MUL-4525): the session was created while the user
 * could run the agent and the server now refuses, so it must not read as a
 * transient failure the user should retry.
 *
 * 走 `i18n.t` 而非 `useT`：这是普通函数不是组件，取不到 hook。绝对 key
 * （`chat:mobile.send_failure.*`）与 `lib/auth-error.ts` 同一模式。注意
 * 本文件是 `.ts`，不在 i18n 覆盖率扫描面（只扫 `.tsx`）内 —— 这三条曾是
 * `chat.tsx` 弹窗「标题中文、正文英文」割裂的来源，防线看不见它们。
 */
export function sendFailureMessage(err: unknown): string {
  switch (dispatchReasonCode(err)) {
    case "invocation_not_allowed":
      return i18n.t(
        "chat:mobile.send_failure.no_permission",
        "You no longer have permission to run this agent, so the message was not sent.",
      );
    case "agent_runtime_required":
      return i18n.t(
        "chat:mobile.send_failure.runtime_required",
        "Bind a runtime to this agent before sending a message.",
      );
    default:
      return i18n.t(
        "chat:mobile.send_failure.generic",
        "Your message could not be sent. Please try again.",
      );
  }
}
