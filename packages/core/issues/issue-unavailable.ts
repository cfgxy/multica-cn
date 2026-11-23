export type IssueUnavailableReason =
  | "cross_workspace_unavailable"
  | "not_found";

/**
 * Limits only server-confirmed 404 issue-read failures to the two UI states
 * that are safe to render. All other failures remain retryable.
 */
export function issueUnavailableReason(error: unknown): IssueUnavailableReason | undefined {
  if (!error || typeof error !== "object" || (error as { status?: unknown }).status !== 404) {
    return undefined;
  }

  const body = (error as { body?: unknown }).body;
  if (body && typeof body === "object") {
    const code = (body as { code?: unknown }).code;
    if (code === "cross_workspace_unavailable") {
      return "cross_workspace_unavailable";
    }
  }
  return "not_found";
}
