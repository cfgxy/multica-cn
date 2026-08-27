import { describe, expect, it } from "vitest";
import { chatSessionDisplayTitle } from "./chat-session-title";

describe("chatSessionDisplayTitle", () => {
  it("uses New chat for an explicitly empty channel-created Chat", () => {
    expect(chatSessionDisplayTitle("")).toBe("New chat");
    expect(chatSessionDisplayTitle(null)).toBe("New chat");
    expect(chatSessionDisplayTitle(undefined)).toBe("New chat");
  });

  it("honors a localized fallback (e.g. chat:mobile.sessions.untitled)", () => {
    expect(chatSessionDisplayTitle(null, "未命名聊天")).toBe("未命名聊天");
    expect(chatSessionDisplayTitle("Investigate deploy", "未命名聊天")).toBe(
      "Investigate deploy",
    );
  });

  it("preserves a stored or manually renamed title", () => {
    expect(chatSessionDisplayTitle("Investigate deploy")).toBe(
      "Investigate deploy",
    );
  });
});
