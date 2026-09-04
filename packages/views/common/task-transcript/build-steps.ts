/**
 * Re-export shim — the implementation moved to `@multica/core/task-transcript`
 * so the mobile app (which may only import pure functions from core, per
 * apps/mobile/CLAUDE.md) shares the exact same logic. Web imports inside
 * views keep their relative `./build-steps` paths and need no changes.
 */
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
} from "@multica/core/task-transcript";
