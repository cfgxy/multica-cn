/**
 * 听写链 hook（RUYI-694 板1 形态C + RUYI-710 阶段 2 降级接入）：把
 * `dictation.ts` 的五态纯状态机接到端侧 SpeechRecognizer 原生模块上。
 *
 * 职责切分：状态迁移全部在 reducer（已测）；本 hook 只负责
 *   1. 端侧可用性探测（模块缺失 / API < 31 / 无权限 → 不可用），并按
 *      `resolveDictationEntry` 收敛入口模式，
 *   2. press 的 120ms 起录门（轻点不进录音），
 *   3. reducer 命令的原生副作用：端侧路径走 SpeechRecognizer；降级路径
 *      （degraded）走 voice-audio 采集 PCM 分片，松开后封 WAV 走注入的
 *      `transcribe` 通道上传服务端转写端点，
 *   4. RMS 波形采样（端侧来自识别器事件，降级来自 PCM 分片折算；UI 态，
 *      不入状态机）与防卡死超时。
 *
 * 降级语义（设计板 1）：无实时预览、显「松开后转写」角标；转写失败或
 * 退出听写条即中止，录音不跨次保留，重新按住即重试。
 */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  idleDictation,
  RECORD_ARM_MS,
  reduceDictation,
  resolveDictationEntry,
  type DictationCommand,
  type DictationEntryMode,
  type DictationEvent,
  type DictationState,
} from "./dictation";
import { rmsFromPcmBase64, wavFromPcmBase64Chunks } from "./pcm-audio";
import { getSpeechRecognitionModule } from "./native-speech";
import { getVoiceAudioModule } from "./native-audio";

/** recording 最长按住时长——识别器异常不吐结果时兜底自动结算。 */
const MAX_RECORD_MS = 60_000;
/** finalizing 等 stop() flush 的 onFinalResult 超时。 */
const FINALIZE_TIMEOUT_MS = 15_000;
/** RMS 采样节流与波形槽数。 */
const RMS_INTERVAL_MS = 50;
const RMS_SLOTS = 28;

export interface UseDictationOptions {
  /** 端侧终稿与降级转写均以文本消息发送。 */
  onSend: (text: string) => void;
  /** toast 语义键，由调用方翻译文案。 */
  onToast: (kind: "not_recognized" | "transcribe_failed") => void;
  /**
   * 降级转写通道：上传内存 WAV，返回转写文本（空串 = 未识别到语音）。
   * 不传（或调用方数据未就绪）则端侧不可用设备隐藏听写入口——收窄期
   * 行为保持。第二参 signal：退出听写条即中止上传。
   */
  transcribe?: (audio: Blob, signal: AbortSignal) => Promise<string>;
}

export interface UseDictationResult {
  state: DictationState;
  /** 入口模式（探测中 / 端侧 / 降级 / 隐藏），听写 🎤 按钮据此显示。 */
  entry: DictationEntryMode;
  open: () => void;
  close: () => void;
  press: () => void;
  move: (upDp: number) => void;
  release: () => void;
  /** 最近 RMS 采样（0..1，旧→新），供波形条渲染。 */
  rms: number[];
}

