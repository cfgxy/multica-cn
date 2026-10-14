/**
 * 语音会话运行时 hook（RUYI-694 板3/板4/板6）：把 MobileVoiceSession
 * 的回调流映射成 orb 六态状态机事件、双向转写条目与生命周期护栏。
 *
 * 护栏（板6 边界）：listening 空闲 5min 自动挂断；切后台超 10s 回前台
 * 自动挂断（系统音频焦点语义）；断线进 disconnected 供「重连/挂断」；
 * interrupted 600ms 瞬态计时；user/assistant 回合静默判定（500ms /
 * 1200ms，协议无显式端点事件的近似）。
 */
import { useCallback, useEffect, useRef, useState, useReducer } from "react";
import { AppState } from "react-native";
import {
  reduceOrbPhase,
  type OrbEvent,
  type OrbPhase,
} from "./orb-state";
import { MobileVoiceSession } from "./session";
import type { VoiceFailure } from "./failure-text";

export interface TranscriptEntry {
  id: number;
  role: "user" | "assistant";
  text: string;
  /** 流式中的最后一条（带光标渲染）。 */
  streaming: boolean;
}

export type AutoEndReason = "idle_timeout" | "background_timeout";

const USER_SILENCE_MS = 500;
const ASSISTANT_SILENCE_MS = 1200;
const INTERRUPTED_SETTLE_MS = 600;
const IDLE_TIMEOUT_MS = 5 * 60_000;
const BACKGROUND_TIMEOUT_MS = 10_000;
const IDLE_TICK_MS = 15_000;

export interface VoiceSessionRuntime {
  orb: OrbPhase;
  entries: TranscriptEntry[];
  muted: boolean;
  setMuted: (muted: boolean) => void;
  failure: VoiceFailure | null;
  /** disconnected 态的重连。 */
  reconnect: () => void;
  /** 显式挂断。 */
  hangUp: () => void;
  /** 自动挂断原因（挂断即关闭前 overlay 读一次）。 */
  autoEnd: AutoEndReason | null;
}

