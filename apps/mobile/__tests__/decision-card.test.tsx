import React from "react";
import { Alert } from "react-native";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { IssueDecision } from "@multica/core/types";
import { issueKeys } from "@multica/core/issues/queries";

const mockAnswer = jest.fn();
const mockCancel = jest.fn();
const mockRestore = jest.fn();
const mockListMembers = jest.fn();
const mockListAgents = jest.fn();
const mockListSquads = jest.fn();

jest.mock("@/data/api", () => ({
  api: {
    answerIssueDecision: (...args: unknown[]) => mockAnswer(...args),
    cancelIssueDecision: (...args: unknown[]) => mockCancel(...args),
    restoreIssueDecision: (...args: unknown[]) => mockRestore(...args),
    listMembers: (...args: unknown[]) => mockListMembers(...args),
    listAgents: (...args: unknown[]) => mockListAgents(...args),
    listSquads: (...args: unknown[]) => mockListSquads(...args),
  },
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (state: { currentWorkspaceId: string }) => unknown) =>
    selector({ currentWorkspaceId: "workspace-1" }),
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (_key: string, fallback?: string, options?: Record<string, string>) =>
      (fallback ?? _key).replace(/\{\{(\w+)\}\}/g, (_match, name: string) => options?.[name] ?? ""),
  }),
}));

jest.mock("@/lib/time-ago", () => ({
  timeAgo: () => "2h",
}));

jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light" }),
}));

jest.mock("@expo/vector-icons", () => ({
  Ionicons: () => null,
}));

// The ui primitives pull in @rn-primitives/slot, whose dist ships raw JSX the
// jest transform never sees — stand in plain RN equivalents (inbox-row pattern).
jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});
jest.mock("@/components/ui/button", () => {
  const { Pressable } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    Button: ({ variant: _v, size: _s, ...props }: React.ComponentProps<typeof Pressable> & { variant?: string; size?: string }) => (
      <Pressable {...props} />
    ),
  };
});
jest.mock("@/components/ui/card", () => {
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Card: ({ children }: { children: React.ReactNode }) => <View>{children}</View> };
});
jest.mock("@/lib/utils", () => ({
  cn: (...classes: unknown[]) => classes.filter(Boolean).join(" "),
}));

import { DecisionCard } from "@/components/issue/decision-card";

function card(overrides: Partial<IssueDecision> = {}): IssueDecision {
  return {
    id: "d-1",
    issue_id: "issue-1",
    source_comment_id: null,
    question: "Ship now or wait?",
    options: [{ label: "Ship" }, { label: "Wait" }],
    multi_select: false,
    recommended_indices: [],
    status: "open",
    selected_indices: [],
    answered_by_type: null,
    answered_by_id: null,
    answered_at: null,
    answer_comment_id: null,
    created_by_type: "agent",
    created_by_id: "agent-1",
    created_at: "2026-10-02T03:00:00Z",
    updated_at: "2026-10-02T03:00:00Z",
    ...overrides,
  };
}

async function renderCard(decision: IssueDecision) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  await render(
    <QueryClientProvider client={queryClient}>
      <DecisionCard decision={decision} />
    </QueryClientProvider>,
  );
  return queryClient;
}

// RN normalizes accessibilityState to the full shape (busy/expanded/selected
// always present), so assert fields instead of deep equality.
function optionState(idx: number) {
  return screen.getByTestId(`decision-option-${idx}`).props.accessibilityState as {
    checked?: boolean;
    disabled?: boolean;
  };
}

function submitDisabled() {
  // Pressable consumes `disabled` and exposes it via accessibilityState on
  // the host view — props.disabled is gone by render time.
  return (screen.getByTestId("decision-submit").props.accessibilityState as { disabled?: boolean }).disabled;
}

// Serialised press: a bare fireEvent inside an async test can overlap
// React's act scope (RNTL v14 + React 19) and silently break the NEXT
// render's commit. Awaiting the act wrapper keeps the scope stack clean.
async function pressPressable(testId: string) {
  await act(async () => {
    fireEvent.press(screen.getByTestId(testId));
  });
}

beforeEach(() => {
  jest.clearAllMocks();
  mockListMembers.mockResolvedValue([{ user_id: "member-1", name: "Owner" }]);
  mockListAgents.mockResolvedValue([{ id: "agent-1", name: "Helper" }]);
  mockListSquads.mockResolvedValue([]);
  mockAnswer.mockImplementation((_issueId: string, _id: string, picks: number[]) =>
    Promise.resolve(card({ status: "answered", selected_indices: picks, answered_by_type: "member", answered_by_id: "member-1" })),
  );
  mockCancel.mockResolvedValue(card({ status: "cancelled" }));
  mockRestore.mockResolvedValue(card({ status: "open" }));
});

