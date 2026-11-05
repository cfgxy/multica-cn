// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ComponentProps, ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import type { AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import enAgents from "../../locales/en/agents.json";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes, agents: enAgents },
};

const mockUpdateRuntime = vi.hoisted(() => vi.fn());
const mockRemoveRuntimeFromList = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());
const mockQueryData = vi.hoisted(() => ({
  members: [] as Array<Record<string, unknown>>,
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/api", () => ({
  api: {
    updateRuntime: (...args: unknown[]) => mockUpdateRuntime(...args),
  },
  ApiError: class ApiError extends Error {},
}));

vi.mock("sonner", () => ({
  toast: { error: mockToastError, success: vi.fn() },
}));

// Pull the bits we want to test directly from the detail file. They aren't
// exported, so we exercise them through RuntimeDetail's DiagnosticsCard.
// Easier path: import the inner components by re-exporting them from a
// shared module. They live in the same file as RuntimeDetail; rather than
// touching the prod file just to ease testing, we test by rendering
// `RuntimeDetail` with a runtime fixture and asserting on the visibility
// UI. To avoid pulling in the entire detail page (which would need
// presence maps, member lists, paths, agents queries, etc.) we stub the
// heavy queries below.
vi.mock("@tanstack/react-query", async () => {
  const actual =
    await vi.importActual<typeof import("@tanstack/react-query")>(
      "@tanstack/react-query",
    );
  return {
    ...actual,
    useQuery: vi.fn((options: { queryKey?: readonly unknown[] }) => {
      const key = options?.queryKey;
      if (key?.[0] === "workspaces" && key?.[2] === "members") {
        return { data: mockQueryData.members, isLoading: false };
      }
      return { data: [], isLoading: false };
    }),
  };
});

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (sel: (s: { user: { id: string } }) => unknown) =>
    sel({ user: { id: "user-me" } }),
}));

// isRuntimeUsableForUser is the shared owner/public rule the component reads
// runtime access from, so the real implementation is kept rather than stubbed —
// a stub here would just re-derive the rule this test is meant to pin down.
vi.mock("@multica/core/runtimes", async () => ({
  isRuntimeUsableForUser: (
    await vi.importActual<typeof import("@multica/core/runtimes")>(
      "@multica/core/runtimes",
    )
  ).isRuntimeUsableForUser,
  readRuntimeBackpressure: (
    await vi.importActual<typeof import("@multica/core/runtimes")>(
      "@multica/core/runtimes",
    )
  ).readRuntimeBackpressure,
  deriveRuntimeHealth: () => "online",
  runtimeDisplayName: (rt: { name: string; custom_name?: string | null }) =>
    rt.custom_name?.trim() || rt.name,
  runtimeDisplayLabel: (
    await vi.importActual<typeof import("@multica/core/runtimes")>(
      "@multica/core/runtimes",
    )
  ).runtimeDisplayLabel,
  useRemoveRuntimeFromList: () => ({
    isPending: false,
    mutateAsync: (...args: unknown[]) => mockRemoveRuntimeFromList(...args),
  }),
}));

vi.mock("@multica/core/agents", () => ({
  useWorkspacePresenceMap: () => ({ byAgent: new Map() }),
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    runtimes: () => "/runtimes",
    agentDetail: () => "/agents",
  }),
}));

vi.mock("@multica/core/runtimes/mutations", () => ({
  useUpdateRuntime: () => ({
    mutate: (
      args: { runtimeId: string; patch: Record<string, unknown> },
      opts?: { onSuccess?: () => void; onError?: () => void },
    ) => {
      mockUpdateRuntime(args.runtimeId, args.patch);
      opts?.onSuccess?.();
    },
    isPending: false,
  }),
}));

// Stubbing ProviderLogo / UsageSection avoids dragging in chart libs and
// additional query keys we don't care about here.
vi.mock("./provider-logo", () => ({ ProviderLogo: () => null }));
vi.mock("./usage-section", () => ({
  UsageSection: () => <div data-testid="usage-section" />,
}));
vi.mock("./shared", () => ({ HealthBadge: () => null }));
vi.mock("../../agents/presence", () => ({
  availabilityConfig: { offline: { dotClass: "", textClass: "" } },
  workloadConfig: { idle: { icon: () => null, textClass: "" } },
}));
vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("../../navigation", () => ({
  AppLink: ({ children }: { children: ReactNode }) => <>{children}</>,
  useNavigation: () => ({ push: vi.fn(), replace: vi.fn() }),
}));

