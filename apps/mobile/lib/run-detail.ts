/**
 * Pure model for the mobile run-detail screen (RUYI-33) — the layer between
 * the raw `task-messages` endpoint and the RN components. Everything the
 * screen renders or copies flows through here, so the redaction policy
 * (规格 v1.0 加严版：所有展示与复制出口 — 时间线行、展开详情、全文复制、
 * error 文本、input 派生文本 — 必须经 redactSecrets 等价脱敏) is enforced
 * in one testable place instead of at each JSX exit.
 *
 * Platform-agnostic on purpose: node vitest lane, no RN imports. The
 * transcript primitives come from `@multica/core/task-transcript` — the
 * same logic web's AgentTranscriptDialog uses (sunk from views so mobile
 * may import it, per apps/mobile/CLAUDE.md).
 */
import i18n from "i18next";
import {
  buildRunOutcome,
  buildSteps,
  redactSecrets,
  traceEventCopyText,
  traceEventDetail,
  traceEventLabel,
  traceEventSummary,
  unwrapToolOutput,
  type RunOutcome,
  type TimelineItem,
  type TraceDiffLineKind,
  type TraceEvent,
  type TraceStep,
} from "@multica/core/task-transcript";
import type { AgentTask } from "@multica/core/types";
import { failureReasonLabel, isKnownFailureReason } from "./failure-reason-label";

/**
 * Long-body clamp, mirroring web's 8000-character affordance
 * (agent-transcript-dialog.tsx): slice first, then redact the visible slice —
 * the dropped tail can never leak, whatever straddles the boundary.
 */
export const DETAIL_TEXT_CLAMP = 8000;

export interface ClampedBody {
  body: string;
  truncated: boolean;
}

export function redactedDetailBody(
  text: string,
  max: number = DETAIL_TEXT_CLAMP,
): ClampedBody {
  const truncated = text.length > max;
  return { body: redactSecrets(truncated ? text.slice(0, max) : text), truncated };
}

/**
 * Failure/cancel panel body (RUYI-33 review fix). The persisted task error
 * is a display AND copy exit exactly like the timeline rows — it routinely
 * carries connection strings, URLs and tokens — so it must not be rendered
 * raw (`selectable` makes a raw render copyable). Same clamp+redact
 * pipeline as every other exit, named for the panel so call sites stay
 * self-documenting.
 */
export function runErrorText(
  text: string,
  max: number = DETAIL_TEXT_CLAMP,
): ClampedBody {
  return redactedDetailBody(text, max);
}

// ─── Durations ──────────────────────────────────────────────────────────────

