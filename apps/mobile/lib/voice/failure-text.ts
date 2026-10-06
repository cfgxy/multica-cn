/**
 * 语音降级文案映射（RUYI-449）。纯函数、零 RN 依赖，与
 * `packages/views/voice/voice-overlay.tsx` 的映射语义逐条对齐：
 *
 *  - 麦克风权限被拒 → 引导系统设置（平台中立措辞，桌面/移动共享 key）。
 *  - `voice_unavailable` 之外的拒斥（鉴权内部细节、provider 不可达等）
 *    一律收敛为通用连接失败文案 —— 鉴权内部原因绝不外露。
 *  - `voice_unavailable` 的 6 个已知 reason 各自映射；未知 reason 不得
 *    产生 undefined 文案，回退通用文案。
 *
 * key 一律写 `voice:` 全限定形式：本文件没有 `useT` 绑定上下文（i18n-keys
 * 收集器按就近绑定归 ns，无绑定会误归 common），全限定形式对 i18next 与
 * 收集器都无歧义。
 */
import type { VoiceDegradeReason, VoiceRejection } from "@multica/core/voice";

export type VoiceFailure =
  | { kind: "permission" }
  | { kind: "rejection"; rejection: VoiceRejection | null };

/** overlay 传入的 t 的窄化视图；调用方用 `(key, dv) => t(key, { defaultValue: dv })` 适配。 */
export type VoiceTFn = (key: string, fallback?: string) => string;

export function voiceFailureText(failure: VoiceFailure, t: VoiceTFn): string {
  if (failure.kind === "permission") {
    return t(
      "voice:overlay.permission_denied",
      "Microphone access is required for voice. Enable it in your system settings and try again.",
    );
  }
  const rejection = failure.rejection;
  if (rejection?.kind !== "voice_unavailable") {
    return t(
      "voice:failure.connection_failed",
      "Voice couldn't connect right now. Text messaging still works as usual.",
    );
  }
  // 惰性命中单个 reason，而不是先对 6 个 key 各调一次 t：急切求值既浪费，
  // 也会让 mock 断言无法表达「只该查一条」。
  const known = (
    REASONS as Record<string, [key: string, fallback: string] | undefined>
  )[rejection.reason];
  if (!known) {
    // 未知的未来 wire reason：回退通用文案，绝不把 undefined 渲染给用户。
    return t(
      "voice:failure.connection_failed",
      "Voice couldn't connect right now. Text messaging still works as usual.",
    );
  }
  return t(known[0], known[1]);
}

const REASONS: Record<VoiceDegradeReason, [key: string, fallback: string]> = {
  no_voice_runtime: [
    "voice:failure.unavailable.no_voice_runtime",
    "This agent has no voice runtime bound yet. Ask a workspace admin to configure one — text messaging still works.",
  ],
  instance_disabled: [
    "voice:failure.unavailable.instance_disabled",
    "Voice is turned off for this agent's voice runtime. Text messaging still works as usual.",
  ],
  instance_not_active: [
    "voice:failure.unavailable.instance_not_active",
    "The voice runtime for this agent isn't active right now. Try again later, or keep using text.",
  ],
  capability_mismatch: [
    "voice:failure.unavailable.capability_mismatch",
    "This agent's model doesn't support voice conversations. Text messaging still works as usual.",
  ],
  credential_missing: [
    "voice:failure.unavailable.credential_missing",
    "Voice isn't fully configured for this agent (missing credentials). A workspace admin needs to finish setup.",
  ],
  credential_invalid: [
    "voice:failure.unavailable.credential_invalid",
    "The voice credentials for this agent are invalid or expired. A workspace admin needs to update them.",
  ],
};
