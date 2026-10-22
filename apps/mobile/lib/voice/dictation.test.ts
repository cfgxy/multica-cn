/**
 * RUYI-694 D3 — 听写五态状态机测试（板1 形态C + 板6 交互标注）。
 * 钉住：120ms 起录、48dp 取消阈值、500ms 过短判定、端侧/降级双路径出口、
 * 取消与空转写不发送。
 */
import { describe, expect, it } from "vitest";
import {
  idleDictation,
  reduceDictation,
  resolveDictationEntry,
  CANCEL_ARM_DP,
  MIN_RECORD_MS,
  RECORD_ARM_MS,
  type DictationState,
} from "./dictation";

function press(baseline: DictationState = idleDictation) {
  return reduceDictation(baseline, { type: "press" });
}

function recording(degraded = false) {
  const r = press();
  return reduceDictation(r.state, {
    type: "beginRecord",
    atMs: 1_000,
    degraded,
  });
}

describe("起录门（120ms）", () => {
  it("press 进入 pressing，beginRecord 才起录并下发 startCapture", () => {
    const pressed = press();
    expect(pressed.state.phase).toBe("pressing");
    expect(pressed.commands).toEqual([]);

    const begun = reduceDictation(pressed.state, {
      type: "beginRecord",
      atMs: 1_000,
      degraded: false,
    });
    expect(begun.state.phase).toBe("recording");
    expect(begun.state.startedAtMs).toBe(1_000);
    expect(begun.commands).toEqual([{ type: "startCapture" }]);
  });

  it("未满 120ms 的轻点直接回 rest，无任何命令", () => {
    const pressed = press();
    const released = reduceDictation(pressed.state, {
      type: "release",
      heldMs: RECORD_ARM_MS - 1,
    });
    expect(released.state.phase).toBe("rest");
    expect(released.commands).toEqual([]);
  });
});

describe("取消态（上滑 >48dp）", () => {
  it("上滑过阈值进入取消态，回落退出", () => {
    const { state } = recording();
    const armed = reduceDictation(state, { type: "move", upDp: CANCEL_ARM_DP + 1 });
    expect(armed.state.cancelArmed).toBe(true);
    const disarmed = reduceDictation(armed.state, { type: "move", upDp: 10 });
    expect(disarmed.state.cancelArmed).toBe(false);
  });

  it("恰好等于阈值不触发（须严格大于）", () => {
    const { state } = recording();
    const armed = reduceDictation(state, { type: "move", upDp: CANCEL_ARM_DP });
    expect(armed.state.cancelArmed).toBe(false);
  });

  it("取消态松开：cancelCapture 废弃，不发送", () => {
    let r = recording();
    r = reduceDictation(r.state, { type: "move", upDp: 60 });
    r = reduceDictation(r.state, { type: "release", heldMs: 2_000 });
    expect(r.state.phase).toBe("rest");
    expect(r.commands).toEqual([{ type: "cancelCapture" }]);
  });
});

describe("过短录音（<500ms）", () => {
  it("松开即废弃并 toast「未识别到语音」", () => {
    const r = reduceDictation(recording().state, {
      type: "release",
      heldMs: MIN_RECORD_MS - 1,
    });
    expect(r.state.phase).toBe("rest");
    expect(r.commands).toEqual([
      { type: "cancelCapture" },
      { type: "toast", kind: "not_recognized" },
    ]);
  });

  it("时长恰好等于阈值有效（须不小于）", () => {
    const r = reduceDictation(recording().state, {
      type: "release",
      heldMs: MIN_RECORD_MS,
    });
    expect(r.state.phase).toBe("finalizing");
  });
});

describe("端侧路径（有实时预览）", () => {
  it("partial 更新预览；松开进入 finalizing 等 final", () => {
    let r = recording();
    r = reduceDictation(r.state, { type: "partial", text: "你好" });
    expect(r.state.preview).toBe("你好");
    r = reduceDictation(r.state, { type: "release", heldMs: 1_500 });
    expect(r.state.phase).toBe("finalizing");
    expect(r.commands).toEqual([{ type: "stopCapture" }]);
  });

  it("非空终稿以文本发送并回 rest", () => {
    let r = recording();
    r = reduceDictation(r.state, { type: "release", heldMs: 1_500 });
    r = reduceDictation(r.state, { type: "final", text: " 帮我查一下天气 " });
    expect(r.state.phase).toBe("rest");
    expect(r.commands).toEqual([{ type: "sendText", text: "帮我查一下天气" }]);
  });

  it("空终稿 toast 回 rest，不发送", () => {
    let r = recording();
    r = reduceDictation(r.state, { type: "release", heldMs: 1_500 });
    r = reduceDictation(r.state, { type: "final", text: "  " });
    expect(r.state.phase).toBe("rest");
    expect(r.commands).toEqual([{ type: "toast", kind: "not_recognized" }]);
  });

  it("识别器报错按语义分流：无匹配 toast 未识别，其他 toast 转写失败", () => {
    let r = recording();
    r = reduceDictation(r.state, { type: "release", heldMs: 1_500 });
    const emptyErr = reduceDictation(r.state, {
      type: "recognizerError",
      kind: "empty",
    });
    expect(emptyErr.commands).toEqual([{ type: "toast", kind: "not_recognized" }]);

    r = recording();
    r = reduceDictation(r.state, { type: "release", heldMs: 1_500 });
    const failErr = reduceDictation(r.state, {
      type: "recognizerError",
      kind: "failed",
    });
    expect(failErr.commands).toEqual([{ type: "toast", kind: "transcribe_failed" }]);
  });

  it("按住期间的错误保持录音态，正常松开后才提示失败", () => {
    let r = recording();
    r = reduceDictation(r.state, { type: "recognizerError", kind: "failed" });
    expect(r.state.phase).toBe("recording");
    expect(r.state.pendingError).toBe("failed");
    expect(r.commands).toEqual([]);

    r = reduceDictation(r.state, { type: "release", heldMs: 1_500 });
    expect(r.state.phase).toBe("rest");
    expect(r.commands).toEqual([
      { type: "cancelCapture" },
      { type: "toast", kind: "transcribe_failed" },
    ]);
  });

  it("取消和短录音优先废弃按住期间的错误", () => {
    let r = recording();
    r = reduceDictation(r.state, { type: "recognizerError", kind: "failed" });
    r = reduceDictation(r.state, { type: "move", upDp: CANCEL_ARM_DP + 1 });
    r = reduceDictation(r.state, { type: "release", heldMs: 1_500 });
    expect(r.commands).toEqual([{ type: "cancelCapture" }]);

    r = recording();
    r = reduceDictation(r.state, { type: "recognizerError", kind: "failed" });
    r = reduceDictation(r.state, { type: "release", heldMs: MIN_RECORD_MS - 1 });
    expect(r.commands).toEqual([
      { type: "cancelCapture" },
      { type: "toast", kind: "not_recognized" },
    ]);
  });

  it("按住期间的终稿覆盖已缓存错误，并在松开后发送", () => {
    let r = recording();
    r = reduceDictation(r.state, { type: "recognizerError", kind: "failed" });
    r = reduceDictation(r.state, { type: "final", text: " 发送这一句 " });
    expect(r.state.pendingError).toBeNull();
    expect(r.state.pendingFinal).toBe(" 发送这一句 ");
    expect(r.commands).toEqual([]);

    r = reduceDictation(r.state, { type: "release", heldMs: 1_500 });
    expect(r.commands).toEqual([{ type: "sendText", text: "发送这一句" }]);
  });
});

