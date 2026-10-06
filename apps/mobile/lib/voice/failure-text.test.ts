import { describe, expect, it, vi } from "vitest";

/**
 * lib/voice/failure-text.ts 单测（RUYI-449）。验证降级文案映射的三条
 * 不变量：
 *  1. 权限拒绝走 permission_denied（平台中立措辞）。
 *  2. `voice_unavailable` 之外的一切拒斥（auth_failed / provider
 *     unreachable / session_rejected / null）收敛为同一条通用连接失败
 *     文案 —— 鉴权内部细节绝不外露。
 *  3. 6 个已知 degrade reason 各自命中；未知 reason 回退通用文案而非
 *     产生 undefined。
 *
 * i18next 用 mock（与 failure-reason-label.test.ts 同策略）：真实资源
 * 解析由 lib/i18n-keys.test.ts 覆盖，这里断言「传给 t 的 key 与
 * fallback 长什么样」。所有 key 带 `voice:` 全限定前缀也是断言点——
 * 本模块无 useT 绑定上下文，靠前缀才能被 i18n-keys 收集器正确归 ns。
 */

const mockT = vi.fn(
  (key: string, fallback?: string) => `译:${key}[${fallback}]`,
);

import { voiceFailureText } from "./failure-text";
import type { VoiceFailure } from "./failure-text";
import type { VoiceRejection } from "@multica/core/voice";

/** 构造 rejection 分支；「未来新增的 wire reason」用 as 模拟，属防线目标形态。 */
function rejection(rejection: VoiceRejection | null): VoiceFailure {
  return { kind: "rejection", rejection };
}

describe("voiceFailureText", () => {
  it("权限拒绝 → permission_denied，带系统设置引导", () => {
    mockT.mockClear();
    const out = voiceFailureText({ kind: "permission" }, mockT);
    expect(mockT).toHaveBeenCalledWith(
      "voice:overlay.permission_denied",
      "Microphone access is required for voice. Enable it in your system settings and try again.",
    );
    expect(out).toContain("voice:overlay.permission_denied");
  });

  it.each([
    ["auth_failed 帧", { kind: "auth_failed" } as unknown as VoiceRejection],
    [
      "provider_unreachable 帧",
      { kind: "provider_unreachable" } as unknown as VoiceRejection,
    ],
    [
      "session_rejected 帧",
      { kind: "session_rejected" } as unknown as VoiceRejection,
    ],
    ["null 拒斥", null],
  ])("非 voice_unavailable（%s）收敛为通用连接失败文案", (_name, rej) => {
    mockT.mockClear();
    const out = voiceFailureText(rejection(rej), mockT);
    expect(mockT).toHaveBeenCalledTimes(1);
    expect(mockT).toHaveBeenCalledWith(
      "voice:failure.connection_failed",
      "Voice couldn't connect right now. Text messaging still works as usual.",
    );
    expect(out).toContain("voice:failure.connection_failed");
  });

  it("voice_unavailable 的 6 个已知 reason 各自命中独立 key", () => {
    const cases: Array<[string, string]> = [
      ["no_voice_runtime", "voice:failure.unavailable.no_voice_runtime"],
      ["instance_disabled", "voice:failure.unavailable.instance_disabled"],
      ["instance_not_active", "voice:failure.unavailable.instance_not_active"],
      ["capability_mismatch", "voice:failure.unavailable.capability_mismatch"],
      ["credential_missing", "voice:failure.unavailable.credential_missing"],
      ["credential_invalid", "voice:failure.unavailable.credential_invalid"],
    ];
    for (const [reason, key] of cases) {
      mockT.mockClear();
      voiceFailureText(
        rejection({ kind: "voice_unavailable", reason } as VoiceRejection),
        mockT,
      );
      expect(mockT).toHaveBeenCalledTimes(1);
      expect(mockT.mock.calls[0]?.[0]).toBe(key);
    }
  });

  it("未知 reason 回退通用文案，不产生 undefined", () => {
    mockT.mockClear();
    const out = voiceFailureText(
      rejection({
        kind: "voice_unavailable",
        reason: "some_future_reason",
      } as unknown as VoiceRejection),
      mockT,
    );
    expect(mockT).toHaveBeenCalledTimes(1);
    expect(mockT.mock.calls[0]?.[0]).toBe("voice:failure.connection_failed");
    expect(out).not.toContain("undefined");
  });
});
