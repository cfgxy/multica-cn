// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";

const { invalidateQueries, subscriptionSetups } = vi.hoisted(() => ({
  invalidateQueries: vi.fn(),
  subscriptionSetups: [] as Array<(ws: MockWS, wsId: string) => Array<() => void>>,
}));

type EventHandler = (payload: unknown) => void;

interface MockWS {
  on: ReturnType<typeof vi.fn>;
  onReconnect: ReturnType<typeof vi.fn>;
}

vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries }),
}));

vi.mock("@/lib/use-ws-subscriptions", () => ({
  useWSSubscriptions: (setup: (ws: MockWS, wsId: string) => Array<() => void>) => {
    subscriptionSetups.push(setup);
  },
}));

vi.mock("@/data/queries/github", () => ({
  githubKeys: { pullRequests: (issueId: string | null) => ["github", "pull-requests", issueId] },
}));

vi.mock("@multica/core/issues/decisions", () => ({ upsertDecisionInCache: vi.fn() }));
vi.mock("@multica/core/issues/queries", () => ({ issueKeys: { decisions: vi.fn() } }));
vi.mock("./issue-ws-updaters", () =>
  Object.fromEntries(
    [
      "addCommentReaction",
      "addIssueReaction",
      "onIssueAuxiliaryRevision",
      "appendTimelineEntry",
      "clearIssueDetail",
      "commentToTimelineEntry",
      "invalidateIssueAfterReconnect",
      "patchIssueDetail",
      "patchIssueLabels",
      "patchIssuesList",
      "patchMyIssuesList",
      "replaceCommentTimelineEntry",
      "removeCommentCascade",
      "removeCommentReaction",
      "removeFromMyIssuesList",
      "removeIssueReaction",
    ].map((name) => [name, vi.fn()]),
  ),
);

import { githubKeys } from "@/data/queries/github";
import { useIssueRealtime } from "./use-issue-realtime";

function mountAndGetHandlers(): {
  handlers: Map<string, EventHandler>;
  onReconnect: EventHandler | undefined;
} {
  useIssueRealtime("issue-1");
  const setup = subscriptionSetups[subscriptionSetups.length - 1];
  const handlers = new Map<string, EventHandler>();
  let onReconnect: EventHandler | undefined;
  const ws: MockWS = {
    on: vi.fn((event: string, handler: EventHandler) => {
      handlers.set(event, handler);
      return () => {};
    }),
    onReconnect: vi.fn((handler: EventHandler) => {
      onReconnect = handler;
      return () => {};
    }),
  };

  setup(ws, "ws-1");
  return { handlers, onReconnect };
}

describe("useIssueRealtime pull-request refresh", () => {
  beforeEach(() => {
    invalidateQueries.mockReset();
    subscriptionSetups.length = 0;
  });

  it("refreshes the PR list for linked, updated, and unlinked events for this issue", () => {
    const { handlers } = mountAndGetHandlers();
    handlers.get("pull_request:linked")?.({ issue_id: "issue-1" });
    handlers.get("pull_request:updated")?.({ linked_issue_ids: ["issue-1"] });
    handlers.get("pull_request:unlinked")?.({ issue_id: "issue-1" });

    expect(invalidateQueries).toHaveBeenCalledTimes(3);
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: githubKeys.pullRequests("issue-1"),
    });
  });

  it("ignores unrelated PR events and refreshes after reconnect", () => {
    const { handlers, onReconnect } = mountAndGetHandlers();
    handlers.get("pull_request:updated")?.({ linked_issue_ids: ["issue-2"] });
    expect(invalidateQueries).not.toHaveBeenCalledWith({
      queryKey: githubKeys.pullRequests("issue-1"),
    });

    onReconnect?.(undefined);

    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: githubKeys.pullRequests("issue-1"),
    });
  });
});
