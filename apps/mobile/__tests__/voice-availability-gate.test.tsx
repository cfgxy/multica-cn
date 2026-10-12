/**
 * RUYI-734 — voice gate chain inputs on mobile. Pins the hook-level
 * priority matrix the availability unit tests can't see: the workspace
 * default must reach the gate through the mobile-owned workspace list
 * (the core ApiClient singleton behind `workspaceBySlugOptions` is never
 * initialised on mobile — CoreProvider is web/desktop-only), the agent
 * override keeps priority, and a broken override must not cascade into
 * the workspace default (RUYI-693 chain semantics: a rejected level falls
 * back to vanilla, never to the next level).
 */
import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react-native";
import type { Agent, Workspace } from "@multica/core/types";

const mockListWorkspaces = jest.fn();
const mockListVoiceProviders = jest.fn();

jest.mock("@/data/api", () => ({
  api: {
    listWorkspaces: (...args: unknown[]) => mockListWorkspaces(...args),
    listVoiceProviders: (...args: unknown[]) => mockListVoiceProviders(...args),
  },
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (state: Record<string, unknown>) => unknown) =>
    selector({ currentWorkspaceId: "ws-1", currentWorkspaceSlug: "acme" }),
}));

import { useVoiceAvailability } from "@/data/voice-providers";

function poolProvider(id: string, overrides: Record<string, unknown> = {}) {
  return {
    id,
    workspace_id: "ws-1",
    display_name: `provider-${id}`,
    provider_type: "gemini",
    enabled: true,
    credential_status: "configured",
    credential_probe: { status: "ok" },
    ...overrides,
  };
}

function workspace(settings: unknown): Workspace {
  return {
    id: "ws-1",
    slug: "acme",
    name: "Acme",
    settings,
  } as Workspace;
}

function agent(voiceProviderId?: string | null): Agent {
  // The gate reads the additive override field defensively off the core
  // Agent type (RUYI-694 D3); a minimal fixture cast is enough here.
  return {
    id: "agent-1",
    voice_provider_id: voiceProviderId ?? null,
  } as unknown as Agent;
}

async function renderGate(currentAgent: Agent | null) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  return renderHook(() => useVoiceAvailability(currentAgent, "acme"), {
    wrapper,
  });
}

describe("useVoiceAvailability chain priority", () => {
  beforeEach(() => {
    mockListWorkspaces.mockReset();
    mockListVoiceProviders.mockReset();
  });

  it("lets the workspace default drive the gate when no override exists", async () => {
    mockListWorkspaces.mockResolvedValue([
      workspace({ voice: { default: { provider_id: "vp-ws" } } }),
    ]);
    mockListVoiceProviders.mockResolvedValue([poolProvider("vp-ws")]);

    const { result } = await renderGate(agent(null));

    await waitFor(() => expect(result.current.resolving).toBe(false));
    expect(result.current.availability).toEqual({ available: true });
  });

  it("keeps the agent override ahead of the workspace default", async () => {
    mockListWorkspaces.mockResolvedValue([
      workspace({ voice: { default: { provider_id: "vp-ws" } } }),
    ]);
    mockListVoiceProviders.mockResolvedValue([
      poolProvider("vp-ws"),
      poolProvider("vp-agent", { credential_probe: { status: "ok" } }),
    ]);

    const { result } = await renderGate(agent("vp-agent"));

    await waitFor(() => expect(result.current.resolving).toBe(false));
    expect(result.current.availability).toEqual({ available: true });
  });

  it("never cascades a broken override into the workspace default", async () => {
    mockListWorkspaces.mockResolvedValue([
      workspace({ voice: { default: { provider_id: "vp-ws" } } }),
    ]);
    mockListVoiceProviders.mockResolvedValue([
      poolProvider("vp-ws"),
      poolProvider("vp-agent", { enabled: false }),
    ]);

    const { result } = await renderGate(agent("vp-agent"));

    await waitFor(() => expect(result.current.resolving).toBe(false));
    expect(result.current.availability).toEqual({
      available: false,
      reason: "provider_disabled",
    });
  });

  it("falls back to unconfigured when no chain level names a provider", async () => {
    mockListWorkspaces.mockResolvedValue([workspace({})]);
    mockListVoiceProviders.mockResolvedValue([poolProvider("vp-ws")]);

    const { result } = await renderGate(agent(null));

    await waitFor(() => expect(result.current.resolving).toBe(false));
    expect(result.current.availability).toEqual({
      available: false,
      reason: "unconfigured",
    });
  });
});