export function useDictation(options: UseDictationOptions): UseDictationResult {
  const [state, setState] = useState<DictationState>(idleDictation);
  const [availability, setAvailability] = useState<{
    available: boolean;
    reason: string;
  } | null>(null);
  const [rms, setRms] = useState<number[]>([]);

  // 入口模式是纯派生：probe 结果 + 转写通道是否接好（调用方数据未就绪
  // 时 transcribe 为空，入口保持隐藏，通道接好后自动放开）。
  const entry: DictationEntryMode = useMemo(
    () => resolveDictationEntry(availability, options.transcribe != null),
    [availability, options.transcribe],
  );

  const stateRef = useRef(state);
  stateRef.current = state;
  const entryRef = useRef(entry);
  entryRef.current = entry;
  const lastRmsAtRef = useRef(0);
  const optionsRef = useRef(options);
  optionsRef.current = options;

  const pressAtRef = useRef<number | null>(null);
  const armTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const recordGuardRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const finalizeGuardRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const startFailureRef = useRef(false);

  // 降级采集面：分片桶（null = 不在降级录制中）、事件退订、在转写上传。
  const chunksRef = useRef<string[] | null>(null);
  const captureUnsubRef = useRef<(() => void) | null>(null);
  const transcribeAbortRef = useRef<AbortController | null>(null);

  // 降级命令处理器经 ref 间接：dispatch 稳定（deps []）而采集/上传闭包
  // 依赖 dispatch，直接互引会成环；ref 每渲染回填最新实现。
  const degradedRef = useRef({
    startCapture: () => {},
    stopCapture: (_keepChunks: boolean) => {},
    requestTranscription: () => {},
  });

  const clearTimers = useCallback(() => {
    if (armTimerRef.current) clearTimeout(armTimerRef.current);
    if (recordGuardRef.current) clearTimeout(recordGuardRef.current);
    if (finalizeGuardRef.current) clearTimeout(finalizeGuardRef.current);
    armTimerRef.current = null;
    recordGuardRef.current = null;
    finalizeGuardRef.current = null;
  }, []);

  const dispatch = useCallback((event: DictationEvent) => {
    const reduction = reduceDictation(stateRef.current, event);
    stateRef.current = reduction.state;
    setState(reduction.state);
    runCommands(reduction.commands, {
      degraded: reduction.state.degraded,
      degradedHandlers: degradedRef.current,
      onSend: (text) => optionsRef.current.onSend(text),
      onToast: (kind) => optionsRef.current.onToast(kind),
      onStartFailure: () => {
        if (stateRef.current.phase === "finalizing") {
          dispatch({ type: "recognizerError", kind: "failed" });
        } else {
          startFailureRef.current = true;
        }
      },
    });
    // 副作用定时器跟状态走：进 recording 起 60s 兜底，进 finalizing
    // 起 15s 兜底，回 idle 全清。降级 transcribing 的结算由上传
    // Promise（fetch 带 60s 超时）保证，无状态机定时器。
    if (recordGuardRef.current) clearTimeout(recordGuardRef.current);
    if (finalizeGuardRef.current) clearTimeout(finalizeGuardRef.current);
    recordGuardRef.current = null;
    finalizeGuardRef.current = null;
    if (reduction.state.phase === "recording") {
      recordGuardRef.current = setTimeout(() => {
        dispatch({ type: "release", heldMs: MAX_RECORD_MS });
      }, MAX_RECORD_MS);
    } else if (reduction.state.phase === "finalizing") {
      finalizeGuardRef.current = setTimeout(() => {
        dispatch({ type: "recognizerError", kind: "failed" });
      }, FINALIZE_TIMEOUT_MS);
    }
  }, []);

  // 模块探测（挂载一次）。模块缺失（非 Android）判 platform：iOS 的采集
  // 模块同样缺失，入口维持隐藏（本发布期语音面 Android-only）。
  useEffect(() => {
    const module = getSpeechRecognitionModule();
    if (!module) {
      setAvailability({ available: false, reason: "platform" });
      return;
    }
    let alive = true;
    void module.isOnDeviceAvailableAsync().then((result) => {
      if (alive) setAvailability(result);
    });
    return () => {
      alive = false;
    };
  }, []);

  // 原生事件 → reducer（端侧识别路径）。
  useEffect(() => {
    const module = getSpeechRecognitionModule();
    if (!module) return;
    const subs = [
      module.addListener("onPartialResult", (event) => {
        dispatch({ type: "partial", text: event.text ?? "" });
      }),
      module.addListener("onFinalResult", (event) => {
        dispatch({ type: "final", text: event.text ?? "" });
      }),
      module.addListener("onError", (event) => {
        // Android SpeechRecognizer：ERROR_NO_MATCH(6) / ERROR_SPEECH_TIMEOUT(7)
        // = 没听清（空结果语义），其余（网络、权限、客户端错误等）按失败。
        const kind =
          event.code === 6 || event.code === 7 ? "empty" : "failed";
        dispatch({ type: "recognizerError", kind });
      }),
    ];
    return () => {
      for (const sub of subs) sub.remove();
    };
  }, [dispatch]);

  // 卸载：清定时器 + 原生资源用完即毁兜底。
  useEffect(() => {
    return () => {
      clearTimers();
      transcribeAbortRef.current?.abort();
      degradedRef.current.stopCapture(false);
      const module = getSpeechRecognitionModule();
      if (module && stateRef.current.phase !== "rest" && !stateRef.current.degraded) {
        module.stop().catch(() => {});
      }
    };
  }, [clearTimers]);

  const startDegradedCapture = useCallback(() => {
    const audio = getVoiceAudioModule();
    if (!audio) {
      dispatch({ type: "recognizerError", kind: "failed" });
      return;
    }
    chunksRef.current = [];
    setRms([]);
    const chunkSub = audio.addListener("onAudioChunk", (event) => {
      const data = event.data ?? "";
      if (!data || chunksRef.current === null) return;
      chunksRef.current.push(data);
      const now = Date.now();
      if (now - lastRmsAtRef.current < RMS_INTERVAL_MS) return;
      lastRmsAtRef.current = now;
      setRms((current) =>
        [...current, rmsFromPcmBase64(data)].slice(-RMS_SLOTS),
      );
    });
    const errorSub = audio.addListener("onCaptureError", () => {
      dispatch({ type: "recognizerError", kind: "failed" });
    });
    captureUnsubRef.current = () => {
      chunkSub.remove();
      errorSub.remove();
    };
    audio.start().catch(() => {
      // 权限中途被收回等：走状态机错误出口（cancelCapture + toast）。
      dispatch({ type: "recognizerError", kind: "failed" });
    });
  }, [dispatch]);

  const stopDegradedCapture = useCallback((keepChunks: boolean) => {
    captureUnsubRef.current?.();
    captureUnsubRef.current = null;
    if (!keepChunks) chunksRef.current = null;
    getVoiceAudioModule()?.stop().catch(() => {});
  }, []);

  const requestBackendTranscription = useCallback(() => {
    const chunks = chunksRef.current ?? [];
    chunksRef.current = null;
    const wav = wavFromPcmBase64Chunks(chunks);
    if (!wav) {
      // 一帧都没采到（如权限收回导致静默失败）：按空结果收敛，不上传。
      dispatch({ type: "recognizerError", kind: "empty" });
      return;
    }
    const transcribe = optionsRef.current.transcribe;
    if (!transcribe) {
      dispatch({ type: "recognizerError", kind: "failed" });
      return;
    }
    const controller = new AbortController();
    transcribeAbortRef.current = controller;
    transcribe(new Blob([wav], { type: "audio/wav" }), controller.signal)
      .then((text) => {
        transcribeAbortRef.current = null;
        dispatch({ type: "transcribed", text });
      })
      .catch(() => {
        transcribeAbortRef.current = null;
        // 含调用方 abort（退出听写条）：此时 phase 已回 rest，reducer
        // 忽略迟到事件；条内失败则统一「转写失败」toast。
        dispatch({ type: "recognizerError", kind: "failed" });
      });
  }, [dispatch]);

  degradedRef.current = {
    startCapture: startDegradedCapture,
    stopCapture: stopDegradedCapture,
    requestTranscription: requestBackendTranscription,
  };

  const open = useCallback(() => {
    // 状态机没有 open 事件：听写条的 rest 就是 idleDictation，进入
    // 形态只需清一次本地采样，把可能残留的录音态归零；上一次未完成的
    // 降级上传一并中止（迟到结果落在 rest 被 reducer 忽略，双保险）。
    clearTimers();
    transcribeAbortRef.current?.abort();
    transcribeAbortRef.current = null;
    stateRef.current = idleDictation;
    setState(idleDictation);
    setRms([]);
  }, [clearTimers]);

  const close = useCallback(() => {
    clearTimers();
    transcribeAbortRef.current?.abort();
    transcribeAbortRef.current = null;
    const phase = stateRef.current.phase;
    const degraded = stateRef.current.degraded;
    stateRef.current = idleDictation;
    setState(idleDictation);
    setRms([]);
    if (degraded) {
      // 降级：录音中退出停采集丢分片；转写中退出已在上面的 abort。
      stopDegradedCapture(false);
    } else if (phase === "recording" || phase === "finalizing") {
      // 录音中退出：丢弃结果（迟到的 final 落在 rest 态被 reducer 忽略）。
      getSpeechRecognitionModule()?.stop().catch(() => {});
    }
  }, [clearTimers, stopDegradedCapture]);

  const press = useCallback(() => {
    const mode = entryRef.current;
    if ((mode !== "ondevice" && mode !== "degraded") || pressAtRef.current !== null) {
      return;
    }
    const atMs = Date.now();
    startFailureRef.current = false;
    pressAtRef.current = atMs;
    dispatch({ type: "press" });
    armTimerRef.current = setTimeout(() => {
      // 起录门到点必须自清引用：release() 以 armTimerRef 非空判「轻点」，
      // 残留真值会把所有正常指松都误判成轻点（heldMs 恒 0，录音永不结算）。
      armTimerRef.current = null;
      pressAtRef.current = null;
      dispatch({
        type: "beginRecord",
        atMs,
        // 降级模式由探测与转写通道共同决定（resolveDictationEntry）。
        degraded: mode === "degraded",
      });
    }, RECORD_ARM_MS);
  }, [dispatch]);

  const move = useCallback(
    (upDp: number) => {
      dispatch({ type: "move", upDp });
    },
    [dispatch],
  );

  const release = useCallback(() => {
    if (armTimerRef.current) {
      // 未满 120ms 的轻点：撤掉起录门，安静回 rest。
      clearTimeout(armTimerRef.current);
      armTimerRef.current = null;
      pressAtRef.current = null;
      dispatch({ type: "release", heldMs: 0 });
      return;
    }
    pressAtRef.current = null;
    const startedAt = stateRef.current.startedAtMs;
    const heldMs = startedAt === null ? 0 : Math.max(0, Date.now() - startedAt);
    dispatch({ type: "release", heldMs });
    if (startFailureRef.current) {
      startFailureRef.current = false;
      // start() can reject while Android is still acquiring the recognizer.
      // Keep a held press quiet and surface the classified failure on release.
      dispatch({ type: "recognizerError", kind: "failed" });
    }
  }, [dispatch]);

  // RMS 事件：不入状态机，节流后供波形条。端侧识别器只在 recording 中
  // 吐 onRms；降级路径的波形来自 startDegradedCapture 里的 PCM 折算。
  useEffect(() => {
    const module = getSpeechRecognitionModule();
    if (!module) return;
    const sub = module.addListener("onRms", (event) => {
      const now = Date.now();
      if (now - lastRmsAtRef.current < RMS_INTERVAL_MS) return;
      lastRmsAtRef.current = now;
      const value = Math.max(0, Math.min(1, event.value ?? 0));
      setRms((current) => [...current, value].slice(-RMS_SLOTS));
    });
    return () => sub.remove();
  }, []);

  return { state, entry, open, close, press, move, release, rms };
}

