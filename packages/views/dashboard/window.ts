// Window math moved to `@multica/core/dashboard/window` (RUYI-638) so the
// mobile stats screen derives its windows from the exact same functions.
// This shim keeps every existing `…/dashboard/window` import path working.
export {
  TIME_RANGES,
  quickWindow,
  shiftedWindow,
  windowLength,
  shiftWindow,
  canShiftNext,
  isCurrentWindow,
  dimsForWindowLength,
  dimsForDays,
} from "@multica/core/dashboard/window";
export type { TimeRange, Dim, StatWindow } from "@multica/core/dashboard/window";
