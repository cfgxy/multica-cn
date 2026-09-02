import { describe, expect, it, vi } from "vitest";
import type { ChatSession } from "@multica/core/types";

import { sortChatSessions } from "./chat";

// data/queries/chat transitively imports the native fetch client via api.ts.
// Mock it so the Node test never loads RN modules — sortChatSessions itself
// is a pure function and needs nothing from api.
vi.mock("@/data/api", () => ({ api: {} }));

function session(
  over: Partial<ChatSession> & { id: string },
): ChatSession {
  return {
    workspace_id: "",
    agent_id: "",
    creator_id: "",
    title: "",
    status: "active",
    has_unread: false,
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
    ...over,
  };
}

describe("sortChatSessions", () => {
  it("ranks pinned sessions before unpinned ones", () => {
    const sorted = sortChatSessions([
      session({ id: "a", updated_at: "2026-08-02T00:00:00Z" }),
      session({ id: "b", pinned: true, updated_at: "2026-08-01T00:00:00Z" }),
    ]);

    expect(sorted.map((s) => s.id)).toEqual(["b", "a"]);
  });

  it("orders unpinned sessions by most-recent activity", () => {
    const sorted = sortChatSessions([
      session({ id: "old", updated_at: "2026-08-01T00:00:00Z" }),
      session({ id: "new", updated_at: "2026-08-03T00:00:00Z" }),
      session({ id: "mid", updated_at: "2026-08-02T00:00:00Z" }),
    ]);

    expect(sorted.map((s) => s.id)).toEqual(["new", "mid", "old"]);
  });

  it("prefers last_message.created_at over updated_at for activity", () => {
    const sorted = sortChatSessions([
      session({
        id: "bumped-updated_at",
        updated_at: "2026-08-05T00:00:00Z",
      }),
      session({
        id: "fresh-message",
        updated_at: "2026-08-01T00:00:00Z",
        last_message: {
          content: "latest reply",
          role: "assistant",
          created_at: "2026-08-06T00:00:00Z",
        },
      }),
    ]);

    expect(sorted.map((s) => s.id)).toEqual(["fresh-message", "bumped-updated_at"]);
  });

  it("is stable for equal keys (pinned rows keep server order)", () => {
    const sorted = sortChatSessions([
      session({ id: "pin-1", pinned: true, updated_at: "2026-08-01T00:00:00Z" }),
      session({ id: "pin-2", pinned: true, updated_at: "2026-08-01T00:00:00Z" }),
    ]);

    expect(sorted.map((s) => s.id)).toEqual(["pin-1", "pin-2"]);
  });

  it("does not mutate the input array", () => {
    const input = [
      session({ id: "a", updated_at: "2026-08-02T00:00:00Z" }),
      session({ id: "b", pinned: true, updated_at: "2026-08-01T00:00:00Z" }),
    ];
    const snapshot = [...input];

    sortChatSessions(input);

    expect(input.map((s) => s.id)).toEqual(snapshot.map((s) => s.id));
  });
});
