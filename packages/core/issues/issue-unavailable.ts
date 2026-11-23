export type IssueUnavailableReason =
  | "cross_workspace_unavailable"
  | "not_found";

/**
 * Limits issue-read failures to the two UI states that are safe to render.
 * Unknown and malformed responses deliberately fall back to not_found.
 */
export function issueUnavailableReason(error: unknown): IssueUnavailableReason {
  if (error && typeof error === "object") {
    const body = (error as { body?: unknown }).body;
    if (body && typeof body === "object") {
      const code = (body as { code?: unknown }).code;
      if (code === "cross_workspace_unavailable") {
        return "cross_workspace_unavailable";
      }
    }
  }
  return "not_found";
}
