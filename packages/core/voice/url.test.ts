import { buildProviderVoiceSessionUrl, buildVoiceSessionUrl, toWebSocketBase } from "./url";

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

  it("carries the chat session link (RUYI-874) as chat_session_id", () => {
    expect(
      buildVoiceSessionUrl("http://localhost:8080", "a", {
        workspaceSlug: "acme",
        chatSessionId: "chat-uuid",
      }),
    ).toBe(
      "ws://localhost:8080/api/agents/a/voice-session?workspace_slug=acme&chat_session_id=chat-uuid",
    );
  });

  it("carries Issue-comment context as issue_id", () => {
    expect(
      buildVoiceSessionUrl("http://localhost:8080", "a", {
        workspaceSlug: "acme",
        issueId: "issue uuid",
      }),
    ).toBe("ws://localhost:8080/api/agents/a/voice-session?workspace_slug=acme&issue_id=issue%20uuid");
  });

  it("omits the link when no chat session is bound", () => {
    expect(
      buildVoiceSessionUrl("http://localhost:8080", "a", {
        workspaceSlug: "acme",
        chatSessionId: null,
      }),
    ).toBe("ws://localhost:8080/api/agents/a/voice-session?workspace_slug=acme");
  });

  it("encodes a chat_session_id with reserved characters", () => {
    expect(
      buildVoiceSessionUrl("http://localhost:8080", "a", {
        chatSessionId: "id with spaces",
      }),
    ).toBe(
      "ws://localhost:8080/api/agents/a/voice-session?chat_session_id=id%20with%20spaces",
    );
  });

  it("never places a token in the URL", () => {
    const url = buildVoiceSessionUrl("https://m.example", "a", { workspaceSlug: "s" });
    expect(url).not.toContain("token");
  });
});

describe("buildProviderVoiceSessionUrl", () => {
  it("targets the demo-aligned BidiGenerateContent route", () => {
    expect(buildProviderVoiceSessionUrl()).toBe(
      "wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent",
    );
  });

  it("accepts a base override and trims the trailing slash", () => {
    expect(buildProviderVoiceSessionUrl("wss://proxy.example/")).toBe(
      "wss://proxy.example/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent",
    );
  });
});
