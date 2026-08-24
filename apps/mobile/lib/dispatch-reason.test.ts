// @vitest-environment node
import { beforeAll, describe, expect, it } from "vitest";
import i18n from "i18next";
import { RESOURCES } from "@multica/views/locales";
import { dispatchReasonCode, sendFailureMessage } from "./dispatch-reason";

// `sendFailureMessage` 走 `i18n.t`，而未初始化的 i18next 返回 undefined
// —— 连第二个参数的 fallback 都不给。生产里 `initI18n()` 在 app 启动时
// 跑过，这里补上等价初始化。用真实 RESOURCES 而非 mock：断言的是用户
// 真正读到的那句话，mock 掉就只剩「调了 t()」这个空结论。
beforeAll(async () => {
  await i18n.init({
    resources: RESOURCES as never,
    lng: "en",
    fallbackLng: "en",
    defaultNS: "common",
    interpolation: { escapeValue: false },
  });
});

// Shaped like `apps/mobile/data/api.ts:ApiError` — a thrown Error carrying the
// parsed response body. Built inline rather than imported because `@/data/api`
// pulls react-native into a node-environment suite; the only field under test
// is `body`, which is what the helper reads.
const apiError = (body: unknown) =>
  Object.assign(new Error("request failed"), { body });

describe("dispatchReasonCode", () => {
  it("reads reason_code off a structured rejection body", () => {
    const err = apiError({
      error: "invocation not allowed",
      reason_code: "invocation_not_allowed",
    });
    expect(dispatchReasonCode(err)).toBe("invocation_not_allowed");
  });

  it("returns undefined for an unstructured failure", () => {
    expect(dispatchReasonCode(new Error("network down"))).toBeUndefined();
    expect(dispatchReasonCode(apiError(undefined))).toBeUndefined();
    expect(dispatchReasonCode(apiError("plain text body"))).toBeUndefined();
    expect(dispatchReasonCode(null)).toBeUndefined();
  });

  it("ignores a non-string or empty reason_code", () => {
    expect(dispatchReasonCode(apiError({ reason_code: 7 }))).toBeUndefined();
    expect(dispatchReasonCode(apiError({ reason_code: "" }))).toBeUndefined();
  });
});

describe("sendFailureMessage", () => {
  // The point of the helper: a revoked permission must not read as a transient
  // failure the user should retry (MUL-6380).
  it("names revoked permission instead of suggesting a retry", () => {
    const message = sendFailureMessage(
      apiError({ reason_code: "invocation_not_allowed" }),
    );
    expect(message).toMatch(/no longer have permission/i);
    expect(message).not.toMatch(/try again/i);
  });

  it("names the runtime for agent_runtime_required", () => {
    expect(
      sendFailureMessage(apiError({ reason_code: "agent_runtime_required" })),
    ).toMatch(/runtime/i);
  });

  it("falls back to a retryable message for anything else", () => {
    expect(sendFailureMessage(new Error("timeout"))).toMatch(/try again/i);
  });

  // 上面三条在 lng="en" 下无法区分「读到了资源」和「退回了第二参数的
  // 英文 fallback」——两者同文。切到 zh-Hans 才能证明确实走了资源：
  // 这三条曾是 chat.tsx 弹窗「标题中文、正文英文」的来源。
  it("跟随语言返回译文，而不是退回英文 fallback", async () => {
    await i18n.changeLanguage("zh-Hans");
    try {
      expect(
        sendFailureMessage(apiError({ reason_code: "invocation_not_allowed" })),
      ).toBe("你已不再拥有运行此 Agent 的权限，消息未发送。");
      expect(sendFailureMessage(new Error("timeout"))).toBe(
        "消息发送失败，请重试。",
      );
    } finally {
      await i18n.changeLanguage("en");
    }
  });
});
