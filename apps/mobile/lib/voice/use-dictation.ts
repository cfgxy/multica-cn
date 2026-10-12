/**
 * 听写链 hook（RUYI-694 板1 形态C）：把 `dictation.ts` 的五态纯状态机
 * 接到端侧 SpeechRecognizer 原生模块上。
 *
 * 职责切分：状态迁移全部在 reducer（已测）；本 hook 只负责
 *   1. 端侧可用性探测（模块缺失 / API < 31 / 无权限 → 不可用），
 *   2. press 的 120ms 起录门（轻点不进录音），
 *   3. reducer 命令的原生副作用（start/stop/capture、sendText、toast），
 *   4. RMS 波形采样（UI 态，不入状态机）与防卡死超时。
 *
 * 降级分支（后端转写）的状态迁移已测但入口当前被收窄：本单端侧不可用
 * 即隐藏听写入口（degraded 恒 false），等 Leader 对转写端点的裁定回来
 * 再放开 degraded 路径——hook 与状态机无需再改。
 */
import { useCallback, useEffect, useRef, useState } from "react";
import {
  idleDictation,
  RECORD_ARM_MS,
  reduceDictation,
  type DictationCommand,
  type DictationEvent,
  type DictationState,
} from "./dictation";
import { getSpeechRecognitionModule } from "./native-speech";

/** recording 最长按住时长——识别器异常不吐结果时兜底自动结算。 */
const MAX_RECORD_MS = 60_000;
/** finalizing 等 stop() flush 的 onFinalResult 超时。 */
const FINALIZE_TIMEOUT_MS = 15_000;
/** RMS 采样节流与波形槽数。 */
const RMS_INTERVAL_MS = 50;
const RMS_SLOTS = 28;

export interface UseDictationOptions {
  /** 端侧终稿以文本消息发送。 */
  onSend: (text: string) => void;
  /** toast 语义键，由调用方翻译文案。 */
  onToast: (kind: "not_recognized" | "transcribe_failed") => void;
}

export interface UseDictationResult {
  state: DictationState;
  /**
   * null = 探测中（入口隐藏）；false = 端侧不可用（本单收窄：隐藏入口）；
   * true = 可用。非 Android 恒 false。
   */
  onDeviceReady: boolean | null;
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
  const [onDeviceReady, setOnDeviceReady] = useState<boolean | null>(null);
  const [rms, setRms] = useState<number[]>([]);

  const stateRef = useRef(state);
  stateRef.current = state;
  const readyRef = useRef(onDeviceReady);
  readyRef.current = onDeviceReady;

  const pressAtRef = useRef<number | null>(null);
  const armTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const recordGuardRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const finalizeGuardRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const lastRmsAtRef = useRef(0);
  const optionsRef = useRef(options);
  optionsRef.current = options;

  const clearTimers = useCallback(() => {
    if (armTimerRef.current) clearTimeout(armTimerRef.current);
    if (recordGuardRef.current) clearTimeout(recordGuardRef.current);
    if (finalizeGuardRef.current) clearTimeout(finalizeGuardRef.current);
    armTimerRef.current = null;
    recordGuardRef.current = null;
    finalizeGuardRef.current = null;
  }, []);

  const dispatch = useCallback(
    (event: DictationEvent) => {
      const reduction = reduceDictation(stateRef.current, event);
      stateRef.current = reduction.state;
      setState(reduction.state);
      runCommands(reduction.commands, {
        onSend: (text) => optionsRef.current.onSend(text),
        onToast: (kind) => optionsRef.current.onToast(kind),
      });
      // 副作用定时器跟状态走：进 recording 起 60s 兜底，进 finalizing
      // 起 15s 兜底，回 idle 全清。
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
    },
    [],
  );

  // 模块探测（挂载一次）。模块缺失（非 Android）直接判 false，入口隐藏。
  useEffect(() => {
    const module = getSpeechRecognitionModule();
    if (!module) {
      setOnDeviceReady(false);
      return;
    }
    let alive = true;
    void module.isOnDeviceAvailableAsync().then((availability) => {
      if (alive) setOnDeviceReady(availability.available);
    });
    return () => {
      alive = false;
    };
  }, []);

  // 原生事件 → reducer。
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

  // 卸载：清定时器 + 识别器用完即毁兜底。
  useEffect(() => {
    return () => {
      clearTimers();
      const module = getSpeechRecognitionModule();
      if (module && stateRef.current.phase !== "rest") {
        module.stop().catch(() => {});
      }
    };
  }, [clearTimers]);

  const open = useCallback(() => {
    // 状态机没有 open 事件：听写条的 rest 就是 idleDictation，进入
    // 形态只需清一次本地采样，把可能残留的录音态归零。
    clearTimers();
    stateRef.current = idleDictation;
    setState(idleDictation);
    setRms([]);
  }, [clearTimers]);

  const close = useCallback(() => {
    clearTimers();
    const phase = stateRef.current.phase;
    stateRef.current = idleDictation;
    setState(idleDictation);
    setRms([]);
    if (phase === "recording" || phase === "finalizing") {
      // 录音中退出：丢弃结果（迟到的 final 落在 rest 态被 reducer 忽略）。
      getSpeechRecognitionModule()?.stop().catch(() => {});
    }
  }, [clearTimers]);

  const press = useCallback(() => {
    if (readyRef.current !== true || pressAtRef.current !== null) return;
    const atMs = Date.now();
    pressAtRef.current = atMs;
    dispatch({ type: "press" });
    armTimerRef.current = setTimeout(() => {
      pressAtRef.current = null;
      dispatch({
        type: "beginRecord",
        atMs,
        // 本单收窄：入口只在端侧可用时显示，degraded 恒 false。
        degraded: false,
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
  }, [dispatch]);

  // RMS 事件：不入状态机，节流后供波形条。识别器只在 recording 中吐 RMS。
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

  return { state, onDeviceReady, open, close, press, move, release, rms };
}

function runCommands(
  commands: DictationCommand[],
  handlers: {
    onSend: (text: string) => void;
    onToast: (kind: "not_recognized" | "transcribe_failed") => void;
  },
): void {
  for (const command of commands) {
    switch (command.type) {
      case "startCapture":
        getSpeechRecognitionModule()
          ?.start()
          .catch(() => {
            handlers.onToast("transcribe_failed");
          });
        break;
      case "stopCapture":
      case "cancelCapture":
        // 两条原生路径相同（stop 后识别器 flush 并自毁）；结果是否被
        // 采纳由状态机阶段决定，hook 层不二次裁决。
        getSpeechRecognitionModule()?.stop().catch(() => {});
        break;
      case "requestBackendTranscription":
        // 降级分支：后端转写端点待后续单落地（Leader 裁定中）。当前
        // degraded 恒 false，此命令不可达；保留接缝不删。
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
