/**
 * Issue-detail picker route map — the single source of truth for "which
 * formSheet route edits which issue attribute".
 *
 * Two call sites need it now: the attribute chip row
 * (`components/issue/attribute-row.tsx`) and the issue-detail header's
 * three-dot menu (`app/(app)/[workspace]/issue/[id].tsx`). Keeping one map
 * means both entry points open the exact same sheet for the same field —
 * the pattern the web/desktop menu follows by reusing one picker component
 * (`packages/views/issues/actions/issue-actions-menu-items.tsx`).
 *
 * Every pathname below is registered as a Stack.Screen with SHEET_OPTIONS
 * in `app/(app)/[workspace]/_layout.tsx`.
 */

export type IssuePickerField =
  | "status"
  | "priority"
  | "assignee"
  | "label"
  | "project"
  | "due-date";

export const ISSUE_PICKER_PATHNAMES = {
  status: "/[workspace]/issue/[id]/picker/status",
  priority: "/[workspace]/issue/[id]/picker/priority",
  assignee: "/[workspace]/issue/[id]/picker/assignee",
  label: "/[workspace]/issue/[id]/picker/label",
  project: "/[workspace]/issue/[id]/picker/project",
  "due-date": "/[workspace]/issue/[id]/picker/due-date",
} as const satisfies Record<IssuePickerField, string>;

export interface IssuePickerHref<F extends IssuePickerField> {
  pathname: (typeof ISSUE_PICKER_PATHNAMES)[F];
  params: { workspace: string; id: string };
}

/**
 * Build the typed `router.push` target for a picker sheet, or `null` when
 * the workspace slug / issue id isn't resolved yet.
 *
 * Returning `null` instead of pushing a half-filled route matters: both the
 * chip row and the header menu can render one frame before the workspace
 * store rehydrates, and a push with an empty `workspace` param lands on a
 * route that can't resolve its own issue.
 */
export function issuePickerHref<F extends IssuePickerField>(
  field: F,
  wsSlug: string | null | undefined,
  issueId: string | null | undefined,
): IssuePickerHref<F> | null {
  if (!wsSlug || !issueId) return null;
  return {
    pathname: ISSUE_PICKER_PATHNAMES[field],
    params: { workspace: wsSlug, id: issueId },
  };
}
