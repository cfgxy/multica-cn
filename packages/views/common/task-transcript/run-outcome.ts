/**
 * Re-export shim — the implementation moved to `@multica/core/task-transcript`
 * so the mobile app (which may only import pure functions from core, per
 * apps/mobile/CLAUDE.md) shares the exact same logic. Web imports inside
 * views keep their relative `./run-outcome` paths and need no changes.
 */
export { buildRunOutcome, type RunOutcome } from "@multica/core/task-transcript";
