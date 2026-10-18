// @ts-nocheck
import React from "react";
import { Alert } from "react-native";
import { fireEvent, render, screen, waitFor } from "@testing-library/react-native";
import AgentRunConfig from "@/app/(app)/[workspace]/more/agents/[id]/run-config";

/**
 * RUYI-624 — the run-config formSheet is the mobile mirror of web's
 * execution section (agent-detail-inspector.tsx). Contract pinned here:
 *
 *   - the field set matches desktop's execution section: runtime, model
 *     (+ discovered catalog), thinking, speed/tier, concurrency, session
 *     context (limit + switch-at) and the subagent toggle;
 *   - Save PUTs ONLY the changed fields (untouched fields are pinned to the
 *     saved agent by buildProfilePatch, so the split editors can't clobber
 *     each other) — model switch cascades thinking/tier clear first;
 *   - a bounded draft outside its range reverts (Save stays disabled) —
 *     never clamps;
 *   - a failed save alerts instead of silently popping;
 *   - the voice override (RUYI-749) binds an eligible provider as a bare
 *     `voice_provider_id` (empty model/advanced drafts ride along as
 *     omissions, not explicit clears) and clears back to inherit with
 *     `voice_provider_id: ""` alone.
 */

const mockBack = jest.fn();
const mockMutate = jest.fn();

jest.mock("expo-router", () => ({
  router: { back: (...args) => mockBack(...args), push: jest.fn() },
  useLocalSearchParams: () => ({ id: "agent-1", workspace: "ws" }),
  Stack: { Screen: ({ children }) => children },
}));

// SheetHeader (RUYI-698) reads the sheet detent via useNavigation — the
// suite renders outside a NavigationContainer, so stub the hook only.
jest.mock("@react-navigation/native", () => ({
  ...jest.requireActual("@react-navigation/native"),
  useNavigation: () => ({ addListener: jest.fn(() => jest.fn()) }),
}));

jest.mock("react-native-keyboard-controller", () => {
  const React = jest.requireActual("react");
  const { View } = jest.requireActual("react-native");
  return {
    KeyboardAvoidingView: (props) => React.createElement(View, props),
  };
});

jest.mock("react-native-safe-area-context", () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

const mockAgent = {
  id: "agent-1",
  workspace_id: "ws-1",
  name: "Alpha",
  description: "d",
  instructions: "i",
  avatar_url: null,
  runtime_id: "runtime-1",
  runtime_bound: true,
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  model: "gpt-x",
  max_concurrent_tasks: 3,
  thinking_level: null,
  service_tier: null,
  voice_runtime_id: null,
  session_max_context_tokens: null,
  session_compact_pct: null,
  owner_id: "user-1",
  archived_at: null,
  permission_mode: "public_to",
  invocation_targets: [{ target_type: "workspace", target_id: "ws-1" }],
};

let mockRuntimes = [
  { id: "runtime-1", name: "Runtime One", custom_name: "", status: "online", provider: "openai" },
  { id: "runtime-2", name: "Runtime Two", custom_name: "", status: "online", provider: "openai" },
];

// Eligible-pool row (RUYI-749): gemini + enabled + configured credential +
// a probe that is not invalid — the full voiceProviderChainEligible gate.
const mockVoiceProviders = [
  {
    id: "vp-1",
    provider_type: "gemini",
    display_name: "Gemini WS",
    enabled: true,
    credential_status: "configured",
    credential_probe: { status: "ok" },
  },
];

// Per-test additive fields merged over mockAgent at useQuery call time, so
// a saved voice override can be staged without mutating the shared fixture.
let mockAgentOverrides: Record<string, unknown> = {};

jest.mock("@tanstack/react-query", () => ({
  // voiceProvidersOptions (RUYI-749) builds its query through queryOptions;
  // pass the descriptor through so useQuery below sees the real queryKey.
  queryOptions: (opts) => opts,
  useQuery: (opts) => {
    const key = opts.queryKey;
    if (key[0] === "agents") {
      return { data: { ...mockAgent, ...mockAgentOverrides }, isLoading: false };
    }
    if (key[0] === "workspaces" && key[2] === "voice-providers") {
      return { data: mockVoiceProviders, isLoading: false };
    }
    if (key[0] === "runtimes") {
      return { data: mockRuntimes, isLoading: false };
    }
    if (key[0] === "runtime-models") {
      return {
        data: { models: [{ id: "gpt-x" }, { id: "gpt-y" }] },
        isLoading: false,
      };
    }
    return { data: undefined, isLoading: false };
  },
}));

jest.mock("@/data/queries/agents", () => ({
  agentDetailOptions: (wsId, id) => ({
    queryKey: ["agents", wsId, "detail", id],
  }),
}));

jest.mock("@/data/queries/runtimes", () => ({
  runtimeListOptions: (wsId) => ({ queryKey: ["runtimes", wsId] }),
}));

jest.mock("@/data/queries/runtime-models", () => ({
  runtimeModelsOptions: (id) => ({ queryKey: ["runtime-models", id] }),
}));

jest.mock("@/data/mutations/agents", () => ({
  useUpdateAgent: () => ({ mutate: mockMutate, isPending: false }),
}));

// The slot filter has its own coverage in core — the editor test pins only
// that both runtimes are offered as the text slot.
jest.mock("@multica/core/runtimes", () => ({
  agentSlotChoices: () => mockRuntimes,
}));

jest.mock("@/lib/model-capability", () => ({
  resolveThinkingLevels: () => [
    { value: "high", label: "High" },
    { value: "low", label: "Low" },
  ],
  findModelCapabilityEntry: (_models, model) =>
    model ? { service_tiers: [{ id: "priority", name: "Priority" }] } : undefined,
}));

jest.mock("@/components/ui/switch", () => {
  const React = jest.requireActual("react");
  const { Pressable } = jest.requireActual("react-native");
  return {
    Switch: ({ checked, onCheckedChange }) =>
      React.createElement(Pressable, {
        testID: "subagents-switch",
        accessibilityState: { checked },
        onPress: () => onCheckedChange(!checked),
      }),
  };
});

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual("react-native");
  return { Text };
});

jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (key, fallback, opts) => {
    if (typeof fallback !== "string") return key;
    return fallback.replace(/\{\{(\w+)\}\}/g, (_m, name) =>
      String(opts?.[name] ?? ""),
    );
  } }),
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (sel) => sel({ currentWorkspaceId: "ws-1" }),
}));

jest.mock("@/data/auth-store", () => ({
  useAuthStore: (sel) => sel({ user: { id: "user-1" } }),
}));

beforeEach(() => {
  jest.clearAllMocks();
  mockAgentOverrides = {};
  mockRuntimes = [
    { id: "runtime-1", name: "Runtime One", custom_name: "", status: "online", provider: "openai" },
    { id: "runtime-2", name: "Runtime Two", custom_name: "", status: "online", provider: "openai" },
  ];
});

describe("AgentRunConfig (RUYI-624)", () => {
  it("renders the desktop execution-section field set with seeded values", async () => {
    await render(<AgentRunConfig />);
    for (const label of [
      "Execution",
      "Runtime",
      "Model",
      "Discovered models",
      "Thinking",
      "Speed",
      "Concurrency",
      "Session context",
      "Context limit",
      "Switch at",
      "Subagent tools",
    ]) {
      expect(screen.getByText(label)).toBeTruthy();
    }
    // Seeded from the saved agent before any interaction.
    expect(screen.getByDisplayValue("3")).toBeTruthy();
    expect(screen.getByDisplayValue("gpt-x")).toBeTruthy();
  });

  it("Save PUTs only the changed fields, after the model-switch cascade", async () => {
    await render(<AgentRunConfig />);
    await waitFor(() => expect(screen.getByDisplayValue("3")).toBeTruthy());

    // Switch model to the catalog entry — thinking/tier drafts clear with it.
    await fireEvent.press(screen.getByText("gpt-y"));
    // Then pick a thinking level and a new concurrency cap.
    await fireEvent.press(screen.getByText("High"));
    await fireEvent.changeText(screen.getByDisplayValue("3"), "5");
    await fireEvent.press(screen.getByLabelText("Save"));

    expect(mockMutate).toHaveBeenCalledTimes(1);
    expect(mockMutate).toHaveBeenCalledWith(
      { model: "gpt-y", thinking_level: "high", max_concurrent_tasks: 5 },
      expect.objectContaining({ onSuccess: expect.any(Function) }),
    );
  });

  it("switching the runtime clears model-derived state and PUTs the rebind with a cleared model", async () => {
    await render(<AgentRunConfig />);
    await waitFor(() => expect(screen.getByDisplayValue("gpt-x")).toBeTruthy());

    await fireEvent.press(screen.getByText("Runtime Two"));
    expect(screen.getByDisplayValue("")).toBeTruthy();
    expect(screen.queryByText("Priority")).toBeNull();

    // The rebind itself is the surviving change; the old model vocabulary
    // is explicitly cleared with it.
    await fireEvent.press(screen.getByLabelText("Save"));
    expect(mockMutate).toHaveBeenCalledWith(
      { runtime_id: "runtime-2", model: "" },
      expect.anything(),
    );
  });

  it("an out-of-range concurrency draft reverts (Save disabled), never clamps", async () => {
    await render(<AgentRunConfig />);
    await waitFor(() => expect(screen.getByDisplayValue("3")).toBeTruthy());

    await fireEvent.changeText(screen.getByDisplayValue("3"), "99");
    expect(screen.getByLabelText("Save").props.accessibilityState.disabled).toBe(
      true,
    );
    await fireEvent.press(screen.getByLabelText("Save"));
    expect(mockMutate).not.toHaveBeenCalled();
  });

  it("subagent toggle round-trips through runtime_config", async () => {
    await render(<AgentRunConfig />);
    await waitFor(() => expect(screen.getByDisplayValue("3")).toBeTruthy());

    await fireEvent.press(screen.getByTestId("subagents-switch"));
    await fireEvent.press(screen.getByLabelText("Save"));
    expect(mockMutate).toHaveBeenCalledWith(
      { runtime_config: { allow_subagents: true } },
      expect.anything(),
    );
  });

  it("binding a voice override PUTs the provider id alone; empty drafts ride as omissions (RUYI-749)", async () => {
    await render(<AgentRunConfig />);
    await waitFor(() => expect(screen.getByDisplayValue("3")).toBeTruthy());

    // The eligible row renders the provider display name; picking it is
    // the whole bind gesture. The exact-match assertion pins that the
    // empty model/advanced drafts are OMITTED, not sent as clears — an
    // explicit empty model would wipe a server-side value on a fresh bind.
    await fireEvent.press(screen.getByText("Gemini WS"));
    await fireEvent.press(screen.getByLabelText("Save"));

    expect(mockMutate).toHaveBeenCalledWith(
      { voice_provider_id: "vp-1" },
      expect.anything(),
    );
  });

  it("a runtime-less agent can still save a voice override (save is not runtime-gated, RUYI-749 P2-c)", async () => {
    // QA m19 scenario: no runtime exists at all and the agent has none
    // bound. The voice override is agent-level config, so Save must be
    // enabled and the PUT must carry the voice field alone — unchanged
    // runtime fields are pinned out of the patch by buildProfilePatch.
    mockAgentOverrides = { runtime_id: "" };
    mockRuntimes = [];
    await render(<AgentRunConfig />);
    await waitFor(() => expect(screen.getByText("Gemini WS")).toBeTruthy());

    expect(screen.getByLabelText("Save").props.accessibilityState.disabled).toBe(
      true,
    );
    await fireEvent.press(screen.getByText("Gemini WS"));
    expect(screen.getByLabelText("Save").props.accessibilityState.disabled).toBe(
      false,
    );
    await fireEvent.press(screen.getByLabelText("Save"));

    expect(mockMutate).toHaveBeenCalledWith(
      { voice_provider_id: "vp-1" },
      expect.anything(),
    );
  });

  it("clearing a saved override back to inherit PUTs an empty provider id alone (RUYI-749)", async () => {
    mockAgentOverrides = {
      voice_provider_id: "vp-1",
      voice_model: "m-9",
      voice_advanced: { temperature: 0.5 },
    };
    await render(<AgentRunConfig />);
    await waitFor(() => expect(screen.getByDisplayValue("3")).toBeTruthy());

    await fireEvent.press(screen.getByText("Inherit workspace default"));
    await fireEvent.press(screen.getByLabelText("Save"));

    // The server 400s a clear that carries model/advanced, so the wire
    // body must be exactly this one field.
    expect(mockMutate).toHaveBeenCalledWith(
      { voice_provider_id: "" },
      expect.anything(),
    );
  });

  it("a failed save alerts instead of silently popping", async () => {
    mockMutate.mockImplementation((_patch, config) => {
      config?.onError?.(new Error("boom"));
    });
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    await render(<AgentRunConfig />);
    await waitFor(() => expect(screen.getByDisplayValue("3")).toBeTruthy());

    await fireEvent.changeText(screen.getByDisplayValue("3"), "5");
    await fireEvent.press(screen.getByLabelText("Save"));

    expect(alertSpy).toHaveBeenCalledWith("Failed to update agent", "boom");
    expect(mockBack).not.toHaveBeenCalled();
    alertSpy.mockRestore();
  });
});