describe("DecisionCard", () => {
  it("renders native decision context only on question cards", async () => {
    await renderCard(card({
      context: {
        why_now: "The release window closes Friday.",
        facts: [{ statement: "The build passed.", evidence: ["CI run 12"] }],
        impact: "A delay moves launch one week.",
        recommendation_reason: "Monday leaves rollback time.",
        unknowns: ["External review timing"],
      },
    }));

    const context = screen.getByTestId("decision-context");
    expect(context).toBeTruthy();
    expect(context.props.children).toBeTruthy();
    expect(screen.getByText("The release window closes Friday.")).toBeTruthy();
    expect(screen.getByText(/The build passed\.\s+CI run 12/)).toBeTruthy();
  });

  // RUYI-588: the single card renders its own index letter per row — the
  // same letter language as the batch bar (RUYI-471) — so cards whose labels
  // no longer embed an "A：" prefix still show A/B/C/D.
  it("renders an index letter per option row", async () => {
    await renderCard(card());

    expect(screen.getByText("A")).toBeTruthy();
    expect(screen.getByText("B")).toBeTruthy();
    expect(screen.getByText("Ship")).toBeTruthy();
    expect(screen.getByText("Wait")).toBeTruthy();
  });

  // RUYI-575's guard, single-card side: labels written under the
  // decision-numbering convention embed the letter itself ("A：…") — the
  // card's own index letter must not double it.
  it("strips the embedded letter prefix so each letter renders once", async () => {
    await renderCard(
      card({ options: [{ label: "A：立即发布" }, { label: "B：观察一周" }] }),
    );

    expect(screen.getByText("A")).toBeTruthy();
    expect(screen.getByText("B")).toBeTruthy();
    expect(screen.getByText("立即发布")).toBeTruthy();
    expect(screen.getByText("观察一周")).toBeTruthy();
    expect(screen.queryByText("A：立即发布")).toBeNull();
    expect(screen.queryByText("B：观察一周")).toBeNull();
  });

  it("renders labels without a matching prefix form untouched", async () => {
    await renderCard(
      card({ options: [{ label: "A-type 优先" }, { label: "B超 声呐" }] }),
    );

    expect(screen.getByText("A")).toBeTruthy();
    expect(screen.getByText("B")).toBeTruthy();
    expect(screen.getByText("A-type 优先")).toBeTruthy();
    expect(screen.getByText("B超 声呐")).toBeTruthy();
  });

  // RUYI-620: legacy data written under the degraded "A 选项文本" form
  // (letter + space, half- or full-width) must not double-number against
  // the card's own index letter.
  it("strips legacy letter+space prefixes so each letter renders once", async () => {
    await renderCard(
      card({ options: [{ label: "A 存量方案一" }, { label: "B　存量方案二" }] }),
    );

    expect(screen.getByText("A")).toBeTruthy();
    expect(screen.getByText("B")).toBeTruthy();
    expect(screen.getByText("存量方案一")).toBeTruthy();
    expect(screen.getByText("存量方案二")).toBeTruthy();
    expect(screen.queryByText("A 存量方案一")).toBeNull();
    expect(screen.queryByText("B　存量方案二")).toBeNull();
  });

  // RUYI-620: the badge renders from recommended_indices, so a trailing
  // "（推荐）" in the label text must be stripped; leading occurrences are
  // content and stay.
  it("strips the trailing recommended marker; the badge still renders once", async () => {
    await renderCard(
      card({
        options: [{ label: "方案一（推荐）" }, { label: "方案二" }, { label: "（推荐）开头不算" }],
        recommended_indices: [0],
      }),
    );

    expect(screen.getByText("方案一")).toBeTruthy();
    expect(screen.queryByText("方案一（推荐）")).toBeNull();
    expect(screen.getByText("Recommended")).toBeTruthy();
    expect(screen.getByText("方案二")).toBeTruthy();
    expect(screen.getByText("（推荐）开头不算")).toBeTruthy();
  });

  it("renders the question, options and creator for an open single-select card", async () => {
    await renderCard(card({ recommended_indices: [1] }));

    expect(screen.getByText("Ship now or wait?")).toBeTruthy();
    expect(screen.getByText("Decision card")).toBeTruthy();
    expect(screen.getByTestId("decision-option-0")).toBeTruthy();
    expect(screen.getByTestId("decision-option-1")).toBeTruthy();
    // Recommended chip only on the recommended option.
    expect(screen.getAllByText("Recommended")).toHaveLength(1);
    expect(optionState(0).disabled).toBe(false);

    await waitFor(() => expect(screen.getByText(/Helper/)).toBeTruthy());
  });

  it("submits a single-select pick, replacing the previous one", async () => {
    await renderCard(card());

    await pressPressable("decision-option-0");
    await waitFor(() => expect(optionState(0).checked).toBe(true));

    await pressPressable("decision-option-1");
    await waitFor(() => expect(optionState(0).checked).toBe(false));
    await waitFor(() => expect(optionState(1).checked).toBe(true));

    await pressPressable("decision-submit");
    await waitFor(() =>
      expect(mockAnswer).toHaveBeenCalledWith("issue-1", "d-1", [1]),
    );
  });

  it("toggles picks independently on multi-select and submits both", async () => {
    await renderCard(card({ multi_select: true }));

    expect(
      screen.getByTestId("decision-option-0").props.accessibilityRole,
    ).toBe("checkbox");

    await pressPressable("decision-option-0");
    await pressPressable("decision-option-1");
    await waitFor(() => expect(optionState(0).checked).toBe(true));
    await waitFor(() => expect(optionState(1).checked).toBe(true));

    await pressPressable("decision-submit");
    await waitFor(() =>
      expect(mockAnswer).toHaveBeenCalledWith("issue-1", "d-1", [0, 1]),
    );
  });

  it("keeps the submit button disabled until something is picked", async () => {
    await renderCard(card());

    expect(submitDisabled()).toBe(true);
    await pressPressable("decision-submit");
    expect(mockAnswer).not.toHaveBeenCalled();
  });

  it("renders an answered card read-only with the picked option marked", async () => {
    await renderCard(
      card({
        status: "answered",
        selected_indices: [1],
        answered_by_type: "member",
        answered_by_id: "member-1",
        answered_at: "2026-10-02T05:00:00Z",
      }),
    );

    expect(screen.getByText("Answered")).toBeTruthy();
    expect(screen.getByText("B")).toBeTruthy();
    expect(screen.queryByTestId("decision-submit")).toBeNull();
    expect(screen.queryByTestId("decision-cancel")).toBeNull();
    expect(optionState(1)).toMatchObject({ checked: true, disabled: true });
    expect(optionState(0)).toMatchObject({ checked: false, disabled: true });

    await waitFor(() => expect(screen.getByText(/Owner/)).toBeTruthy());
  });

  it("renders a cancelled card read-only", async () => {
    await renderCard(card({ status: "cancelled" }));

    expect(screen.getByText("Cancelled")).toBeTruthy();
    expect(screen.queryByTestId("decision-submit")).toBeNull();
    expect(screen.queryByTestId("decision-cancel")).toBeNull();
    expect(optionState(0).disabled).toBe(true);
    expect(optionState(1).disabled).toBe(true);
  });

  it("cancels an open card via the cancel button", async () => {
    await renderCard(card());

    await pressPressable("decision-cancel");
    await waitFor(() =>
      expect(mockCancel).toHaveBeenCalledWith("issue-1", "d-1"),
    );
  });

  it("restores a cancelled card from the header more affordance (RUYI-863)", async () => {
    const qc = await renderCard(card({ status: "cancelled" }));
    // Seed the list cache so the upsert has a prior entry to patch.
    qc.setQueryData<IssueDecision[]>(issueKeys.decisions("issue-1"), [
      card({ status: "cancelled" }),
    ]);

    // The "···" affordance exists ONLY on cancelled cards.
    expect(screen.queryByTestId("decision-card-more")).not.toBeNull();
    // Capture the confirm button; do NOT fire it inside pressPressable's
    // act scope — the mutation's TanStack notification batch would escape
    // the act and break the next test's render commit (file-top note).
    let confirmButton: { onPress?: () => void } | undefined;
    const restoreAlert = jest.spyOn(Alert, "alert").mockImplementation(
      (_title: string, _message?: string, buttons?: { onPress?: () => void }[]) => {
        confirmButton = buttons?.[1];
      },
    );

    await pressPressable("decision-card-more");

    expect(restoreAlert).toHaveBeenCalled();
    await act(async () => {
      confirmButton?.onPress?.();
    });
    await waitFor(() =>
      expect(mockRestore).toHaveBeenCalledWith("issue-1", "d-1"),
    );
    // The server's flipped card lands in the decisions cache.
    await waitFor(() => {
      const cached = qc.getQueryData<IssueDecision[]>(
        issueKeys.decisions("issue-1"),
      );
      expect(cached?.[0]?.status).toBe("open");
    });
    restoreAlert.mockRestore();
  });

  it("hides the more affordance on open cards", async () => {
    await renderCard(card());
    expect(screen.queryByTestId("decision-card-more")).toBeNull();
  });

  it("hides the more affordance on answered cards", async () => {
    await renderCard(
      card({
        status: "answered",
        selected_indices: [0],
        answered_by_type: "member",
        answered_by_id: "member-1",
      }),
    );
    expect(screen.queryByTestId("decision-card-more")).toBeNull();
  });
});