export function useVoiceSessionRuntime(args: {
  agentId: string | null;
  workspaceSlug: string;
  onUserTurn?: (text: string) => void;
  /** 会话因任何原因终止（自动挂断含内）——overlay 借此收尾。 */
  onEnded?: (autoEnd: AutoEndReason | null) => void;
}): VoiceSessionRuntime {
  const { agentId, workspaceSlug, onUserTurn, onEnded } = args;

  const [orb, dispatchOrb] = useOrbReducer();
  const [entries, setEntries] = useState<TranscriptEntry[]>([]);
  const [streamingUser, setStreamingUser] = useState("");
  const [streamingAssistant, setStreamingAssistant] = useState("");
  const [muted, setMutedState] = useState(false);
  const [failure, setFailure] = useState<VoiceFailure | null>(null);
  const [autoEnd, setAutoEnd] = useState<AutoEndReason | null>(null);

  const sessionRef = useRef<MobileVoiceSession | null>(null);
  const orbRef = useRef<OrbPhase>(orb);
  orbRef.current = orb;
  const mutedRef = useRef(false);
  const entryIdRef = useRef(0);
  const onUserTurnRef = useRef(onUserTurn);
  onUserTurnRef.current = onUserTurn;
  const onEndedRef = useRef(onEnded);
  onEndedRef.current = onEnded;

  const userSilenceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const assistantSilenceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const interruptedRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const lastActivityRef = useRef(0);
  const closedRef = useRef(false);
  /** 主动/自动挂断置位；未置位的 controller onEnded 即意外断线。 */
  const hangingUpRef = useRef(false);
  const autoEndRef = useRef<AutoEndReason | null>(null);

  const dispatch = useCallback((event: OrbEvent) => {
    dispatchOrb(event);
  }, []);

  const touch = useCallback(() => {
    lastActivityRef.current = Date.now();
  }, []);

  const clearTimers = useCallback(() => {
    if (userSilenceRef.current) clearTimeout(userSilenceRef.current);
    if (assistantSilenceRef.current) clearTimeout(assistantSilenceRef.current);
    if (interruptedRef.current) clearTimeout(interruptedRef.current);
    userSilenceRef.current = null;
    assistantSilenceRef.current = null;
    interruptedRef.current = null;
  }, []);

  const finishSession = useCallback(
    (reason: AutoEndReason | null) => {
      if (closedRef.current) return;
      closedRef.current = true;
      hangingUpRef.current = true;
      clearTimers();
      autoEndRef.current = reason;
      setAutoEnd(reason);
      sessionRef.current?.end();
      sessionRef.current = null;
      onEndedRef.current?.(reason);
    },
    [clearTimers],
  );

  const startSession = useCallback(() => {
    if (!agentId || closedRef.current) return;
    const session = new MobileVoiceSession(agentId, workspaceSlug, {
      onStateChange: (state) => {
        dispatch({ type: "sessionState", state });
        if (state === "live") touch();
      },
      onUserTranscriptDelta: (delta) => {
        touch();
        dispatch({ type: "userSpeechStarted" });
        setStreamingUser((prev) => prev + delta);
        if (userSilenceRef.current) clearTimeout(userSilenceRef.current);
        userSilenceRef.current = setTimeout(() => {
          dispatch({ type: "userSpeechEnded" });
        }, USER_SILENCE_MS);
      },
      onUserTurn: (text) => {
        touch();
        setStreamingUser("");
        setEntries((prev) => [
          ...prev,
          { id: ++entryIdRef.current, role: "user", text, streaming: false },
        ]);
        onUserTurnRef.current?.(text);
      },
      onAssistantTranscriptDelta: (delta) => {
        touch();
        dispatch({ type: "assistantStarted" });
        setStreamingAssistant((prev) => prev + delta);
        if (assistantSilenceRef.current) clearTimeout(assistantSilenceRef.current);
        assistantSilenceRef.current = setTimeout(() => {
          // 回合结束近似：delta 静默即视为播完（音频侧无声级回调）。
          dispatch({ type: "assistantEnded" });
          setStreamingAssistant((current) => {
            if (current) {
              setEntries((prev) => [
                ...prev,
                {
                  id: ++entryIdRef.current,
                  role: "assistant",
                  text: current,
                  streaming: false,
                },
              ]);
            }
            return "";
          });
        }, ASSISTANT_SILENCE_MS);
      },
      onInterrupted: () => {
        touch();
        dispatch({ type: "interrupted" });
        // 打断丢弃未定稿的用户流与未播完的助手流（ChatGPT 同语义）。
        setStreamingUser("");
        setStreamingAssistant("");
        if (interruptedRef.current) clearTimeout(interruptedRef.current);
        interruptedRef.current = setTimeout(() => {
          dispatch({ type: "interruptedSettled" });
        }, INTERRUPTED_SETTLE_MS);
      },
      onDegrade: (rejection) => {
        setFailure({ kind: "rejection", rejection });
        dispatch({ type: "degraded" });
      },
      onEnded: () => {
        if (closedRef.current) return;
        // 意外断线（服务端重启、网络切换）：不自动收尾，进 disconnected
        // 供「重连/挂断」；重连失败由 start().catch 落 failed 终态。
        sessionRef.current = null;
        dispatch({ type: "connectionLost" });
      },
    });
    sessionRef.current = session;
    session.setMuted(mutedRef.current);
    session.start().catch((error: Error) => {
      if (closedRef.current) return;
      if (error.message === "VOICE_PERMISSION_DENIED") {
        setFailure({ kind: "permission" });
      } else {
        // 连接层失败（网络断、服务端 5xx）：无 wire code，走通用降级
        // 文案；带 code 的门禁拒绝已由 onDegrade 记录精确文案。
        setFailure({ kind: "rejection", rejection: null });
      }
      dispatch({ type: "degraded" });
    });
  }, [agentId, workspaceSlug, dispatch, touch, finishSession]);

  // 挂载即开会话；卸载/agentId 变化即挂断。
  useEffect(() => {
    if (!agentId) return;
    closedRef.current = false;
    autoEndRef.current = null;
    setEntries([]);
    setStreamingUser("");
    setStreamingAssistant("");
    setFailure(null);
    setAutoEnd(null);
    touch();
    startSession();
    return () => {
      sessionRef.current?.end();
      sessionRef.current = null;
      clearTimers();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agentId, workspaceSlug]);

  // 空闲 5min 自动挂断（Listening 空闲语义：任何活动都刷新心跳）。
  useEffect(() => {
    const timer = setInterval(() => {
      if (!sessionRef.current) return;
      const idleMs = Date.now() - lastActivityRef.current;
      if (idleMs >= IDLE_TIMEOUT_MS) {
        finishSession("idle_timeout");
      }
    }, IDLE_TICK_MS);
    return () => clearInterval(timer);
  }, [finishSession]);

  // 切后台：回前台时超 10s 自动挂断（板6 来电/切后台语义）。
  useEffect(() => {
    let backgroundedAt = 0;
    const sub = AppState.addEventListener("change", (state) => {
      if (state === "background") {
        backgroundedAt = Date.now();
        return;
      }
      if (state === "active" && backgroundedAt && sessionRef.current) {
        const awayMs = Date.now() - backgroundedAt;
        backgroundedAt = 0;
        if (awayMs >= BACKGROUND_TIMEOUT_MS) {
          finishSession("background_timeout");
        }
      }
    });
    return () => sub.remove();
  }, [finishSession]);

  const setMuted = useCallback((next: boolean) => {
    mutedRef.current = next;
    setMutedState(next);
    sessionRef.current?.setMuted(next);
  }, []);

  const reconnect = useCallback(() => {
    if (orbRef.current !== "disconnected") return;
    dispatch({ type: "reconnected" });
    startSession();
  }, [dispatch, startSession]);

  const hangUp = useCallback(() => {
    finishSession(null);
  }, [finishSession]);

  // 流式条目拼装：streaming 中的内容作为最后一条 streaming 条目渲染。
  const merged: TranscriptEntry[] = [
    ...entries,
    ...(streamingUser
      ? [
          {
            id: -1,
            role: "user" as const,
            text: streamingUser,
            streaming: true,
          },
        ]
      : []),
    ...(streamingAssistant
      ? [
          {
            id: -2,
            role: "assistant" as const,
            text: streamingAssistant,
            streaming: true,
          },
        ]
      : []),
  ];

  return {
    orb,
    entries: merged,
    muted,
    setMuted,
    failure,
    reconnect,
    hangUp,
    autoEnd,
  };
}

function useOrbReducer() {
  return useReducer(
    (phase: OrbPhase, event: OrbEvent) => reduceOrbPhase(phase, event),
    "connecting" as OrbPhase,
  );
}
