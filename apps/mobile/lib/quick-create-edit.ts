/**
 * "Edit in the full form" planning for quick-create outcome notifications
 * (RUYI-605).
 *
 * Mirrors web's gate in packages/views/inbox/components/inbox-page.tsx: the
 * detail pane shows the edit button for EVERY quick-create outcome
 * (`isQuickCreateOutcome` — failed and unconfirmed alike, no original_prompt
 * requirement); tapping it seeds the manual create form with the stored
 * original prompt as the description and the original agent as the assignee
 * candidate. `isQuickCreateOutcome` mirrors
 * packages/views/inbox/components/inbox-display.ts:51 — mobile keeps its own
 * copy per Sharing Principles (views UI helpers are not on the mobile
 * import whitelist).
 *
 * `quick_create_unconfirmed` deliberately gets the button too: the outcome
 * is "result unverified", not "failed", and re-editing in the manual form is
 * the non-destructive recovery path. Failure wording stays out of that
 * flow — the button label is neutral on both platforms.
 */
import type { InboxItem } from "@multica/core/types";

export function isQuickCreateOutcome(type: InboxItem["type"]): boolean {
  return (
    type === "quick_create_failed" || type === "quick_create_unconfirmed"
  );
}

export interface QuickCreateEditSeed {
  /** The stored original input — may be empty; the button still renders. */
  description: string;
  /** details.agent_id when present; null leaves the form's assignee default. */
  agentId: string | null;
}

export function getQuickCreateEditSeed(
  item: InboxItem,
): QuickCreateEditSeed | null {
  if (!isQuickCreateOutcome(item.type)) return null;
  const details = item.details;
  return {
    description: details?.original_prompt ?? "",
    agentId: details?.agent_id || null,
  };
}
