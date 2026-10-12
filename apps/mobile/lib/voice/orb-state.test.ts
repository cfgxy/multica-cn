// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  miniPhase,
  orbPalette,
  orbStatusLabelKey,
  reduceOrbPhase,
  type OrbPhase,
} from "./orb-state";

/** 主链：connecting → listening → userSpeaking → thinking → speaking。 */
function happyPath(): OrbPhase {
  let p: OrbPhase = "connecting";
  p = reduceOrbPhase(p, { type: "sessionState", state: "live" });
  p = reduceOrbPhase(p, { type: "userSpeechStarted" });
  p = reduceOrbPhase(p, { type: "userSpeechEnded" });
  p = reduceOrbPhase(p, { type: "assistantStarted" });
  return p;
}

describe("reduceOrbPhase（板3 状态机）", () => {
  it("主链迁移 connecting→listening→userSpeaking→thinking→speaking", () => {
    expect(happyPath()).toBe("speaking");
  });

  it("speaking 结束回 listening；再次开口重新走 userSpeaking", () => {
    let p = reduceOrbPhase(happyPath(), { type: "assistantEnded" });
    expect(p).toBe("listening");
    p = reduceOrbPhase(p, { type: "userSpeechStarted" });
    expect(p).toBe("userSpeaking");
  });

  it("speaking 中插话（barge-in）→ interrupted；600ms 瞬态回 listening", () => {
    let p = reduceOrbPhase(happyPath(), { type: "interrupted" });
    expect(p).toBe("interrupted");
    p = reduceOrbPhase(p, { type: "interruptedSettled" });
    expect(p).toBe("listening");
  });

  it("thinking/userSpeaking/listening 中的插话同样进 interrupted", () => {
    for (const base of ["listening", "userSpeaking", "thinking"] as OrbPhase[]) {
      expect(reduceOrbPhase(base, { type: "interrupted" })).toBe("interrupted");
    }
  });

  it("assistant 可先开口（listening 直接 assistantStarted → speaking）", () => {
    let p: OrbPhase = "connecting";
    p = reduceOrbPhase(p, { type: "sessionState", state: "live" });
    p = reduceOrbPhase(p, { type: "assistantStarted" });
    expect(p).toBe("speaking");
  });

  it("failed 终态不被 sessionState 翻回（拒绝后不复活）", () => {
    let p: OrbPhase = reduceOrbPhase("connecting", { type: "degraded" });
    expect(p).toBe("failed");
    p = reduceOrbPhase(p, { type: "sessionState", state: "live" });
    p = reduceOrbPhase(p, { type: "sessionState", state: "connecting" });
    expect(p).toBe("failed");
  });

  it("断线从任意活跃态进 disconnected；重连回到 connecting 重新走链", () => {
    let p = reduceOrbPhase(happyPath(), { type: "connectionLost" });
    expect(p).toBe("disconnected");
    p = reduceOrbPhase(p, { type: "reconnected" });
    expect(p).toBe("connecting");
    p = reduceOrbPhase(p, { type: "sessionState", state: "live" });
    expect(p).toBe("listening");
  });

  it("interrupted 瞬态断线：disconnected 优先于 settled（不闪回 listening）", () => {
    let p = reduceOrbPhase("speaking", { type: "interrupted" });
    p = reduceOrbPhase(p, { type: "connectionLost" });
    p = reduceOrbPhase(p, { type: "interruptedSettled" });
    expect(p).toBe("disconnected");
  });

  it("乱序事件不迁移：resting 相位收到不相关事件保持原状", () => {
    expect(reduceOrbPhase("listening", { type: "interruptedSettled" })).toBe("listening");
    expect(reduceOrbPhase("connecting", { type: "userSpeechStarted" })).toBe("connecting");
    expect(reduceOrbPhase("speaking", { type: "userSpeechEnded" })).toBe("speaking");
  });
});

describe("miniPhase（板4 迷你球归并）", () => {
  it("UserSpeaking 与 Interrupted 并入聆听；其余一一对应；failed 归断线", () => {
    expect(miniPhase("userSpeaking")).toBe("listening");
    expect(miniPhase("interrupted")).toBe("listening");
    expect(miniPhase("listening")).toBe("listening");
    expect(miniPhase("thinking")).toBe("thinking");
    expect(miniPhase("speaking")).toBe("speaking");
    expect(miniPhase("connecting")).toBe("connecting");
    expect(miniPhase("disconnected")).toBe("disconnected");
    expect(miniPhase("failed")).toBe("disconnected");
  });
});

describe("色板与文案", () => {
  it("思考紫 #8B5CF6、打断红 glow、断线/失败灰化", () => {
    expect(orbPalette("thinking").core).toBe("#8B5CF6");
    expect(orbPalette("interrupted").glow).toBe("#EF4444");
    expect(orbPalette("disconnected").core).toBe(orbPalette("failed").core);
  });

  it("每态有唯一状态文案 key", () => {
    const phases: OrbPhase[] = [
      "connecting",
      "listening",
      "userSpeaking",
      "thinking",
      "speaking",
      "interrupted",
      "disconnected",
      "failed",
    ];
    expect(new Set(phases.map(orbStatusLabelKey)).size).toBe(phases.length);
  });
});
