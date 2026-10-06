import { buildVoiceSessionUrl, toWebSocketBase } from "./url";

describe("toWebSocketBase", () => {
  it("upgrades http to ws", () => {
    expect(toWebSocketBase("http://localhost:8080")).toBe("ws://localhost:8080");
  });

  it("upgrades https to wss and trims trailing slashes", () => {
    expect(toWebSocketBase("https://multica.example.com/")).toBe(
      "wss://multica.example.com",
    );
  });
});

describe("buildVoiceSessionUrl", () => {
  it("targets the gateway route for the agent", () => {
    expect(buildVoiceSessionUrl("http://localhost:8080", "agent-1")).toBe(
      "ws://localhost:8080/api/agents/agent-1/voice-session",
    );
  });

  it("prefers workspace_id in the query (header-less upgrade)", () => {
    expect(
      buildVoiceSessionUrl("http://localhost:8080", "a", {
        workspaceId: "ws-uuid",
        workspaceSlug: "acme",
      }),
    ).toBe("ws://localhost:8080/api/agents/a/voice-session?workspace_id=ws-uuid");
  });

  it("falls back to workspace_slug", () => {
    expect(
      buildVoiceSessionUrl("http://localhost:8080", "a", { workspaceSlug: "acme" }),
    ).toBe("ws://localhost:8080/api/agents/a/voice-session?workspace_slug=acme");
  });

  it("never places a token in the URL", () => {
    const url = buildVoiceSessionUrl("https://m.example", "a", { workspaceSlug: "s" });
    expect(url).not.toContain("token");
  });
});