function runCommands(
  commands: DictationCommand[],
  handlers: {
    degraded: boolean;
    degradedHandlers: {
      startCapture: () => void;
      stopCapture: (keepChunks: boolean) => void;
      requestTranscription: () => void;
    };
    onSend: (text: string) => void;
    onToast: (kind: "not_recognized" | "transcribe_failed") => void;
    onStartFailure: () => void;
  },
): void {
  for (const command of commands) {
    switch (command.type) {
      case "startCapture":
        if (handlers.degraded) {
          handlers.degradedHandlers.startCapture();
        } else {
          getSpeechRecognitionModule()
            ?.start()
            .catch(() => {
              handlers.onStartFailure();
            });
        }
        break;
      case "stopCapture":
        if (handlers.degraded) {
          // 降级：停采集并退订分片；已采分片保留给随后的
          // requestBackendTranscription 封 WAV 上传。
          handlers.degradedHandlers.stopCapture(true);
        } else {
          // stop 后识别器 flush 并自毁；结果是否被采纳由状态机阶段决定，
          // hook 层不二次裁决。
          getSpeechRecognitionModule()?.stop().catch(() => {});
        }
        break;
      case "cancelCapture":
        if (handlers.degraded) {
          // 取消路径无上传，分片一并丢弃。
          handlers.degradedHandlers.stopCapture(false);
        } else {
          getSpeechRecognitionModule()?.stop().catch(() => {});
        }
        break;
      case "requestBackendTranscription":
        handlers.degradedHandlers.requestTranscription();
        break;
      case "sendText":
        handlers.onSend(command.text);
        break;
      case "toast":
        handlers.onToast(command.kind);
        break;
    }
  }
}
