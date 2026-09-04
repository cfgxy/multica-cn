/**
 * Re-export shim — the implementation moved to `@multica/core/task-transcript`
 * so the mobile app (which may only import pure functions from core, per
 * apps/mobile/CLAUDE.md) shares the exact same logic. Web imports inside
 * views keep their relative `./trace-event-presenter` paths and need no changes.
 */
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
} from "@multica/core/task-transcript";
