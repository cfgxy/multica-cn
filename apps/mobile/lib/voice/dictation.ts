/**
 * 听写长条状态机（RUYI-694 D3，设计板1 形态C + 板6 交互标注）。纯 reducer，
 * 零 RN 依赖 —— UI hook 驱动原生模块并把事件喂进来，命令列表回给 hook 执行；
 * vitest 通道钉住五态与回退门禁。
 *
 * 五态与设计稿的对应：
 *
 *   rest          按住 说话 提示
 *   pressing      按住未满 120ms（轻点不起录，防误触）
 *   recording     红点计时 + 波形 + 端侧转写预览；`cancelArmed` 为 true 即
 *                 设计稿的取消态（整条变红）；`degraded` 为 true 时无预览、
 *                 显「松开后转写」角标
 *   finalizing    松开后等端侧终稿（partial 已上屏）
 *   transcribing  降级分支：录音交后端转写，条内「转写中…」
 *
 * 出口三支：取消（废弃）、过短/空转写（toast「未识别到语音」回 rest）、
 * 非空（sendText 以文本发送）。降级转写失败 toast 后回 rest（录音不跨次
 * 保留；「保留录音重试」留待降级链路落地时一并裁定）。
 */

/** 按住此时长才起录，轻点不算。 */
export const RECORD_ARM_MS = 120;
/** 上滑超过该距离（dp）进入取消态。 */
export const CANCEL_ARM_DP = 48;
/** 短于该时长的录音视为误触，不进入识别。 */
export const MIN_RECORD_MS = 500;

export type DictationPhase =
  | "rest"
  | "pressing"
  | "recording"
  | "finalizing"
  | "transcribing";

export interface DictationState {
  phase: DictationPhase;
  /** 端侧识别不可用：无实时预览，松开后交后端转写。 */
  degraded: boolean;
  /** 上滑已过阈值 —— 整条变红的取消态。 */
  cancelArmed: boolean;
  /** 端侧 partial 预览文本。 */
  preview: string;
  startedAtMs: number | null;
}

export const idleDictation: DictationState = {
  phase: "rest",
  degraded: false,
  cancelArmed: false,
  preview: "",
  startedAtMs: null,
};

export type DictationEvent =
  | { type: "press" }
  /** 按住满 120ms 后由 UI 定时器触发；`atMs` 为按下的绝对时刻，`degraded`
   *  为 hook 按下前探测到的端侧可用性。 */
  | { type: "beginRecord"; atMs: number; degraded: boolean }
  /** 手指位移：向上为正（dp）。 */
  | { type: "move"; upDp: number }
  /** 松开；`heldMs` 为本次按住总时长。 */
  | { type: "release"; heldMs: number }
  | { type: "partial"; text: string }
  /** 端侧终稿（stop 后识别器 flush，可能为空串）。 */
  | { type: "final"; text: string }
  /** 降级分支：后端转写完成。 */
  | { type: "transcribed"; text: string }
  | { type: "recognizerError"; kind: "empty" | "failed" };

export type DictationCommand =
  | { type: "startCapture" }
  | { type: "stopCapture" }
  | { type: "cancelCapture" }
  /** 降级分支：上传录音请求后端转写（端点待后续单落地）。 */
  | { type: "requestBackendTranscription" }
  | { type: "sendText"; text: string }
  | { type: "toast"; kind: "not_recognized" | "transcribe_failed" };

export interface DictationReduction {
  state: DictationState;
  commands: DictationCommand[];
}

export function reduceDictation(
  state: DictationState,
  event: DictationEvent,
): DictationReduction {
  switch (event.type) {
    case "press": {
      if (state.phase !== "rest") return { state, commands: [] };
      return { state: { ...state, phase: "pressing" }, commands: [] };
    }

    case "beginRecord": {
      if (state.phase !== "pressing") return { state, commands: [] };
      return {
        state: {
          ...state,
          phase: "recording",
          degraded: event.degraded,
          startedAtMs: event.atMs,
          cancelArmed: false,
          preview: "",
        },
        commands: [{ type: "startCapture" }],
      };
    }

    case "move": {
      if (state.phase !== "recording") return { state, commands: [] };
      const cancelArmed = event.upDp > CANCEL_ARM_DP;
      if (cancelArmed === state.cancelArmed) return { state, commands: [] };
      return { state: { ...state, cancelArmed }, commands: [] };
    }

    case "release": {
      if (state.phase === "pressing") {
        // 未满 120ms 的轻点：什么都没开始，安静回 rest。
        return { state: idleDictation, commands: [] };
      }
      if (state.phase !== "recording") return { state, commands: [] };
      if (state.cancelArmed) {
        return {
          state: idleDictation,
          commands: [{ type: "cancelCapture" }],
        };
      }
      if (event.heldMs < MIN_RECORD_MS) {
        return {
          state: idleDictation,
          commands: [
            { type: "cancelCapture" },
            { type: "toast", kind: "not_recognized" },
          ],
        };
      }
      if (state.degraded) {
        return {
          state: { ...state, phase: "transcribing" },
          commands: [
            { type: "stopCapture" },
            { type: "requestBackendTranscription" },
          ],
        };
      }
      return {
        state: { ...state, phase: "finalizing" },
        commands: [{ type: "stopCapture" }],
      };
    }

    case "partial": {
      if (state.phase !== "recording") return { state, commands: [] };
      return { state: { ...state, preview: event.text }, commands: [] };
    }

    case "final": {
      if (state.phase !== "finalizing") return { state, commands: [] };
      const text = event.text.trim();
      if (!text) {
        return {
          state: idleDictation,
          commands: [{ type: "toast", kind: "not_recognized" }],
        };
      }
      return {
        state: idleDictation,
        commands: [{ type: "sendText", text }],
      };
    }

    case "transcribed": {
      if (state.phase !== "transcribing") return { state, commands: [] };
      const text = event.text.trim();
      if (!text) {
        return {
          state: idleDictation,
          commands: [{ type: "toast", kind: "not_recognized" }],
        };
      }
      return {
        state: idleDictation,
        commands: [{ type: "sendText", text }],
      };
    }

    case "recognizerError": {
      if (state.phase !== "finalizing" && state.phase !== "transcribing") {
        // 录制中识别器崩了：停止并提示，与 finalizing 错误同出口。
        if (state.phase !== "recording") return { state, commands: [] };
        return {
          state: idleDictation,
          commands: [
            { type: "cancelCapture" },
            {
              type: "toast",
              kind: event.kind === "empty" ? "not_recognized" : "transcribe_failed",
            },
          ],
        };
      }
      return {
        state: idleDictation,
        commands: [
          {
            type: "toast",
            kind: event.kind === "empty" ? "not_recognized" : "transcribe_failed",
          },
        ],
      };
    }
  }
}
