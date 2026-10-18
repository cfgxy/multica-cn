import { beforeEach, describe, expect, it, vi } from "vitest";
import { chatKeys } from "@/data/queries/chat";
import { useChatSessionRealtime } from "./use-chat-session-realtime";

const {
  invalidateQueries,
  subscriptionSetups,
  settlePendingChatTask,
} = vi.hoisted(() => ({
  invalidateQueries: vi.fn(),
  subscriptionSetups: [] as ((ws: MockWS) => (() => void)[])[],
  settlePendingChatTask: vi.fn(),
}));

type Handler = (payload: unknown) => void;
type MockWS = {
  on: ReturnType<typeof vi.fn>;
  onReconnect: ReturnType<typeof vi.fn>;
};

vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries }),
}));

vi.mock("@/lib/use-ws-subscriptions", () => ({
  useWSSubscriptions: (setup: (ws: MockWS) => (() => void)[]) => {
    subscriptionSetups.push(setup);
  },
}));

vi.mock("./chat-ws-updaters", () => ({
  appendTaskMessage: vi.fn(),
  applyChatDoneToCache: vi.fn(),
  applyChatQuickActionsToCache: vi.fn(),
  invalidatePendingTask: vi.fn(),
  promotePendingTaskToRunning: vi.fn(),
  seedPendingTaskFromQueued: vi.fn(),
  settlePendingChatTask,
}));

vi.mock("@/data/api", () => ({ api: {} }));

describe("useChatSessionRealtime terminal events", () => {
  beforeEach(() => {
    invalidateQueries.mockReset();
    settlePendingChatTask.mockReset();
    subscriptionSetups.length = 0;
  });

  it.each(["task:completed", "task:failed", "task:cancelled"] as const)(
    "settles the matching pending task immediately on %s",
    (event) => {
      useChatSessionRealtime("session-1");
      const handlers = new Map<string, Handler>();
      const ws: MockWS = {
        on: vi.fn((name: string, handler: Handler) => {
          handlers.set(name, handler);
          return () => {};
        }),
        onReconnect: vi.fn(() => () => {}),
      };

      subscriptionSetups[0](ws);
      handlers.get(event)?.({ chat_session_id: "session-1", task_id: "task-1" });

      expect(settlePendingChatTask).toHaveBeenCalledWith(
        expect.anything(),
        "session-1",
        "task-1",
      );
      if (event !== "task:completed") {
        expect(invalidateQueries).toHaveBeenCalledWith({
          queryKey: chatKeys.messages("session-1"),
        });
      }
    },
  );

  it("refreshes the session messages and pending state after reconnect", () => {
    useChatSessionRealtime("session-1");
    const ws: MockWS = {
      on: vi.fn(() => () => {}),
      onReconnect: vi.fn((handler: () => void) => {
        handler();
        return () => {};
      }),
    };

    subscriptionSetups[0](ws);

    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: chatKeys.messages("session-1"),
    });
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: chatKeys.pendingTask("session-1"),
    });
  });
});
