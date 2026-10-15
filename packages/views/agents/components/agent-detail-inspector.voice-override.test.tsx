import { cleanup, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent, VoiceProvider } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";

const mocks = vi.hoisted(() => ({
  providers: [] as VoiceProvider[],
}));

vi.mock("@tanstack/react-query", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-query")>()),
  useQuery: (options: { queryKey?: readonly unknown[] }) =>
    options.queryKey?.[0] === "voice-providers"
      ? { data: mocks.providers, isLoading: false }
      : { data: undefined, isSuccess: false },
}));

vi.mock("../../common/avatar-upload-control", () => ({
  AvatarUploadControl: () => <div data-testid="avatar-upload" />,
}));

vi.mock("./inspector/model-picker", () => ({
  ModelPicker: () => <div data-testid="model-picker" />,
}));

vi.mock("./inspector/runtime-picker", () => ({
  RuntimePicker: () => <div data-testid="runtime-picker" />,
}));

vi.mock("./inspector/thinking-prop-row", () => ({
  ThinkingSettingField: () => <div data-testid="thinking-field" />,
}));

vi.mock("./inspector/service-tier-setting-field", () => ({
  ServiceTierSettingField: () => <div data-testid="service-tier-field" />,
}));

import { AgentDetailInspector } from "./agent-detail-inspector";

function providerFixture(overrides: Partial<VoiceProvider> & { id: string }): VoiceProvider {
  return {
    workspace_id: "workspace-1",
    display_name: `Provider ${overrides.id}`,
    provider_type: "gemini",
    enabled: true,
    credential_status: "configured",
    credential_probe: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function makeAgent(overrides: Partial<Agent> = {}) {
  return {
    id: "agent-1",
    workspace_id: "workspace-1",
    name: "Lambda",
    description: "Test agent",
    runtime_id: "runtime-1",
    session_max_context_tokens: 400_000,
    session_compact_pct: 80,
    ...overrides,
  } as Agent;
}

function renderInspector(agent: Agent, onUpdate = vi.fn(async () => {})) {
  renderWithI18n(
    <AgentDetailInspector
      agent={agent}
      runtime={null}
      runtimes={[]}
      members={[]}
      currentUserId="user-1"
      canEdit
      onUpdate={onUpdate}
    />,
  );
  return onUpdate;
}

beforeEach(() => {
  mocks.providers = [];
});

afterEach(() => {
  cleanup();
});

describe("AgentDetailInspector voice override", () => {
  it("treats the wire's empty-string unbound value as inherit on the closed selector", () => {
    renderInspector(makeAgent({ voice_provider_id: "" }));

    const trigger = screen.getByLabelText("Voice provider");
    expect(trigger).toHaveTextContent("Inherit workspace default");
    // An unbound agent has nothing to reset.
    expect(
      screen.queryByRole("button", { name: "Reset to workspace default" }),
    ).not.toBeInTheDocument();
  });

  it("shows the inherit label when the provider override is absent", () => {
    renderInspector(makeAgent());

    const trigger = screen.getByLabelText("Voice provider");
    expect(trigger).toHaveTextContent("Inherit workspace default");
  });

  it("shows the provider name on the closed selector of a bound agent", () => {
    mocks.providers = [providerFixture({ id: "p1", display_name: "QA Gemini B" })];
    renderInspector(makeAgent({ voice_provider_id: "p1" }));

    const trigger = screen.getByLabelText("Voice provider");
    expect(trigger).toHaveTextContent("QA Gemini B");
  });

  it("does not leak the built-in default model constant into the model placeholder", () => {
    mocks.providers = [providerFixture({ id: "p1", display_name: "QA Gemini B" })];
    renderInspector(makeAgent({ voice_provider_id: "p1" }));

    const input = screen.getByPlaceholderText("Inherits the workspace default model");
    expect(input).toBeInTheDocument();
    expect(screen.queryByPlaceholderText(/gemini-3\.8-live/)).not.toBeInTheDocument();
  });
});
