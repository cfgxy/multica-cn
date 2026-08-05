// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { Agent, AgentRuntime } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enAgents from "../../locales/en/agents.json";

const TEST_RESOURCES = { en: { common: enCommon, agents: enAgents } };

const migrateSpy = vi.hoisted(() => vi.fn());
const mutateAsyncSpy = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", () => ({
  api: { migrateAgentsToRuntime: migrateSpy },
}));
vi.mock("@multica/core/runtimes/mutations", () => ({
  useMigrateAgentsToRuntime: () => ({
    mutateAsync: mutateAsyncSpy,
    isPending: false,
  }),
}));
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <div>avatar</div>,
}));
// The picker's own behaviour is covered by runtime-picker.test.tsx; here we
// only need a way to choose a target.
vi.mock("./inspector/runtime-picker", () => ({
  RuntimePicker: ({ onChange }: { onChange: (id: string) => void }) => (
    <button type="button" onClick={() => onChange("rt-target")}>
      pick-target
    </button>
  ),
}));
vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}));

import { SwitchAgentRuntimeDialog } from "./switch-agent-runtime-dialog";

function makeAgent(id: string, overrides: Partial<Agent> = {}): Agent {
  return {
    id,
    workspace_id: "ws-1",
    runtime_id: "rt-source",
    name: `Agent ${id}`,
    description: "",
    model: "",
    archived_at: null,
    custom_env_key_count: 0,
    ...overrides,
  } as Agent;
}

const RUNTIMES = [
  { id: "rt-target", name: "Target", status: "online" },
] as unknown as AgentRuntime[];

