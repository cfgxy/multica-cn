import { describe, expect, it } from "vitest";
import {
  AGENT_WEBHOOK_URL_MASK,
  buildAgentWebhookUrl,
  maskAgentWebhookUrl,
  maskedAgentWebhookUrlPreview,
} from "./webhook";
import type { AgentWebhook } from "../types";

const baseWebhook: AgentWebhook = {
  id: "w1",
  agent_id: "a1",
  name: "push",
  prompt: "check",
  enabled: true,
  created_at: "",
  updated_at: "",
  webhook_path_masked: "/api/webhooks/agents/••••••••••••",
  webhook_token: "awt_abc",
  webhook_path: "/api/webhooks/agents/awt_abc",
  webhook_url: null,
};

describe("buildAgentWebhookUrl", () => {
  it("returns the server-provided webhook_url verbatim when present", () => {
    expect(
      buildAgentWebhookUrl({
        webhook: { ...baseWebhook, webhook_url: "https://custom.example/api/webhooks/agents/awt_abc" },
      }),
    ).toBe("https://custom.example/api/webhooks/agents/awt_abc");
  });

  it("composes from apiBaseUrl + webhook_path", () => {
    expect(
      buildAgentWebhookUrl({ webhook: baseWebhook, apiBaseUrl: "https://api.example" }),
    ).toBe("https://api.example/api/webhooks/agents/awt_abc");
  });

  it("strips trailing slash on apiBaseUrl", () => {
    expect(
      buildAgentWebhookUrl({ webhook: baseWebhook, apiBaseUrl: "https://api.example/" }),
    ).toBe("https://api.example/api/webhooks/agents/awt_abc");
  });

  it("falls back to currentOrigin when apiBaseUrl is empty", () => {
    expect(
      buildAgentWebhookUrl({
        webhook: baseWebhook,
        apiBaseUrl: "",
        currentOrigin: "https://app.example",
      }),
    ).toBe("https://app.example/api/webhooks/agents/awt_abc");
  });

  it("composes from token when webhook_path is missing", () => {
    expect(
      buildAgentWebhookUrl({
        webhook: { ...baseWebhook, webhook_path: null },
        apiBaseUrl: "https://api.example",
      }),
    ).toBe("https://api.example/api/webhooks/agents/awt_abc");
  });

  it("returns null when the viewer holds no credential fields", () => {
    expect(
      buildAgentWebhookUrl({
        webhook: { ...baseWebhook, webhook_token: null, webhook_path: null, webhook_url: null },
      }),
    ).toBeNull();
  });
});

describe("maskAgentWebhookUrl", () => {
  it("keeps the origin and path prefix readable and masks only the token", () => {
    expect(
      maskAgentWebhookUrl("https://mica.example/api/webhooks/agents/awt_secret"),
    ).toBe(`https://mica.example/api/webhooks/agents/${AGENT_WEBHOOK_URL_MASK}`);
  });

  it("mask is fixed-width and never derived from the token", () => {
    const short = maskAgentWebhookUrl("https://x.test/api/webhooks/agents/a");
    const long = maskAgentWebhookUrl("https://x.test/api/webhooks/agents/awt_aaaaaaaaaaaaaaaaaaaa");
    expect(short).toBe(long);
    expect(short).not.toContain("awt_");
  });

  it("falls back to the bare mask for a URL without a separable segment", () => {
    expect(maskAgentWebhookUrl("not-a-url")).toBe(AGENT_WEBHOOK_URL_MASK);
    expect(maskAgentWebhookUrl("https://x.test/")).toBe(AGENT_WEBHOOK_URL_MASK);
  });
});

describe("maskedAgentWebhookUrlPreview", () => {
  it("builds a recognizable non-functional preview for non-managers", () => {
    expect(maskedAgentWebhookUrlPreview({ apiBaseUrl: "https://api.example" })).toBe(
      `https://api.example/api/webhooks/agents/${AGENT_WEBHOOK_URL_MASK}`,
    );
  });

  it("renders a relative preview when no base is known", () => {
    expect(maskedAgentWebhookUrlPreview({})).toBe(`/api/webhooks/agents/${AGENT_WEBHOOK_URL_MASK}`);
  });
});
