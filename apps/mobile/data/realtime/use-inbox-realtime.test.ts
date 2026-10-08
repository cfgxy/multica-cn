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

vi.mock("@/data/api", () => ({ api: {} }));

vi.mock("./inbox-ws-updaters", () => ({
  patchInboxIssueStatus: vi.fn(),
  dropInboxItemsByIssue: vi.fn(),
}));

import { inboxKeys } from "@/data/queries/inbox";
import { useInboxRealtime } from "./use-inbox-realtime";

function useMountAndGetHandlers(): {
  handlers: Map<string, EventHandler>;
  onReconnect: EventHandler | undefined;
} {
  useInboxRealtime();
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

describe("useInboxRealtime", () => {
  beforeEach(() => {
    invalidateQueries.mockReset();
    subscriptionSetups.length = 0;
  });

  it.each([
    "inbox:archived",
    "inbox:unarchived",
    "inbox:batch-archived",
    "inbox:read",
    "inbox:unread",
    "inbox:new",
    "inbox:batch-read",
  ])(
    "invalidates the whole inbox family (list + archived) and the unread summary on %s",
    (event) => {
      const { handlers } = useMountAndGetHandlers();
      handlers.get(event)?.({});

      // The key family invalidation reaches both the main list AND the
      // archived sub-view cache: every inbox event can move an item across
      // that boundary and the split is decided server-side (RUYI-532).
      expect(invalidateQueries).toHaveBeenCalledWith({
        queryKey: inboxKeys.all("ws-1"),
      });
      expect(invalidateQueries).toHaveBeenCalledWith({
        queryKey: inboxKeys.unreadSummary(),
      });
    },
  );

  it("invalidates the unread summary on reconnect too", () => {
    const { onReconnect } = useMountAndGetHandlers();
    onReconnect?.(undefined);

    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: inboxKeys.all("ws-1"),
    });
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: inboxKeys.unreadSummary(),
    });
  });
});