function renderDialog(agents: Agent[], extraProps: Record<string, unknown> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <SwitchAgentRuntimeDialog
          open
          onOpenChange={() => {}}
          agents={agents}
          runtimes={RUNTIMES}
          members={[]}
          currentUserId="user-1"
          wsId="ws-1"
          {...extraProps}
        />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

const PREVIEW = {
  target_runtime_id: "rt-target",
  dry_run: true,
  migrated: [
    {
      agent_id: "a",
      name: "Agent a",
      cleared_model: "claude-opus-4",
      cleared_thinking_level: "high",
    },
  ],
  skipped: [],
  tasks_migrated: 2,
  tasks_staying_active: 3,
};

beforeEach(() => {
  migrateSpy.mockReset();
  migrateSpy.mockResolvedValue(PREVIEW);
  mutateAsyncSpy.mockReset();
  mutateAsyncSpy.mockResolvedValue({
    ...PREVIEW,
    dry_run: false,
    tasks_migrated: 2,
  });
});

describe("SwitchAgentRuntimeDialog — one dialog for one agent and for many", () => {
  it("names the agent when a single agent is passed and counts them when several are", () => {
    const single = renderDialog([makeAgent("a")]);
    expect(screen.getByText(/Switch runtime for "Agent a"/)).toBeTruthy();
    single.unmount();

    renderDialog([makeAgent("a"), makeAgent("b")]);
    expect(screen.getByText(/Switch runtime for 2 agents/)).toBeTruthy();
  });

  it("submits the same request shape for one agent and for many", async () => {
    const single = renderDialog([makeAgent("a")]);
    fireEvent.click(screen.getByText("pick-target"));
    // Confirm only enables once the preview lands — that is the point of the
    // gate, so the test waits for the rendered summary rather than the call.
    await screen.findByText(/2 queued tasks move to the new runtime/);
    fireEvent.click(screen.getByRole("button", { name: "Switch runtime" }));
    await waitFor(() => expect(mutateAsyncSpy).toHaveBeenCalled());
    expect(mutateAsyncSpy.mock.calls[0]?.[0]).toMatchObject({
      targetRuntimeId: "rt-target",
      agentIds: ["a"],
    });
    single.unmount();

    mutateAsyncSpy.mockClear();
    renderDialog([makeAgent("a"), makeAgent("b")]);
    fireEvent.click(screen.getByText("pick-target"));
    await screen.findByText(/2 queued tasks move to the new runtime/);
    fireEvent.click(screen.getByRole("button", { name: "Switch runtime" }));
    await waitFor(() => expect(mutateAsyncSpy).toHaveBeenCalled());
    expect(mutateAsyncSpy.mock.calls[0]?.[0]).toMatchObject({
      agentIds: ["a", "b"],
    });
  });
});

describe("SwitchAgentRuntimeDialog — consequence summary", () => {
  it("asks the server for the task split instead of deriving it locally", async () => {
    renderDialog([makeAgent("a")]);
    // Nothing is knowable before a target is chosen, so no preview until then.
    expect(migrateSpy).not.toHaveBeenCalled();

    fireEvent.click(screen.getByText("pick-target"));
    await waitFor(() => expect(migrateSpy).toHaveBeenCalled());
    expect(migrateSpy.mock.calls[0]?.[0]).toBe("rt-target");
    expect(migrateSpy.mock.calls[0]?.[1]).toMatchObject({ dry_run: true });
  });

  it("separates tasks that move from tasks that stay with a running daemon", async () => {
    renderDialog([makeAgent("a")]);
    fireEvent.click(screen.getByText("pick-target"));

    // The two groups are stated separately and never summed: 'queued' and
    // 'deferred' travel, while 'dispatched' / 'running' /
    // 'waiting_local_directory' finish where they are.
    expect(
      await screen.findByText(/2 queued tasks move to the new runtime/),
    ).toBeTruthy();
    expect(
      screen.getByText(/3 running tasks stay on their current runtime/),
    ).toBeTruthy();
  });

  it("names the model settings it is about to discard", async () => {
    renderDialog([makeAgent("a")]);
    fireEvent.click(screen.getByText("pick-target"));

    expect(
      await screen.findByText(/These runtime-specific settings will be cleared/),
    ).toBeTruthy();
    expect(screen.getByText(/claude-opus-4 · high/)).toBeTruthy();
  });

  it("warns that some selected agents will be skipped", async () => {
    migrateSpy.mockResolvedValue({
      ...PREVIEW,
      skipped: [{ agent_id: "b", name: "Agent b", reason: "forbidden" }],
    });
    renderDialog([makeAgent("a"), makeAgent("b")]);
    fireEvent.click(screen.getByText("pick-target"));

    expect(
      await screen.findByText(/1 selected agent will be skipped/),
    ).toBeTruthy();
  });

  it("keeps confirm disabled when nothing in the selection can move", async () => {
    migrateSpy.mockResolvedValue({
      ...PREVIEW,
      migrated: [],
      skipped: [{ agent_id: "a", name: "Agent a", reason: "already_on_target" }],
      tasks_migrated: 0,
      tasks_staying_active: 0,
    });
    renderDialog([makeAgent("a")]);
    fireEvent.click(screen.getByText("pick-target"));

    expect(await screen.findByText(/Nothing to move/)).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Switch runtime" }).hasAttribute("disabled"),
    ).toBe(true);
  });

  it("requires a target before it will submit anything", () => {
    renderDialog([makeAgent("a")]);
    expect(
      screen.getByRole("button", { name: "Switch runtime" }).hasAttribute("disabled"),
    ).toBe(true);
  });
});

describe("SwitchAgentRuntimeDialog — Runtime detail entry point", () => {
  it("forwards the source runtime so the server can refuse a drifted plan", async () => {
    renderDialog([makeAgent("a")], { expectedSourceRuntimeId: "rt-source" });
    fireEvent.click(screen.getByText("pick-target"));
    await screen.findByText(/2 queued tasks move to the new runtime/);
    fireEvent.click(screen.getByRole("button", { name: "Switch runtime" }));

    await waitFor(() => expect(mutateAsyncSpy).toHaveBeenCalled());
    expect(mutateAsyncSpy.mock.calls[0]?.[0]).toMatchObject({
      expectedSourceRuntimeId: "rt-source",
    });
  });

  it("re-asks the server when the user excludes an agent from the selection", async () => {
    renderDialog([makeAgent("a"), makeAgent("b")]);
    fireEvent.click(screen.getByText("pick-target"));
    await waitFor(() => expect(migrateSpy).toHaveBeenCalledTimes(1));

    // Unchecking must invalidate the previous counts rather than leaving the
    // dialog describing a set the user has since changed.
    fireEvent.click(screen.getByText("Agent b"));
    await waitFor(() => expect(migrateSpy).toHaveBeenCalledTimes(2));
    expect(migrateSpy.mock.calls[1]?.[1]).toMatchObject({ agent_ids: ["a"] });
  });
});