/** Step-duration column, verbatim from web's formatStepDuration. */
export function formatStepDuration(ms: number): string {
  if (ms < 1000) return `${(ms / 1000).toFixed(1)}s`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)}s`;
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.round((ms % 60_000) / 1000);
  return `${minutes}m ${String(seconds).padStart(2, "0")}s`;
}

/** Header total — same compact shape as the step column. */
export function formatRunDuration(ms: number): string {
  return formatStepDuration(ms);
}

/** "+MM:SS" offset from the run's first event, or "+H:MM:SS" past an hour. */
export function formatClockOffset(ms: number): string {
  const totalSeconds = Math.max(0, Math.floor(ms / 1000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (minutes < 60) {
    return `+${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
  }
  const hours = Math.floor(minutes / 60);
  return `+${hours}:${String(minutes % 60).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
}

function timeMs(iso: string | null | undefined): number | undefined {
  if (!iso) return undefined;
  const ms = new Date(iso).getTime();
  return Number.isFinite(ms) ? ms : undefined;
}

const RUNNING_STATUSES: readonly AgentTask["status"][] = [
  "queued",
  "dispatched",
  "waiting_local_directory",
  "running",
];

/**
 * Total run duration: started → completed, or started → now while still
 * running. Null when the run has no usable start (never guessed).
 */
export function runDurationMs(
  task: Pick<AgentTask, "started_at" | "completed_at" | "status">,
  now: number = Date.now(),
): number | null {
  const start = timeMs(task.started_at);
  if (start === undefined) return null;
  const end =
    timeMs(task.completed_at) ??
    (RUNNING_STATUSES.includes(task.status) ? now : undefined);
  if (end === undefined) return null;
  return Math.max(0, end - start);
}

// ─── Step views ─────────────────────────────────────────────────────────────

/** Expanded-body view of a tool call's input, redacted field by field. */
export type RunDetailBody =
  | {
      variant: "diff";
      path: string;
      lines: { kind: TraceDiffLineKind; text: string; hidden?: number }[];
    }
  | { variant: "file"; path: string; text: string; lineCount: number }
  | {
      variant: "patch";
      truncated: boolean;
      files: {
        path: string;
        changeKind?: string;
        movePath?: string;
        truncated?: boolean;
        body:
          | {
              kind: "diff";
              lines: { kind: TraceDiffLineKind; text: string; hidden?: number }[];
            }
          | { kind: "file"; text: string; lineCount: number }
          | { kind: "none" };
      }[];
    }
  | { variant: "text"; text: string };

export interface RunStepViewBase {
  /** Stable row key — the originating event's seq. */
  key: string;
  kind: "call" | "text" | "thinking" | "error";
  /** Provider-native label (tool name / "Agent" / "Thinking" / raw type). */
  label: string;
  /** One-line collapsed summary — redacted (input-derived text included). */
  summary: string;
  /** "+MM:SS" offset from the run's first event, when timestamps allow. */
  clockLabel?: string;
}

export interface RunCallStepView extends RunStepViewBase {
  kind: "call";
  /** Right-column call duration, when both sides carry timestamps. */
  durationLabel?: string;
  /** Expanded input detail (diff / file / patch / JSON fallback), redacted. */
  detail?: RunDetailBody;
  /** Expanded paired-result body, unwrapped + clamped + redacted. */
  result?: ClampedBody;
}

export interface RunMessageStepView extends RunStepViewBase {
  kind: "text" | "thinking" | "error";
  /** Full content, clamped + redacted. */
  body: ClampedBody;
}

export type RunStepView = RunCallStepView | RunMessageStepView;

export type DiffLineView = {
  kind: TraceDiffLineKind;
  text: string;
  hidden?: number;
};

function diffLinesView(
  lines: readonly { kind: TraceDiffLineKind; text: string; hidden?: number }[],
): DiffLineView[] {
  return lines.map((line) => ({ ...line, text: redactSecrets(line.text) }));
}

/** Expanded input detail for one tool_use event, redacted end to end. */
function callDetailView(event: TraceEvent): RunDetailBody | undefined {
  const detail = traceEventDetail(event);
  switch (detail.kind) {
    case "diff":
      return {
        variant: "diff",
        path: redactSecrets(detail.path),
        lines: diffLinesView(detail.lines),
      };
    case "file":
      return {
        variant: "file",
        path: redactSecrets(detail.path),
        text: redactSecrets(detail.text),
        lineCount: detail.lineCount,
      };
    case "patch":
      return {
        variant: "patch",
        truncated: detail.truncated,
        files: detail.files.map((file) => {
          let body: Extract<RunDetailBody, { variant: "patch" }>["files"][number]["body"];
          switch (file.body.kind) {
            case "diff":
              body = { kind: "diff", lines: diffLinesView(file.body.lines) };
              break;
            case "file":
              body = {
                kind: "file",
                text: redactSecrets(file.body.text),
                lineCount: file.body.lineCount,
              };
              break;
            default:
              body = { kind: "none" };
          }
          return {
            path: redactSecrets(file.path),
            changeKind: file.changeKind,
            movePath: file.movePath ? redactSecrets(file.movePath) : undefined,
            truncated: file.truncated,
            body,
          };
        }),
      };
    case "text":
      return detail.text.length > 0
        ? { variant: "text", text: redactedDetailBody(detail.text).body }
        : undefined;
  }
}

function offsetLabel(
  at: string | undefined,
  startMs: number | undefined,
): string | undefined {
  const ms = timeMs(at);
  if (ms === undefined || startMs === undefined) return undefined;
  return formatClockOffset(ms - startMs);
}

/**
 * Build the redacted, render-ready step list from raw task messages.
 *
 * `runStartMs` (when provided) anchors the per-step clock even if the first
 * event lacks a timestamp; otherwise the earliest event timestamp anchors it.
 */
export function buildRunStepViews(
  items: TimelineItem[],
  runStartMs?: number,
): RunStepView[] {
  const steps: TraceStep[] = buildSteps(items);
  const anchor =
    runStartMs ??
    steps
      .flatMap((s): number[] => {
        const ms = timeMs(s.startedAt);
        return ms === undefined ? [] : [ms];
      })
      .reduce<number | undefined>(
        (min, ms) => (min === undefined || ms < min ? ms : min),
        undefined,
      );

  return steps.map((step) => {
    if (step.kind === "call") {
      // A paired step shows the call; an orphan result (stream reconnected
      // mid-flight) still shows its output — never dropped.
      const event = step.call ?? step.result;
      const detail = event ? callDetailView(event) : undefined;
      const output = step.result?.output ?? event?.output;
      const result = output ? redactedDetailBody(unwrapToolOutput(output)) : undefined;
      const durationLabel =
        step.durationMs !== undefined ? formatStepDuration(step.durationMs) : undefined;
      return {
        key: String(step.seq),
        kind: "call" as const,
        tool: step.tool,
        label: event ? traceEventLabel(event) : step.tool || "Tool",
        summary: redactSecrets(event ? traceEventSummary(event) : ""),
        clockLabel: offsetLabel(step.startedAt, anchor) ?? durationLabel,
        durationLabel,
        detail,
        result,
      };
    }

    return {
      key: String(step.seq),
      kind: step.kind,
      label: traceEventLabel(step.item),
      summary: redactSecrets(traceEventSummary(step.item)),
      clockLabel: offsetLabel(step.startedAt, anchor),
      body: redactedDetailBody(step.item.content ?? ""),
    };
  });
}

// ─── Outcome summary ────────────────────────────────────────────────────────

/** RunOutcome with the touched paths redacted (they derive from tool input). */
export function runOutcomeSummary(items: TimelineItem[]): RunOutcome | null {
  const outcome = buildRunOutcome(buildSteps(items));
  if (!outcome) return null;
  return { ...outcome, paths: outcome.paths.map((p) => redactSecrets(p)) };
}

// ─── Copy exits ─────────────────────────────────────────────────────────────

/** One step's full copy text — body untruncated, secrets redacted. */
export function runStepCopyText(item: TimelineItem): string {
  return redactSecrets(traceEventCopyText(item));
}

/** Copy-all — per-event copy text joined, redaction applied throughout. */
export function runCopyAllText(items: TimelineItem[]): string {
  return items
    .map((event) => redactSecrets(traceEventCopyText(event)))
    .join("\n\n");
}

// ─── Header status ──────────────────────────────────────────────────────────

const STATUS_LABEL_EN: Record<AgentTask["status"], string> = {
  queued: "Queued",
  dispatched: "Starting",
  waiting_local_directory: "Waiting for directory",
  running: "Running",
  completed: "Done",
  failed: "Failed",
  cancelled: "Cancelled",
};

/** Localized status word, same key family the runs list badge uses. */
export function runStatusLabel(status: AgentTask["status"]): string {
  return i18n.t(`issues:mobile.run_status.${status}`, STATUS_LABEL_EN[status]);
}

/**
 * Cancel-reason classification for the detail header — web's
 * `cancelReasonLabel` with mobile's no-leak fallback policy: a recognised
 * reason shows its label; an unrecognised one (which failureReasonLabel can
 * only render as the generic "Failed") falls through to the system-cancel
 * wording when the run carries persisted error text, and to nothing otherwise.
 */
export function cancelReasonLabel(
  task: Pick<AgentTask, "status" | "error" | "failure_reason">,
): string | null {
  if (task.status !== "cancelled") return null;
  if (isKnownFailureReason(task.failure_reason)) {
    return failureReasonLabel(task.failure_reason);
  }
  if (task.error) {
    return i18n.t(
      "issues:mobile.run_detail.cancelled_by_system",
      "Cancelled by the system",
    );
  }
  return null;
}
