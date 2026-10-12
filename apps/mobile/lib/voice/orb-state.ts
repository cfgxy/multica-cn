/**
 * 全屏语音球六态状态机（RUYI-694 板3/板4）：纯函数、零 RN 依赖。
 *
 * 六态：connecting → listening ⇄ userSpeaking → thinking → speaking，
 * speaking 被插话经 interrupted（600ms 瞬态）回 listening。加上
 * disconnected（会话中断线，展示「重连/挂断」）与 failed（闸门拒绝/
 * 拨号失败，降级卡片）两个异常态，共八个渲染相位。
 *
 * 时序近似（协议面限制，交付回报注明）：Gemini Live 没有显式的
 * 「用户说完」端点事件，user 侧以 500ms 无新 delta 判静默、assistant
 * 侧以 1200ms 无新 delta 且音频播完判回合结束——计时器在
 * `use-voice-session.ts`，本文件只认显式事件。
 */

export type OrbPhase =
  | "connecting"
  | "listening"
  | "userSpeaking"
  | "thinking"
  | "speaking"
  | "interrupted"
  | "disconnected"
  | "failed";

export type OrbEvent =
  | { type: "sessionState"; state: "idle" | "connecting" | "live" | "ended" | "failed" }
  /** 首个 user 转写 delta（VAD 命中）。 */
  | { type: "userSpeechStarted" }
  /** user delta 静默 500ms。 */
  | { type: "userSpeechEnded" }
  /** 首个 assistant delta 或下行音频。 */
  | { type: "assistantStarted" }
  /** assistant 回合结束（静默判定）。 */
  | { type: "assistantEnded" }
  /** serverContent.interrupted —— barge-in。 */
  | { type: "interrupted" }
  /** 瞬态 600ms 结束。 */
  | { type: "interruptedSettled" }
  /** 传输断开。 */
  | { type: "connectionLost" }
  /** 手动重连成功开始（重新走 connecting）。 */
  | { type: "reconnected" }
  /** 闸门拒绝 / 拨号失败。 */
  | { type: "degraded" };

export function reduceOrbPhase(phase: OrbPhase, event: OrbEvent): OrbPhase {
  switch (event.type) {
    case "sessionState":
      if (event.state === "live") {
        return phase === "failed" ? phase : "listening";
      }
      if (event.state === "connecting" && phase !== "failed") {
        return "connecting";
      }
      // idle/ended/failed 不改相位：挂断由 overlay 卸载处理。
      return phase;

    case "userSpeechStarted":
      if (phase === "listening") return "userSpeaking";
      return phase;

    case "userSpeechEnded":
      if (phase === "userSpeaking") return "thinking";
      return phase;

    case "assistantStarted":
      if (phase === "listening" || phase === "thinking" || phase === "userSpeaking") {
        return "speaking";
      }
      return phase;

    case "assistantEnded":
      if (phase === "speaking") return "listening";
      return phase;

    case "interrupted":
      if (
        phase === "speaking" ||
        phase === "thinking" ||
        phase === "userSpeaking" ||
        phase === "listening"
      ) {
        return "interrupted";
      }
      return phase;

    case "interruptedSettled":
      if (phase === "interrupted") return "listening";
      return phase;

    case "connectionLost":
      if (phase !== "failed" && phase !== "disconnected") return "disconnected";
      return phase;

    case "reconnected":
      if (phase === "disconnected") return "connecting";
      return phase;

    case "degraded":
      return "failed";
  }
}

/**
 * 迷你球（58dp）的相位归并（板4）：尺寸下不区分细相位——
 * UserSpeaking 与 Interrupted 并入聆听，其余一一对应；failed 归
 * disconnected（迷你球只表达「还活着吗」，失败细节展开后看）。
 */
export type MiniPhase = "connecting" | "listening" | "thinking" | "speaking" | "disconnected";

export function miniPhase(phase: OrbPhase): MiniPhase {
  switch (phase) {
    case "connecting":
      return "connecting";
    case "userSpeaking":
    case "listening":
    case "interrupted":
      return "listening";
    case "thinking":
      return "thinking";
    case "speaking":
      return "speaking";
    case "disconnected":
    case "failed":
      return "disconnected";
  }
}

/**
 * 相位 → 球体色板（沉浸态固定深色底上的取值，不随全局主题）。设计
 * token：品牌蓝取 theme.primary 语义值，思考紫 #8B5CF6（板3）。
 */
export interface OrbPalette {
  /** 球体主色。 */
  core: string;
  /** 高光/涟漪等衍生色。 */
  glow: string;
  /** 状态文案色（浅白系，除 failed/disconnected 灰化）。 */
  label: string;
}

const BLUE = "#3B82F6";
const BLUE_SOFT = "#93A6C4";
const PURPLE = "#8B5CF6";
const GRAY = "#6B7280";
const RED = "#EF4444";

export function orbPalette(phase: OrbPhase): OrbPalette {
  switch (phase) {
    case "connecting":
      return { core: BLUE_SOFT, glow: BLUE_SOFT, label: "rgba(255,255,255,0.78)" };
    case "listening":
    case "userSpeaking":
      return { core: BLUE, glow: BLUE, label: "rgba(255,255,255,0.78)" };
    case "thinking":
      return { core: PURPLE, glow: PURPLE, label: "rgba(255,255,255,0.78)" };
    case "speaking":
      return { core: BLUE, glow: BLUE, label: "rgba(255,255,255,0.78)" };
    case "interrupted":
      return { core: BLUE, glow: RED, label: "rgba(255,255,255,0.78)" };
    case "disconnected":
      return { core: GRAY, glow: GRAY, label: "rgba(255,255,255,0.55)" };
    case "failed":
      return { core: GRAY, glow: GRAY, label: "rgba(255,255,255,0.55)" };
  }
}

/** 状态文案 key（voice ns），overlay 用 t() 解析。 */
export function orbStatusLabelKey(phase: OrbPhase): string {
  switch (phase) {
    case "connecting":
      return "voice:orb.connecting";
    case "listening":
      return "voice:orb.listening";
    case "userSpeaking":
      return "voice:orb.user_speaking";
    case "thinking":
      return "voice:orb.thinking";
    case "speaking":
      return "voice:orb.speaking";
    case "interrupted":
      return "voice:orb.interrupted";
    case "disconnected":
      return "voice:orb.disconnected";
    case "failed":
      return "voice:orb.failed";
  }
}
