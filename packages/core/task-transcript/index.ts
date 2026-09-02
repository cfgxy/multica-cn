/**
 * Pure, platform-agnostic transcript logic shared by web/desktop
 * (`@multica/views/common/task-transcript` re-exports these) and mobile
 * (imports `@multica/core/task-transcript` directly, per the apps/mobile
 * CLAUDE.md import allowlist: pure functions from core only).
 *
 * Everything here must stay free of React, DOM, and CSS — the mobile
 * Metro bundle pulls these files into RN. UI concerns live in views.
 */
export { redactSecrets } from "./redact";
export {
  appendTimelineItem,
  buildTimeline,
  coalesceTimelineItems,
  type TimelineItem,
} from "./build-timeline";
export {
  MIN_GROUP_SIZE,
  buildLanes,
  buildSteps,
  groupSteps,
  isCallStep,
  isGroupRow,
  isMessageStep,
  laneSegmentPosition,
  rowCalls,
  shouldShowTimeline,
  timelineTicks,
  toolKindTotals,
  type LaneSegment,
  type LaneSegmentKind,
  type ToolKindTotals,
  type TraceCallStep,
  type TraceGroupRow,
  type TraceLanes,
  type TraceMessageStep,
  type TraceRow,
  type TraceStep,
} from "./build-steps";
export {
  base64ByteLength,
  collapseDiffContext,
  diffTraceLines,
  parseUnifiedDiff,
  readImageResult,
  shortenTracePath,
  stripShellWrapper,
  traceEventCopyText,
  traceEventDetail,
  traceEventHasDetail,
  traceEventKind,
  traceEventLabel,
  traceEventSummary,
  traceEventSummaryIsMono,
  traceToolArgSummary,
  unwrapToolOutput,
  type TraceDiffLine,
  type TraceDiffLineKind,
  type TraceEvent,
  type TraceEventDetail,
  type TraceEventKind,
  type TraceImageResult,
  type TracePatchBody,
  type TracePatchFile,
  type TraceSummaryLabels,
} from "./trace-event-presenter";
export { buildRunOutcome, type RunOutcome } from "./run-outcome";
