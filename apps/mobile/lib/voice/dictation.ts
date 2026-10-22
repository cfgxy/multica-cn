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
 * 非空（sendText 以文本发送）。降级转写失败 toast 后回 rest，重新按住
 * 即重试（设计板 1 语义：失败 toast 重试；录音不跨次保留）。
 */

/** 按住此时长才起录，轻点不算。 */
export const RECORD_ARM_MS = 120;
/** 上滑超过该距离（dp）进入取消态。 */
export const CANCEL_ARM_DP = 48;
/** 短于该时长的录音视为误触，不进入识别。 */
export const MIN_RECORD_MS = 500;

/**
 * 听写入口模式（RUYI-710 阶段 2 放开降级）。probe 结果 + 转写通道是否
 * 接好 → 入口呈现：
 *
 *   - probing        探测未回来，入口隐藏
 *   - ondevice       端侧识别可用（与降级接入无关，行为回归面）
 *   - degraded       端侧不可用但录音上传转写可行：api_level（API<31 无
 *                    端侧识别器）/ module_missing（识别服务缺失）——
 *                    「识别失败的设备」即此设备级语义，录音交后端转写
 *   - hidden         不可用：非 Android（platform，采集模块同样缺失）/
 *                    权限被拒（两条路径都要麦克风）/ 其余未知原因 /
 *                    转写通道未接（收窄行为保持）
 */
export type DictationEntryMode =
  | "probing"
  | "ondevice"
  | "degraded"
  | "hidden";

export function resolveDictationEntry(
  availability: { available: boolean; reason: string } | null,
  transcribeWired: boolean,
): DictationEntryMode {
  if (!availability) return "probing";
  if (availability.available) return "ondevice";
  if (!transcribeWired) return "hidden";
  switch (availability.reason) {
    case "api_level":
    case "module_missing":
      return "degraded";
    default:
      // permission（无麦克风两态皆不可行）、platform（非 Android，采集
      // 模块缺失）与未来未知原因一律收敛隐藏。
      return "hidden";
  }
}

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
  /** A final result observed while held, settled only after release. */
  pendingFinal: string | null;
  /** A recognizer error observed while held, settled only after release. */
  pendingError: "empty" | "failed" | null;
  startedAtMs: number | null;
}

export const idleDictation: DictationState = {
  phase: "rest",
  degraded: false,
  cancelArmed: false,
  preview: "",
  pendingFinal: null,
  pendingError: null,
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
  /** 降级分支：上传录音请求后端转写（POST /api/workspaces/{id}/voice-transcriptions）。 */
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
          pendingFinal: null,
          pendingError: null,
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
      if (state.pendingFinal !== null) {
        const text = state.pendingFinal.trim();
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
      if (state.pendingError) {
        return {
          state: idleDictation,
          commands: [
            { type: "cancelCapture" },
            {
              type: "toast",
              kind: state.pendingError === "empty" ? "not_recognized" : "transcribe_failed",
            },
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
      if (state.phase === "recording") {
        return {
          state: { ...state, pendingFinal: event.text, pendingError: null },
          commands: [],
        };
      }
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
      if (state.phase === "recording") {
        return {
          state: { ...state, pendingError: event.kind },
          commands: [],
        };
      }
      if (state.phase !== "finalizing" && state.phase !== "transcribing") {
        return { state, commands: [] };
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