import { RuntimeDetail } from "./runtime-detail";

function makeRuntime(overrides: Partial<AgentRuntime>): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: null,
    name: "Local Runtime",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "host.local",
    metadata: {},
    owner_id: "user-me",
    visibility: "private",
    last_seen_at: "2026-04-27T11:59:50Z",
    created_at: "2026-04-01T00:00:00Z",
    updated_at: "2026-04-01T00:00:00Z",
    ...overrides,
  };
}

function renderDetail(
  runtime: AgentRuntime,
  props: Pick<
    ComponentProps<typeof RuntimeDetail>,
    "machineHref" | "machineLabel"
  > = {},
) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>
        <RuntimeDetail runtime={runtime} {...props} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("RuntimeDetail visibility section", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockQueryData.members = [];
    mockRemoveRuntimeFromList.mockResolvedValue(undefined);
  });

  it("shows owner-editable visibility choices when the caller owns the runtime", () => {
    renderDetail(makeRuntime({ owner_id: "user-me" }));
    expect(screen.getByText("Visibility")).toBeInTheDocument();
    expect(screen.getByText("Private")).toBeInTheDocument();
    expect(screen.getByText("Public")).toBeInTheDocument();
  });

  it("keeps daemon CLI version details without rendering update controls", () => {
    renderDetail(
      makeRuntime({
        metadata: { cli_version: "0.3.17" },
        runtime_mode: "local",
      }),
    );

    fireEvent.click(
      screen.getByRole("button", { name: "Technical details" }),
    );

    expect(screen.getByText("0.3.17")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Update" }),
    ).not.toBeInTheDocument();
  });

  it("uses the provider name when the runtime alias is the machine name", () => {
    renderDetail(
      makeRuntime({
        name: "Pi (Studio Mac)",
        custom_name: "Studio Mac",
        provider: "pi",
      }),
      { machineHref: "/runtimes/local:daemon-1", machineLabel: "Studio Mac" },
    );

    expect(screen.getAllByText("Pi")).toHaveLength(2);
    expect(screen.getByRole("banner")).toHaveTextContent("Studio Mac");
  });

  it("preserves a runtime-specific alias that differs from the machine name", () => {
    renderDetail(
      makeRuntime({
        name: "Pi (Studio Mac)",
        custom_name: "Research Pi",
        provider: "pi",
      }),
      { machineHref: "/runtimes/local:daemon-1", machineLabel: "Studio Mac" },
    );

    expect(screen.getAllByText("Research Pi")).toHaveLength(2);
  });

  it("flips visibility to public when the owner clicks the Public choice", async () => {
    renderDetail(makeRuntime({ owner_id: "user-me", visibility: "private" }));
    fireEvent.click(screen.getByText("Public"));
    await waitFor(() =>
      expect(mockUpdateRuntime).toHaveBeenCalledWith("rt-1", { visibility: "public" }),
    );
  });

  it("saves a runtime-wide task limit from diagnostics", async () => {
    renderDetail(makeRuntime({ owner_id: "user-me", max_concurrent_tasks: 0 }));
    const input = screen.getByRole("spinbutton", { name: "Parallel task limit" });
    fireEvent.change(input, { target: { value: "8" } });
    fireEvent.click(screen.getByRole("button", { name: "Save parallel task limit" }));

    await waitFor(() =>
      expect(mockUpdateRuntime).toHaveBeenCalledWith("rt-1", {
        max_concurrent_tasks: 8,
      }),
    );
  });

  it("shows the unlimited runtime setting read-only to non-editors", () => {
    renderDetail(makeRuntime({ owner_id: "someone-else", max_concurrent_tasks: 0 }));
    expect(screen.getByText("Unlimited")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Save parallel task limit" }),
    ).not.toBeInTheDocument();
  });

  it("renders a read-only visibility chip when the caller cannot edit", () => {
    renderDetail(makeRuntime({ owner_id: "someone-else", visibility: "public" }));
    expect(screen.getByText("Public")).toBeInTheDocument();
    // The editor's "Private" choice button must not render in read-only mode.
    expect(screen.queryByText("Private")).not.toBeInTheDocument();
  });

  // MUL-6126: a workspace admin may rename or hide someone else's runtime,
  // but not share it — sharing is the owner's consent to lend their machine,
  // and the PATCH refuses an admin regardless of what this UI renders.
  it("keeps visibility read-only for a workspace admin who does not own the runtime", () => {
    mockQueryData.members = [
      { user_id: "user-me", role: "admin" },
      { user_id: "someone-else", role: "member" },
    ];
    renderDetail(
      makeRuntime({ owner_id: "someone-else", visibility: "private" }),
    );
    expect(screen.getByText("Private")).toBeInTheDocument();
    expect(screen.queryByText("Public")).not.toBeInTheDocument();
  });

  it("hides usage from a workspace admin viewing another member's private runtime", () => {
    mockQueryData.members = [
      { user_id: "user-me", role: "admin" },
      { user_id: "someone-else", role: "member" },
    ];

    renderDetail(
      makeRuntime({ owner_id: "someone-else", visibility: "private" }),
    );

    expect(screen.queryByTestId("usage-section")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /Remove from management list/i }),
    ).toBeInTheDocument();
  });

  it("shows usage to a workspace admin viewing another member's public runtime", () => {
    mockQueryData.members = [
      { user_id: "user-me", role: "admin" },
      { user_id: "someone-else", role: "member" },
    ];

    renderDetail(
      makeRuntime({ owner_id: "someone-else", visibility: "public" }),
    );

    expect(screen.getByTestId("usage-section")).toBeInTheDocument();
  });

  // An ownerless runtime is unreadable server-side (the read gate reuses
  // canUseRuntimeForAgent, which is fail-closed without an owner), so the
  // page must not render a Usage section that can only 404.
  it("hides usage for an ownerless runtime even when it is public", () => {
    mockQueryData.members = [{ user_id: "user-me", role: "admin" }];

    renderDetail(makeRuntime({ owner_id: null, visibility: "public" }));

    expect(screen.queryByTestId("usage-section")).not.toBeInTheDocument();
  });

  it("renders an enabled removal button for an owner on a local runtime", () => {
    renderDetail(
      makeRuntime({
        owner_id: "user-me",
        runtime_mode: "local",
        status: "online",
      }),
    );
    const btn = screen.getByRole("button", {
      name: /Remove from management list/i,
    }) as HTMLButtonElement;
    expect(btn.disabled).toBe(false);
  });

  it("hides the management-list removal button for callers who cannot edit", () => {
    renderDetail(
      makeRuntime({
        owner_id: "someone-else",
        runtime_mode: "local",
        status: "online",
      }),
    );
    expect(
      screen.queryByRole("button", { name: /Remove from management list/i }),
    ).not.toBeInTheDocument();
  });

  it("removes a custom runtime instance from the management list without deleting its profile", async () => {
    mockQueryData.members = [
      { user_id: "user-me", role: "owner", name: "Me" },
    ];

    renderDetail(
      makeRuntime({
        owner_id: "someone-else",
        profile_id: "profile-1",
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: /Remove from management list/i }));
    expect(
      screen.getByText('Remove "Local Runtime" from your management list?'),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Remove from list" }));
    await waitFor(() =>
      expect(mockRemoveRuntimeFromList).toHaveBeenCalledWith("rt-1"),
    );
  });

  it("shows removal for a custom runtime owner", () => {

    renderDetail(
      makeRuntime({
        owner_id: "user-me",
        profile_id: "profile-1",
      }),
    );

    expect(
      screen.getByRole("button", { name: /Remove from management list/i }),
    ).toBeInTheDocument();
  });

  it("keeps the dialog open and reports a removal failure", async () => {
    mockRemoveRuntimeFromList.mockRejectedValueOnce(new Error("remove failed"));
    renderDetail(makeRuntime({ owner_id: "user-me" }));

    fireEvent.click(screen.getByRole("button", { name: /Remove from management list/i }));
    fireEvent.click(screen.getByRole("button", { name: "Remove from list" }));

    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith("remove failed"));
    expect(
      screen.getByRole("alertdialog", {
        name: 'Remove "Local Runtime" from your management list?',
      }),
    ).toBeInTheDocument();
  });
});
