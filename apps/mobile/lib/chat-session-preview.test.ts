// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ChatSession } from "@multica/core/types";
import { chatSessionPreview } from "./chat-session-preview";

/** Identity translator — assertions pin the en fallback copy. */
const t = (key: string, fallback: string) => fallback;

function session(overrides: Partial<ChatSession> = {}): ChatSession {
  return {
    id: "s1",
    workspace_id: "ws1",
    agent_id: "agent-1",
    creator_id: "user-1",
    title: "Weekly report",
    status: "active",
    has_unread: false,
    updated_at: "2026-10-07T08:00:00Z",
    ...overrides,
  } as ChatSession;
}

describe("chatSessionPreview", () => {
  it("archives win over everything else", () => {
    const result = chatSessionPreview(
      session({
        status: "archived",
        last_message: { content: "hello", role: "assistant", created_at: "" },
      }),
      t,
    );
    expect(result).toEqual({ kind: "archived", text: "archived" });
  });

  it("flags a failed last reply", () => {
    const result = chatSessionPreview(
      session({
        last_message: {
          content: "boom",
          role: "assistant",
          created_at: "",
          failure_reason: "runtime_offline",
        },
      }),
      t,
    );
    expect(result).toEqual({ kind: "failed", text: "Failed to send" });
  });

  it("localizes a no_response turn instead of leaking the stored fallback", () => {
    const result = chatSessionPreview(
      session({
        last_message: {
          content: "stored english fallback",
          role: "assistant",
          created_at: "",
          message_kind: "no_response",
        },
      }),
      t,
    );
    expect(result).toEqual({ kind: "no_response", text: "No text reply" });
  });

  it("prefixes user messages and strips markdown noise", () => {
    const result = chatSessionPreview(
      session({
        last_message: {
          content: "# Heading\n```js\ncode block\n```\n**bold** tail",
          role: "user",
          created_at: "",
        },
      }),
      t,
    );
    expect(result).toEqual({ kind: "preview", text: "You: Heading bold tail" });
  });

  it("renders assistant content verbatim (after strip)", () => {
    const result = chatSessionPreview(
      session({
        last_message: {
          content: "plain answer",
          role: "assistant",
          created_at: "",
        },
      }),
      t,
    );
    expect(result).toEqual({ kind: "preview", text: "plain answer" });
  });

  it("falls back to the empty hint when the session has no messages", () => {
    const result = chatSessionPreview(session(), t);
    expect(result).toEqual({ kind: "empty", text: "No messages yet" });
  });

  it("collapses whitespace so the row stays single-line", () => {
    const result = chatSessionPreview(
      session({
        last_message: {
          content: "a\n\n  b\tc",
          role: "assistant",
          created_at: "",
        },
      }),
      t,
    );
    expect(result.text).toBe("a b c");
  });
});
