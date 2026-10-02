// @vitest-environment jsdom

import { cleanup, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentTask, RunDetail } from "@multica/core/types";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderWithI18n } from "../../test/i18n";

const mockState = vi.hoisted(() => ({
  getIssueRun: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: { getIssueRun: mockState.getIssueRun },
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getMemberName: (userId: string) => (userId === "user-alice" ? "Alice" : "Unknown"),
    getAgentName: () => "Fixer",
    getSquadName: () => "Unknown Squad",
    getActorName: () => "Unknown",
    getActorInitials: () => "U",
    getActorAvatarUrl: () => null,
    hasActor: () => true,
  }),
}));

vi.mock("@multica/ui/components/ui/sheet", () => ({
  Sheet: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  SheetContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  SheetHeader: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  SheetTitle: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <span data-testid="actor-avatar" />,
}));

import { RunDetailDrawer } from "./run-detail-drawer";

function makeTask(overrides: Partial<AgentTask> = {}): AgentTask {
  return {
    id: "run-2",
    agent_id: "agent-1",
    runtime_id: "runtime-1",
    issue_id: "issue-1",
    status: "failed",
    priority: 0,
    attempt: 2,
    dispatched_at: null,
    started_at: "2026-06-08T08:00:00Z",
    completed_at: "2026-06-08T08:04:00Z",
    result: null,
    error: "provider returned 402",
    failure_reason: "timeout",
    created_at: "2026-06-08T08:00:00Z",
    trigger_summary: "Fix the login flow",
    ...overrides,
  };
}

// cancelled source → manual rerun (failed, the drawer's run) → system retry
// (still queued). One mixed chain exercises both lineage-edge labels.
function runDetail(overrides: Partial<RunDetail> = {}): RunDetail {
  return {
    task: makeTask({ rerun_of_task_id: "run-1" }),
    ancestors: [
      {
        id: "run-1",
        agent_id: "agent-1",
        status: "cancelled",
        attempt: 1,
        created_at: "2026-06-08T07:50:00Z",
        completed_at: "2026-06-08T07:55:00Z",
        cancel_requested_by_user_id: "user-alice",
      },
    ],
    descendants: [
      {
        id: "run-3",
        agent_id: "agent-1",
        status: "queued",
        attempt: 1,
        created_at: "2026-06-08T08:06:00Z",
        retry_of_task_id: "run-2",
      },
    ],
    ...overrides,
  };
}

function renderDrawer(task: AgentTask | null, detail = runDetail()) {
  mockState.getIssueRun.mockResolvedValue(detail);
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return renderWithI18n(
    <QueryClientProvider client={queryClient}>
      <RunDetailDrawer issueId="issue-1" task={task} onOpenChange={() => {}} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

describe("RunDetailDrawer", () => {
  it("renders the run, its human failure reason, and its raw error", async () => {
    renderDrawer(makeTask());

    expect(await screen.findByText("Fix the login flow")).toBeInTheDocument();
    expect(await screen.findByText("Task timed out")).toBeInTheDocument();
    // The raw diagnostic is a deliberate part of the detail surface — the
    // row tooltip keeps it out (#7411), the drawer owns it.
    expect(screen.getByText("provider returned 402")).toBeInTheDocument();
    expect(screen.getByText("Fixer")).toBeInTheDocument();
  });

  it("renders the mixed retry chain with both lineage-edge labels", async () => {
    renderDrawer(makeTask());

    // The cancelled ancestor is linked by THIS run's rerun_of_task_id.
    expect(await screen.findByText("Manual rerun")).toBeInTheDocument();
    // The queued descendant names its own system-retry edge.
    expect(screen.getByText("System retry")).toBeInTheDocument();
  });

  it("names the member who asked to stop a cancelled run", async () => {
    renderDrawer(
      makeTask({
        status: "cancelled",
        cancel_requested_by_user_id: "user-alice",
        cancel_requested_at: "2026-06-08T07:54:00Z",
        failure_reason: undefined,
        error: null,
        rerun_of_task_id: undefined,
      }),
      runDetail({ descendants: [] }),
    );

    expect(
      await screen.findByText(/Alice/),
    ).toBeInTheDocument();
    expect(screen.getByText(/Stop requested by/)).toBeInTheDocument();
  });

  it("renders nothing for a closed drawer", () => {
    renderDrawer(null);
    expect(screen.queryByText("Fix the login flow")).not.toBeInTheDocument();
    expect(mockState.getIssueRun).not.toHaveBeenCalled();
  });
});
