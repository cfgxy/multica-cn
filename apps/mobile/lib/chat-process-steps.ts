import { buildTimeline, type TimelineItem } from "@multica/core/task-transcript";
import type { TaskMessagePayload } from "@multica/core/types";

/**
 * Chat process-steps model (RUYI-444) — what the mobile "N steps" fold owns
 * and counts, made identical to web chat's pipeline:
 *
 *   1. `buildTimeline` (shared `@multica/core/task-transcript`): sort by seq →
 *      merge adjacent streaming text/thinking fragments that were split only
 *      by daemon flush timing → redactSecrets. Web chat runs the exact same
 *      call (`packages/views/chat/components/chat-message-list.tsx`).
 *   2. Middle carve, mirroring web's `splitTimeline`
 *      (`packages/views/chat/lib/copy-text.ts`): the fold spans from the
 *      first to the last non-text item, sandwiched text included; preface and
 *      final text render as the reply body outside the fold. The badge counts
 *      `middle` — the same number web's `OuterProcessFold` shows.
 *
 * Mobile difference (intentional): the reply body itself renders from
 * `message.content` below the fold, so preface/final are not re-exported
 * here — only the fold's rows are.
 *
 * The raw `task-messages` stream must never reach this UI directly: counting
 * it split one streaming thinking pass into dozens of steps (同一回复手机端
 * 2138 步 vs PC 端 416 步，RUYI-444 Owner 实机截图) — the same class of
 * count divergence as the 2026-05-09 inbox dedup incident.
 */
export function chatProcessSteps(items: TaskMessagePayload[]): {
  middle: TimelineItem[];
} {
  const timeline = buildTimeline(items);
  const firstNonText = timeline.findIndex((i) => i.type !== "text");
  if (firstNonText === -1) return { middle: [] };
  let lastNonText = timeline.length - 1;
  while (lastNonText >= 0 && timeline[lastNonText]!.type === "text") {
    lastNonText--;
  }
  return { middle: timeline.slice(firstNonText, lastNonText + 1) };
}
