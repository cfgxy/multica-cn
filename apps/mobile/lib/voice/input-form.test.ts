// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  resolveInputShape,
  voiceGateVerdict,
  type InputShapeInput,
} from "./input-form";

const base: InputShapeInput = {
  disabled: false,
  sending: false,
  allowStop: true,
  allowSubmitWhileRunning: false,
  hasDraft: false,
  dictationOpen: false,
  voice: "available",
};

describe("voiceGateVerdict", () => {
  it("null/undefined gate（调用方未接门禁）→ resolving", () => {
    expect(voiceGateVerdict(null)).toBe("resolving");
    expect(voiceGateVerdict(undefined)).toBe("resolving");
  });

  it("resolving 或 availability 未决 → resolving（入口隐藏但不判负）", () => {
    expect(voiceGateVerdict({ availability: null, resolving: true })).toBe(
      "resolving",
    );
    expect(voiceGateVerdict({ availability: null, resolving: false })).toBe(
      "resolving",
    );
  });

  it("available → available；回退判负 → hidden", () => {
    expect(
      voiceGateVerdict({ availability: { available: true }, resolving: false }),
    ).toBe("available");
    expect(
      voiceGateVerdict({
        availability: { available: false, reason: "credential_invalid" },
        resolving: false,
      }),
    ).toBe("hidden");
  });
});

describe("resolveInputShape（板1 形态矩阵）", () => {
  it("空草稿 + 语音可用 → voice 形态（🎤🔵）", () => {
    expect(resolveInputShape(base)).toEqual({
      dictation: false,
      pill: false,
      rightSlot: "voice",
    });
  });

  it("空草稿 + 语音不可用/未决 → 灰化发送（无 🎤🔵）", () => {
    expect(
      resolveInputShape({ ...base, voice: "hidden" }).rightSlot,
    ).toBe("muted-send");
    expect(
      resolveInputShape({ ...base, voice: "resolving" }).rightSlot,
    ).toBe("muted-send");
  });

  it("有草稿 → send 形态（🎤🔵 让位），语音可用与否无关", () => {
    expect(resolveInputShape({ ...base, hasDraft: true }).rightSlot).toBe(
      "send",
    );
    expect(
      resolveInputShape({ ...base, hasDraft: true, voice: "available" })
        .rightSlot,
    ).toBe("send");
  });

  it("发送中 → 空草稿为停止；队列可用且有草稿时仅显示发送", () => {
    expect(resolveInputShape({ ...base, sending: true }).rightSlot).toBe(
      "stop",
    );
    expect(resolveInputShape({
      ...base,
      sending: true,
      allowSubmitWhileRunning: true,
      hasDraft: true,
    })).toMatchObject({ rightSlot: "send" });
    expect(resolveInputShape({
      ...base,
      sending: true,
      allowSubmitWhileRunning: true,
      hasDraft: false,
    }).rightSlot).toBe("stop");
    expect(
      resolveInputShape({ ...base, sending: true, allowStop: false })
        .rightSlot,
    ).toBe("voice");
  });

  it("disabled → pill（R1 语义），优先于一切", () => {
    expect(resolveInputShape({ ...base, disabled: true })).toEqual({
      dictation: false,
      pill: true,
      rightSlot: "muted-send",
    });
    expect(
      resolveInputShape({ ...base, disabled: true, dictationOpen: true }).pill,
    ).toBe(true);
  });

  it("听写展开 → 整条替换；sending 不闪回 bar", () => {
    expect(resolveInputShape({ ...base, dictationOpen: true })).toEqual({
      dictation: true,
      pill: false,
      rightSlot: "send",
    });
    expect(
      resolveInputShape({ ...base, dictationOpen: true, sending: true })
        .dictation,
    ).toBe(true);
  });
});
