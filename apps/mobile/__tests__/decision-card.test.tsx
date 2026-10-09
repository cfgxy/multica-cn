import React from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { IssueDecision } from "@multica/core/types";

const mockAnswer = jest.fn();
const mockCancel = jest.fn();
const mockListMembers = jest.fn();
const mockListAgents = jest.fn();
const mockListSquads = jest.fn();

jest.mock("@/data/api", () => ({
  api: {
    answerIssueDecision: (...args: unknown[]) => mockAnswer(...args),
    cancelIssueDecision: (...args: unknown[]) => mockCancel(...args),
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
  useT: () => ({ t: (_key: string, fallback?: string) => fallback ?? _key }),
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
});

describe("DecisionCard", () => {
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
});