describe("DecisionCard authorization face (RUYI-630)", () => {
  const authCard = card({
    decision_kind: "authorization",
    action_type: "prompt_restore",
    approve_label: "Restore",
    deny_label: "Keep",
  });

  it("renders the two fixed buttons with custom labels and no option rows, submit or cancel", async () => {
    await renderCard(authCard);
    expect(screen.getByTestId("decision-auth-approve")).toBeTruthy();
    expect(screen.getByTestId("decision-auth-deny")).toBeTruthy();
    expect(screen.getByText("Restore")).toBeTruthy();
    expect(screen.getByText("Keep")).toBeTruthy();
    expect(screen.queryByTestId("decision-option-0")).toBeNull();
    expect(screen.queryByTestId("decision-submit")).toBeNull();
    expect(screen.queryByTestId("decision-cancel")).toBeNull();
  });

  it("shows each project result after project-prompt execution", async () => {
    await renderCard(card({
      ...authCard,
      action_type: "project_prompt_sync",
      status: "answered",
      auth_state: "executed",
      executed_at: "2026-10-21T12:00:00Z",
      execution_result: {
        action: "project_prompt_sync",
        projects: [{ project_id: "project-1", status: "updated", version: 4 }],
      },
    }));

    expect(screen.getByTestId("decision-auth-project-result").props.children).toContain("project-1");
  });

  it("shows the one created issue and its in-progress state", async () => {
    await renderCard(card({
      ...authCard,
      action_type: "single_issue_create",
      status: "answered",
      auth_state: "executed",
      executed_at: "2026-10-21T12:00:00Z",
      execution_result: {
        target_issue_id: "issue-12",
        target_identifier: "RUYI-12",
        target_issue_status: "in_progress",
      },
    }));

    expect(screen.getByTestId("decision-auth-created-issue").props.children).toContain("RUYI-12");
  });

  it("announces unknown side effects and does not display the internal error code", async () => {
    await renderCard(card({
      ...authCard,
      status: "answered",
      auth_state: "execute_failed",
      execution_error: "PROMPT_SYNC_FAILED",
      execution_result: { receipt_version: 1, side_effect_status: "unknown", steps: [] },
    }));

    expect(screen.getByText("Result needs verification. Do not retry this request.")).toBeTruthy();
    expect(screen.queryByText("PROMPT_SYNC_FAILED")).toBeNull();
  });

  it("answers through the issue decision endpoint with the approve index", async () => {
    await renderCard(authCard);
    await pressPressable("decision-auth-approve");
    expect(mockAnswer).toHaveBeenCalledWith("issue-1", "d-1", [0]);
  });

  it("hides the buttons and shows the answered state once answered", async () => {
    await renderCard(
      card({
        ...authCard,
        status: "answered",
        auth_state: "approved",
        answered_by_type: "member",
        answered_by_id: "member-1",
        answered_at: "2026-10-02T04:00:00Z",
      }),
    );
    expect(screen.queryByTestId("decision-auth-approve")).toBeNull();
    expect(screen.queryByTestId("decision-auth-deny")).toBeNull();
    // The answered line is one merged text node ("Owner · 2h" — timeAgo is
    // mocked), and the directory-backed name resolves asynchronously.
    await waitFor(() => expect(screen.getByText("Owner · 2h")).toBeTruthy());
  });

  it("shows the execution failure line on execute_failed", async () => {
    await renderCard(
      card({
        ...authCard,
        status: "answered",
        auth_state: "approved",
        execution_error: "modified",
      }),
    );
    expect(screen.getByText("Execution failed; it was not retried")).toBeTruthy();
  });
});
