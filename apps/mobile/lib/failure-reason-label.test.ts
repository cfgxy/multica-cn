import { describe, expect, it, vi, beforeEach } from "vitest";

/**
 * lib/failure-reason-label.ts 的单测（P1 批次 8）。
 *
 * 三条不变量，每条都对应一个真实缺陷形态：
 *  1. wire 值里的点号必须被换成 `__` 再拼 key —— 不换的话 i18next 会把
 *     `agent_error.provider_network` 当成两层嵌套去找一个不存在的子对象，
 *     26 条里有 14 条会静默退回 fallback，中文永远出不来。
 *  2. 未识别的 reason 不得进入 i18n 查找 —— 那会拼出缺失 key，i18next 在
 *     缺失时返回 key 本身，等于把 `chat:mobile.failure_reason.xxx` 这种
 *     内部串贴到用户脸上（本文件顶部注释刻意与 web 分道扬镳的那一点）。
 *  3. 每个 i18n 调用都必须带上英文兜底，且兜底文本与 LABELS 表一致。
 *
 * i18next 用 mock：真实资源解析由 lib/i18n-keys.test.ts 覆盖，这里要断言的
 * 是「传给 t 的 key 和 fallback 长什么样」，mock 才看得见。
 */

const { mockT } = vi.hoisted(() => ({ mockT: vi.fn() }));

vi.mock("i18next", () => ({ default: { t: mockT } }));

import { failureReasonLabel } from "./failure-reason-label";

beforeEach(() => {
  mockT.mockReset();
  // 默认行为模拟「已翻译」：返回一个可辨认的中文串，便于断言取的是译文
  // 而不是 fallback。
  mockT.mockImplementation((key: string) => `译:${key}`);
});

describe("failureReasonLabel", () => {
  it("带点号的 wire 值把点换成 __ 再拼 key", () => {
    const out = failureReasonLabel("agent_error.provider_network");
    expect(mockT).toHaveBeenCalledWith(
      "chat:mobile.failure_reason.agent_error__provider_network",
      "Network error reaching provider",
    );
    expect(out).toBe(
      "译:chat:mobile.failure_reason.agent_error__provider_network",
    );
  });

  it("不带点号的 wire 值原样拼 key", () => {
    failureReasonLabel("runtime_offline");
    expect(mockT).toHaveBeenCalledWith(
      "chat:mobile.failure_reason.runtime_offline",
      "Daemon offline",
    );
  });

  it("拼出的 key 里不得残留点号（除 ns 分隔的那一个冒号后的部分）", () => {
    failureReasonLabel("agent_error.runtime_missing_executable");
    const key = mockT.mock.calls[0]?.[0] as string;
    const afterNs = key.slice(key.indexOf(":") + 1);
    // `mobile.failure_reason.` 这三段是我们自己的层级，wire 值那一段之后
    // 不允许再有点。
    const wireSegment = afterNs.replace(/^mobile\.failure_reason\./, "");
    expect(wireSegment).not.toContain(".");
    expect(wireSegment).toBe("agent_error__runtime_missing_executable");
  });

  it("reason 为 null/undefined/空串时走 fallback key", () => {
    for (const empty of [null, undefined, ""]) {
      mockT.mockClear();
      const out = failureReasonLabel(empty);
      expect(mockT).toHaveBeenCalledWith(
        "chat:mobile.failure_reason.fallback",
        "Failed",
      );
      expect(out).toBe("译:chat:mobile.failure_reason.fallback");
    }
  });

  it("未识别的 reason 走 fallback key，绝不把 wire 值拼进 key", () => {
    const out = failureReasonLabel("some_future_reason_we_dont_know");
    expect(mockT).toHaveBeenCalledTimes(1);
    expect(mockT).toHaveBeenCalledWith(
      "chat:mobile.failure_reason.fallback",
      "Failed",
    );
    expect(out).toBe("译:chat:mobile.failure_reason.fallback");
    // 反向：wire 值一个字都不能出现在传给 t 的参数里。
    expect(JSON.stringify(mockT.mock.calls)).not.toContain(
      "some_future_reason",
    );
  });

  it("i18n 缺失时退回英文兜底（模拟 i18next 返回 key 本身的行为不该发生）", () => {
    // 模拟 i18next 命中 defaultValue 的行为：返回第二个参数。
    mockT.mockImplementation((_k: string, fb: string) => fb);
    expect(failureReasonLabel("timeout")).toBe("Task timed out");
    expect(failureReasonLabel("manual")).toBe("Cancelled by user");
    expect(failureReasonLabel(null)).toBe("Failed");
    expect(failureReasonLabel("nope")).toBe("Failed");
  });

  it("每次调用都传了非空的英文兜底", () => {
    const reasons = [
      "queued_expired",
      "runtime_recovery",
      "iteration_limit",
      "agent_blocked",
      "api_invalid_request",
      "skill_bundle_unavailable",
      "runtime_cli_timeout",
      "agent_error.provider_auth_or_access",
      "agent_error.context_overflow",
      "agent_error.unknown",
      "agent_error",
      "codex_semantic_inactivity",
    ];
    for (const r of reasons) failureReasonLabel(r);
    expect(mockT.mock.calls).toHaveLength(reasons.length);
    for (const [key, fb] of mockT.mock.calls) {
      expect(key).toMatch(/^chat:mobile\.failure_reason\./);
      expect(typeof fb).toBe("string");
      expect((fb as string).length).toBeGreaterThan(0);
    }
  });
});
