/**
 * 板1 输入区形态矩阵（RUYI-694 定稿设计稿）：纯函数、零 RN 依赖。
 *
 * 四形态：
 *   - pill        现状灰化胶囊（disabled 链，R1 语义不变）
 *   - dictation   听写长条整条替换（形态C）
 *   - voice       空草稿 + 语音可用：[+][占位][🎤][🔵]
 *   - send        有草稿：[+][文字][⬆️发送]（🎤🔵 让位——声波入口消失
 *                 等于语音关闭，清空草稿即恢复）；无草稿且语音不可用时
 *                 发送钮灰化，同样无 🎤🔵
 *   - stop        发送中：发送位换 Stop（现状逻辑保留，不变）
 *
 * Stop 优先级沿用 MessageComposer 的发送位规则：Stop > 语音 > 发送。
 */
import type { VoiceAvailabilityOutcome } from "./availability";

export type VoiceGateVerdict = "available" | "hidden" | "resolving";

/**
 * 语音入口的三值判定。resolving（链路数据未就绪）与 hidden（回退门禁
 * 判负）都收敛为不显示 🎤🔵，但 verdict 保留区分供测试与埋点。
 */
export function voiceGateVerdict(
  gate:
    | { availability: VoiceAvailabilityOutcome | null; resolving: boolean }
    | null
    | undefined,
): VoiceGateVerdict {
  if (!gate || gate.resolving || gate.availability === null) return "resolving";
  return gate.availability.available ? "available" : "hidden";
}

export type InputRightSlot = "stop" | "voice" | "send" | "muted-send";

export interface InputShapeInput {
  /** 现状 disabled 链（no agent / revoked / archived / runtime missing）。 */
  disabled: boolean;
  /** 本会话有 in-flight 任务。 */
  sending: boolean;
  /** queued 任务不给 Stop（现状语义）。 */
  allowStop: boolean;
  /** text.trim() 非空。 */
  hasDraft: boolean;
  /** 听写长条已展开。 */
  dictationOpen: boolean;
  voice: VoiceGateVerdict;
}

export interface InputShape {
  /** 整条替换为听写长条。 */
  dictation: boolean;
  /** 现状灰 pill（disabled 链）。 */
  pill: boolean;
  rightSlot: InputRightSlot;
}

const DICTATION: InputShape = { dictation: true, pill: false, rightSlot: "send" };
const PILL: InputShape = { dictation: false, pill: true, rightSlot: "muted-send" };

export function resolveInputShape(input: InputShapeInput): InputShape {
  // 已展开的听写条不因 sending 翻回 bar——录音中被任务完成事件闪屏
  // 比多显示一瞬 Stop 更糟；听写条自身不提供发送路径（松开即发送）。
  if (input.dictationOpen && !input.disabled) return DICTATION;
  if (input.disabled) return PILL;
  if (input.sending && input.allowStop) {
    return { dictation: false, pill: false, rightSlot: "stop" };
  }
  if (!input.hasDraft && input.voice === "available") {
    return { dictation: false, pill: false, rightSlot: "voice" };
  }
  return {
    dictation: false,
    pill: false,
    rightSlot: input.hasDraft ? "send" : "muted-send",
  };
}
