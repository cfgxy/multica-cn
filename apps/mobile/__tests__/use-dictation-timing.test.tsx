import { act, renderHook } from "@testing-library/react-native";
import { MIN_RECORD_MS, RECORD_ARM_MS } from "@/lib/voice/dictation";
import { useDictation } from "@/lib/voice/use-dictation";

/**
 * RUYI-710 阶段 2 返工 — 听写 hook press/release 定时器引用时序回归面。
 *
 * P0 根因：press 的 120ms 起录门定时器触发后只清 pressAtRef、不清
 * armTimerRef，release() 以 armTimerRef 非空判「轻点」，导致按住 ≥120ms
 * 的正常指松永远按 heldMs:0 走误触取消——录音不上传/不发送，toast
 * 「未识别到语音」。hook 层此前无直接测试（两轮 QA 未拦住本缺陷的盲区），
 * 本套件用假时钟逐路径钉住：轻点静默、正常指松必达结算（端侧
 * finalizing / 降级 transcribing+上传）、60s 兜底自动结算、120–500ms
 * 短录音误触门保持。
 */

type NativeEvent = { data?: string; text?: string; code?: number; value?: number };
type NativeListener = (event: NativeEvent) => void;

/** 原生模块事件面桩：addListener/emit 同构 expo-modules-core 事件订阅。 */
function makeEventBus() {
  const listeners = new Map<string, Set<NativeListener>>();
  return {
    addListener: (type: string, cb: NativeListener) => {
      if (!listeners.has(type)) listeners.set(type, new Set());
      listeners.get(type)!.add(cb);
      return { remove: () => listeners.get(type)?.delete(cb) };
    },
    emit: (type: string, event: NativeEvent) => {
      for (const cb of [...(listeners.get(type) ?? [])]) cb(event);
    },
  };
}

const mockSpeechModule = {
  ...makeEventBus(),
  isOnDeviceAvailableAsync: jest.fn<Promise<{ available: boolean; reason: string }>, []>(),
  start: jest.fn<Promise<null>, []>(),
  stop: jest.fn<Promise<null>, []>(),
};

const mockAudioModule = {
  ...makeEventBus(),
  start: jest.fn<Promise<null>, []>(),
  stop: jest.fn<Promise<null>, []>(),
};

jest.mock("@/lib/voice/native-speech", () => ({
  getSpeechRecognitionModule: () => mockSpeechModule,
}));

jest.mock("@/lib/voice/native-audio", () => ({
  getVoiceAudioModule: () => mockAudioModule,
}));

let onSend: jest.Mock;
let onToast: jest.Mock;
let transcribe: jest.Mock;
/** 降级上传手动放行：transcribing 是瞬态相位，立即 resolve 的桩会在
 * act flush 中直接走到 rest，观察不到结算分支本身。 */
let resolveTranscribe!: (text: string) => void;

beforeEach(() => {
  // 调用计数逐用例隔离：原生 start/stop 的次数断言不可跨用例累积。
  jest.clearAllMocks();
  jest.useFakeTimers();
  mockSpeechModule.start.mockResolvedValue(null);
  mockSpeechModule.stop.mockResolvedValue(null);
  mockAudioModule.start.mockResolvedValue(null);
  mockAudioModule.stop.mockResolvedValue(null);
  onSend = jest.fn();
  onToast = jest.fn();
  // 默认立即成功；需要观察 transcribing 相位或 60s 兜底的用例改挂 deferred。
  transcribe = jest.fn(async () => "你好");
});

afterEach(() => {
  jest.useRealTimers();
});

/** 挂载 hook 并 flush 可用性探测——entry 离开 probing 后 press 才生效。 */
async function mountDictation(availability: { available: boolean; reason: string }) {
  mockSpeechModule.isOnDeviceAvailableAsync.mockResolvedValue(availability);
  const view = await renderHook(() =>
    useDictation({ onSend, onToast, transcribe }),
  );
  await act(async () => {});
  return view;
}

/** 降级转写桩切换为手动放行（上传挂起，transcribing 可观察）。 */
function deferTranscribe() {
  transcribe.mockImplementation(
    () =>
      new Promise<string>((resolve) => {
        resolveTranscribe = resolve;
      }),
  );
}

/** 按下并推进满起录门（120ms）：进入 recording 且起录副作用已发。 */
async function pressAndArm(result: { current: ReturnType<typeof useDictation> }) {
  await act(async () => {
    result.current.press();
  });
  await act(async () => {
    jest.advanceTimersByTime(RECORD_ARM_MS);
  });
}