describe("降级路径（无端侧识别）", () => {
  it("松开进入 transcribing，发 stopCapture + requestBackendTranscription", () => {
    const r = recording(true);
    const released = reduceDictation(r.state, { type: "release", heldMs: 1_500 });
    expect(released.state.phase).toBe("transcribing");
    expect(released.commands).toEqual([
      { type: "stopCapture" },
      { type: "requestBackendTranscription" },
    ]);
  });

  it("转写完成以文本发送；空结果 toast 回 rest", () => {
    let r = recording(true);
    r = reduceDictation(r.state, { type: "release", heldMs: 1_500 });
    const done = reduceDictation(r.state, { type: "transcribed", text: "好的" });
    expect(done.commands).toEqual([{ type: "sendText", text: "好的" }]);

    r = recording(true);
    r = reduceDictation(r.state, { type: "release", heldMs: 1_500 });
    const empty = reduceDictation(r.state, { type: "transcribed", text: "" });
    expect(empty.commands).toEqual([{ type: "toast", kind: "not_recognized" }]);
  });

  it("后端转写失败 toast 后回 rest", () => {
    let r = recording(true);
    r = reduceDictation(r.state, { type: "release", heldMs: 1_500 });
    const failed = reduceDictation(r.state, {
      type: "recognizerError",
      kind: "failed",
    });
    expect(failed.state.phase).toBe("rest");
    expect(failed.commands).toEqual([{ type: "toast", kind: "transcribe_failed" }]);
  });
});

describe("乱序事件防御", () => {
  it("rest 期收到 partial/final/move 一律忽略", () => {
    expect(reduceDictation(idleDictation, { type: "partial", text: "x" }).commands).toEqual([]);
    expect(reduceDictation(idleDictation, { type: "final", text: "x" }).commands).toEqual([]);
    expect(reduceDictation(idleDictation, { type: "move", upDp: 100 }).state.cancelArmed).toBe(false);
  });

  it("重复 press 不叠加", () => {
    const once = press();
    const twice = reduceDictation(once.state, { type: "press" });
    expect(twice.state.phase).toBe("pressing");
  });
});

describe("听写入口模式（RUYI-710 阶段 2 降级放开）", () => {
  it("探测中一律 probing，无论转写通道是否接好", () => {
    expect(resolveDictationEntry(null, true)).toBe("probing");
    expect(resolveDictationEntry(null, false)).toBe("probing");
  });

  it("端侧可用恒为 ondevice（降级接入不影响可用设备回归）", () => {
    expect(resolveDictationEntry({ available: true, reason: "ok" }, true)).toBe("ondevice");
    expect(resolveDictationEntry({ available: true, reason: "ok" }, false)).toBe("ondevice");
  });

  it("端侧不可用且转写通道未接 → hidden（收窄行为不变）", () => {
    expect(resolveDictationEntry({ available: false, reason: "api_level" }, false)).toBe("hidden");
    expect(resolveDictationEntry({ available: false, reason: "module_missing" }, false)).toBe("hidden");
  });

  it("端侧不可用且转写通道已接 → degraded，但权限被拒仍 hidden（两条路径都要麦克风）", () => {
    expect(resolveDictationEntry({ available: false, reason: "api_level" }, true)).toBe("degraded");
    expect(resolveDictationEntry({ available: false, reason: "module_missing" }, true)).toBe("degraded");
    expect(resolveDictationEntry({ available: false, reason: "permission" }, true)).toBe("hidden");
  });

  it("非 Android（platform）与未知原因即使通道已接也 hidden", () => {
    expect(resolveDictationEntry({ available: false, reason: "platform" }, true)).toBe("hidden");
    expect(resolveDictationEntry({ available: false, reason: "whatever" }, true)).toBe("hidden");
  });
});
