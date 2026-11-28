import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import type { IssueLedger } from "@multica/core/types";
import { RESOURCES } from "../../test/i18n";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import { TaskLedgerSection } from "./task-ledger-section";

const mockLedgerData: IssueLedger = {
  acs: [
    {
      id: "ac-1",
      workspace_id: "ws-1",
      issue_id: "issue-1",
      key: "AC1",
      title: "AC1 can be extracted and verified independently",
      status: "uncompleted",
      evidence: [],
      rejection_reason: "",
      position: 1,
      created_at: "2026-11-28T09:00:00Z",
      updated_at: "2026-11-28T09:00:00Z",
    },
    {
      id: "ac-2",
      workspace_id: "ws-1",
      issue_id: "issue-1",
      key: "AC2",
      title: "TODO lifecycle",
      status: "verified",
      evidence: [{ type: "test", note: "vitest passed" }],
      rejection_reason: "",
      position: 2,
      created_at: "2026-11-28T09:00:00Z",
      updated_at: "2026-11-28T09:00:00Z",
    },
    {
      id: "ac-3",
      workspace_id: "ws-1",
      issue_id: "issue-1",
      key: "AC3",
      title: "TODO completion does not auto verify AC",
      status: "rejected",
      evidence: [],
      rejection_reason: "Missing verification evidence",
      position: 3,
      created_at: "2026-11-28T09:00:00Z",
      updated_at: "2026-11-28T09:00:00Z",
    },
  ],
  todos: [
    {
      id: "todo-1",
      workspace_id: "ws-1",
      issue_id: "issue-1",
      title: "Write migration files",
      status: "completed",
      next_step: "",
      blocked_reason: "",
      ac_ids: ["AC1"],
      evidence: [],
      position: 1,
      created_at: "2026-11-28T09:00:00Z",
      updated_at: "2026-11-28T09:00:00Z",
    },
    {
      id: "todo-2",
      workspace_id: "ws-1",
      issue_id: "issue-1",
      title: "Build frontend UI",
      status: "in_progress",
      next_step: "Run typecheck and vitest",
      blocked_reason: "",
      ac_ids: ["AC1", "AC2"],
      evidence: [],
      position: 2,
      created_at: "2026-11-28T09:00:00Z",
      updated_at: "2026-11-28T09:00:00Z",
    },
    {
      id: "todo-3",
      workspace_id: "ws-1",
      issue_id: "issue-1",
      title: "Verify E2E flow",
      status: "blocked",
      next_step: "",
      blocked_reason: "Waiting for backend deployment",
      ac_ids: ["AC3"],
      evidence: [],
      position: 3,
      created_at: "2026-11-28T09:00:00Z",
      updated_at: "2026-11-28T09:00:00Z",
    },
  ],
  audit: [
    {
      id: "audit-1",
      workspace_id: "ws-1",
      issue_id: "issue-1",
      target_type: "ac",
      target_id: "ac-2",
      action: "verified",
      from_status: "pending_verification",
      to_status: "verified",
      actor_type: "agent",
      actor_id: "agent-1",
      evidence: [{ type: "test", note: "vitest passed" }],
      note: "All tests green",
      created_at: "2026-11-28T09:10:00Z",
    },
  ],
  summary: {
    ac_total: 3,
    ac_verified: 1,
    ac_pending: 1,
    ac_rejected: 1,
    todo_total: 3,
    todo_completed: 1,
    todo_in_progress: 1,
    todo_pending: 0,
    todo_blocked: 1,
    current_next_steps: ["Run typecheck and vitest"],
  },
};

const mutateSpies = vi.hoisted(() => ({
  extract: vi.fn(),
  verify: vi.fn(),
  reject: vi.fn(),
  reopen: vi.fn(),
}));

vi.mock("@multica/core/issues", () => ({
  issueLedgerOptions: (id: string) => ({
    queryKey: ["issues", "ledger", id],
    queryFn: () => Promise.resolve(mockLedgerData),
  }),
  useExtractACs: () => ({ mutate: mutateSpies.extract, isPending: false }),
  useCreateAC: () => ({ mutate: vi.fn(), isPending: false }),
  useVerifyAC: () => ({ mutate: mutateSpies.verify, isPending: false }),
  useRejectAC: () => ({ mutate: mutateSpies.reject, isPending: false }),
  useDeleteAC: () => ({ mutate: vi.fn(), isPending: false }),
  useCreateTodo: () => ({ mutate: vi.fn(), isPending: false }),
  useUpdateTodo: () => ({ mutate: vi.fn(), isPending: false }),
  useReopenTodo: () => ({ mutate: mutateSpies.reopen, isPending: false }),
  useDeleteTodo: () => ({ mutate: vi.fn(), isPending: false }),
}));

function makeAdapter(): NavigationAdapter {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (p) => p,
  };
}

function Wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  qc.setQueryData(["issues", "ledger", "issue-1"], mockLedgerData);
  return (
    <I18nProvider locale="en" resources={RESOURCES}>
      <QueryClientProvider client={qc}>
        <WorkspaceSlugProvider slug="acme">
          <NavigationProvider value={makeAdapter()}>{children}</NavigationProvider>
        </WorkspaceSlugProvider>
      </QueryClientProvider>
    </I18nProvider>
  );
}

describe("TaskLedgerSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders ACs, dynamic TODOs and summary metrics", async () => {
    render(
      <Wrapper>
        <TaskLedgerSection issueId="issue-1" description="Some issue text" />
      </Wrapper>,
    );

    // AC items rendered
    expect(screen.getAllByText("AC1").length).toBeGreaterThan(0);
    expect(
      screen.getByText("AC1 can be extracted and verified independently"),
    ).toBeDefined();
    expect(screen.getAllByText("AC2").length).toBeGreaterThan(0);
    expect(screen.getAllByText("AC3").length).toBeGreaterThan(0);

    // Rejection reason rendered for AC3
    expect(screen.getByText(/Missing verification evidence/)).toBeDefined();

    // TODO items rendered
    expect(screen.getByText("Write migration files")).toBeDefined();
    expect(screen.getByText("Build frontend UI")).toBeDefined();
    expect(screen.getByText("Verify E2E flow")).toBeDefined();

    // Next step and Blocked reason rendered
    expect(screen.getByText("Run typecheck and vitest")).toBeDefined();
    expect(screen.getByText(/Waiting for backend deployment/)).toBeDefined();
  });

  it("allows extracting ACs from description", async () => {
    render(
      <Wrapper>
        <TaskLedgerSection issueId="issue-1" description="test description" />
      </Wrapper>,
    );

    const extractBtn = screen.getByText("Extract ACs from description");
    fireEvent.click(extractBtn);

    expect(mutateSpies.extract).toHaveBeenCalledWith({
      description: "test description",
    });
  });

  it("allows reopening completed or blocked TODO", async () => {
    render(
      <Wrapper>
        <TaskLedgerSection issueId="issue-1" />
      </Wrapper>,
    );

    const reopenBtns = screen.getAllByText(/Reopen/i);
    expect(reopenBtns.length).toBeGreaterThan(0);
    fireEvent.click(reopenBtns[0]!);

    expect(mutateSpies.reopen).toHaveBeenCalledWith({ todoId: "todo-1" });
  });
});