describe("useDictation 指松结算时序（RUYI-710 P0 回归面）", () => {
  it("轻点 <120ms 松开：不起录、安静回 rest、无 toast", async () => {
    const { result } = await mountDictation({ available: true, reason: "ok" });
    expect(result.current.entry).toBe("ondevice");

    await act(async () => {
      result.current.press();
    });
    expect(result.current.state.phase).toBe("pressing");
    // 未推进假时钟即松开：arm 定时器未触发，属真实轻点。
    await act(async () => {
      result.current.release();
    });

    expect(result.current.state.phase).toBe("rest");
    expect(mockSpeechModule.start).not.toHaveBeenCalled();
    expect(onToast).not.toHaveBeenCalled();
    expect(onSend).not.toHaveBeenCalled();
  });

  it("按住 ≥500ms 松开（端侧）：必达结算进 finalizing，终稿后 sendText", async () => {
    const { result } = await mountDictation({ available: true, reason: "ok" });
    expect(result.current.entry).toBe("ondevice");
    await pressAndArm(result);
    expect(result.current.state.phase).toBe("recording");
    expect(mockSpeechModule.start).toHaveBeenCalledTimes(1);

    // 总按住 520ms ≥ MIN_RECORD_MS：指松必须走到端侧结算分支。
    await act(async () => {
      jest.advanceTimersByTime(MIN_RECORD_MS - RECORD_ARM_MS + 20);
    });
    await act(async () => {
      result.current.release();
    });
    expect(result.current.state.phase).toBe("finalizing");

    // 识别器 flush 终稿：发送文本消息并回 rest。
    await act(async () => {
      mockSpeechModule.emit("onFinalResult", { text: "你好世界" });
    });
    expect(onSend).toHaveBeenCalledWith("你好世界");
    expect(result.current.state.phase).toBe("rest");
  });

  it("端侧 start 拒绝：按住期间不提示，指松后才按失败语义收敛", async () => {
    mockSpeechModule.start.mockRejectedValueOnce(new Error("recognizer unavailable"));
    const { result } = await mountDictation({ available: true, reason: "ok" });
    await pressAndArm(result);
    await act(async () => {});
    expect(onToast).not.toHaveBeenCalled();

    await act(async () => {
      jest.advanceTimersByTime(MIN_RECORD_MS - RECORD_ARM_MS + 20);
      result.current.release();
    });
    expect(onToast).toHaveBeenCalledWith("transcribe_failed");
    expect(result.current.state.phase).toBe("rest");
  });

  it("按住 ≥500ms 松开（降级）：必达结算进 transcribing 并发起上传", async () => {
    deferTranscribe();
    const { result } = await mountDictation({
      available: false,
      reason: "api_level",
    });
    expect(result.current.entry).toBe("degraded");
    await pressAndArm(result);
    expect(result.current.state.phase).toBe("recording");
    expect(mockAudioModule.start).toHaveBeenCalledTimes(1);

    // 喂一帧非空分片（「AAAA」→ 3 字节 PCM），封 WAV 走上传而非空收敛。
    await act(async () => {
      mockAudioModule.emit("onAudioChunk", { data: "AAAA" });
    });

    await act(async () => {
      jest.advanceTimersByTime(MIN_RECORD_MS - RECORD_ARM_MS + 20);
    });
    await act(async () => {
      result.current.release();
    });
    expect(result.current.state.phase).toBe("transcribing");
    expect(transcribe).toHaveBeenCalledTimes(1);
    expect(transcribe.mock.calls[0][0]).toBeInstanceOf(Blob);

    // 放行上传：转写文本发送并回 rest。
    resolveTranscribe("你好");
    await act(async () => {});
    expect(onSend).toHaveBeenCalledWith("你好");
    expect(result.current.state.phase).toBe("rest");
  });

  it("60s 兜底：不松手也自动结算并上传", async () => {
    deferTranscribe();
    const { result } = await mountDictation({
      available: false,
      reason: "api_level",
    });
    await pressAndArm(result);
    await act(async () => {
      mockAudioModule.emit("onAudioChunk", { data: "AAAA" });
    });

    // 防卡死守卫到点：绕过 release() 直接结算，行为与指松一致。
    await act(async () => {
      jest.advanceTimersByTime(60_000);
    });
    expect(result.current.state.phase).toBe("transcribing");
    expect(transcribe).toHaveBeenCalledTimes(1);

    resolveTranscribe("你好");
    await act(async () => {});
    expect(onSend).toHaveBeenCalledWith("你好");
    expect(result.current.state.phase).toBe("rest");
  });

  it(`短录音（${RECORD_ARM_MS}–${MIN_RECORD_MS}ms）松开仍按误触取消`, async () => {
    const { result } = await mountDictation({ available: true, reason: "ok" });
    await pressAndArm(result);
    expect(result.current.state.phase).toBe("recording");

    // 总按住 320ms，落在起录门与误触门之间：取消 + toast，不发送。
    await act(async () => {
      jest.advanceTimersByTime(200);
    });
    await act(async () => {
      result.current.release();
    });

    expect(result.current.state.phase).toBe("rest");
    expect(mockSpeechModule.stop).toHaveBeenCalledTimes(1);
    expect(onToast).toHaveBeenCalledWith("not_recognized");
    expect(onSend).not.toHaveBeenCalled();
  });
});
