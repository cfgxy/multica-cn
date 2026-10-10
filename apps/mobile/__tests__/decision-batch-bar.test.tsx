import React from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react-native";
import { Alert } from "react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { IssueDecision } from "@multica/core/types";
import { issueKeys } from "@multica/core/issues/queries";

const mockBatch = jest.fn();

jest.mock("@/data/api", () => ({
  api: {
    answerIssueDecisionsBatch: (...args: unknown[]) => mockBatch(...args),
  },
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (_key: string, fallback?: string) => fallback ?? _key }),
}));

jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light" }),
}));

jest.mock("@expo/vector-icons", () => ({
  Ionicons: () => null,
}));

// The ui primitives pull in @rn-primitives/slot, whose dist ships raw JSX the
// jest transform never sees — stand in plain RN equivalents (decision-card
// test pattern).
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

import { DecisionBatchBar } from "@/components/issue/decision-batch-bar";

function card(id: string, overrides: Partial<IssueDecision> = {}): IssueDecision {
  return {
    id,
    issue_id: "issue-1",
    source_comment_id: null,
    question: `Question ${id}?`,
    options: [{ label: "Alpha" }, { label: "Beta" }],
    multi_select: false,
    recommended_indices: [],
    status: "open",
    selected_indices: [],
    answered_by_type: null,
    answered_by_id: null,
    answered_at: null,
    answer_comment_id: null,
    created_by_type: "agent",
    created_by_id: "a-1",
    created_at: "2026-10-02T03:00:00Z",
    updated_at: "2026-10-02T03:00:00Z",
    ...overrides,
  };
}

async function renderBar(open: IssueDecision[]) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  qc.setQueryData(issueKeys.decisions("issue-1"), open);
  await render(
    <QueryClientProvider client={qc}>
      <DecisionBatchBar issueId="issue-1" open={open} />
    </QueryClientProvider>,
  );
  return qc;
}

// RN normalizes accessibilityState to the full shape, so assert fields.
function optionState(testId: string) {
  return screen.getByTestId(testId).props.accessibilityState as {
    checked?: boolean;
    disabled?: boolean;
  };
}

function submitDisabled() {
  return (screen.getByTestId("decision-batch-submit").props.accessibilityState as { disabled?: boolean }).disabled;
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
  mockBatch.mockReset();
});

describe("DecisionBatchBar", () => {
  // RUYI-575: labels written under the decision-numbering convention embed the
  // letter itself ("A：…") while the bar renders its own index letter — the
  // prefix must be stripped or the letter shows twice (RUYI-572 real card).
  it("strips the embedded letter prefix so each letter renders once", async () => {
    await renderBar([
      card("d-572", {
        question: "wiki/docx 链接的降级文案范围？（详见 ADR 005 §8 决策 2）",
        options: [
          { label: "A：维持现状，wiki/docx 链接不注入提示（仅 bare /file/ 引述块有 note）" },
          { label: "B：note 扩展到 wiki/docx 链接（请以附件形式发送类提示）" },
        ],
      }),
    ]);

    expect(screen.getByText("维持现状，wiki/docx 链接不注入提示（仅 bare /file/ 引述块有 note）")).toBeTruthy();
    expect(screen.queryByText("A：维持现状，wiki/docx 链接不注入提示（仅 bare /file/ 引述块有 note）")).toBeNull();
    expect(screen.getByText("note 扩展到 wiki/docx 链接（请以附件形式发送类提示）")).toBeTruthy();
    expect(screen.queryByText("B：note 扩展到 wiki/docx 链接（请以附件形式发送类提示）")).toBeNull();
  });

  it("renders labels without a matching prefix form untouched", async () => {
    await renderBar([card("d-neg", { options: [{ label: "A-type 优先" }, { label: "B超 声呐" }] })]);

    expect(screen.getByText("A-type 优先")).toBeTruthy();
    expect(screen.getByText("B超 声呐")).toBeTruthy();
  });

  it("renders one numbered row per open card and disables submit until a pick", async () => {
    await renderBar([card("d-1"), card("d-2")]);

    expect(screen.getByTestId("decision-batch-bar")).toBeTruthy();
    expect(screen.getByText("2")).toBeTruthy();
    expect(screen.getByText("Question d-1?")).toBeTruthy();
    expect(screen.getByText("Question d-2?")).toBeTruthy();
    expect(submitDisabled()).toBe(true);

    await pressPressable("decision-batch-option-d-1-0");
    await waitFor(() => expect(optionState("decision-batch-option-d-1-0").checked).toBe(true));
    expect(submitDisabled()).toBe(false);
  });

  it("single-select rows replace the pick", async () => {
    await renderBar([card("d-1"), card("d-2")]);

    await pressPressable("decision-batch-option-d-1-0");
    await pressPressable("decision-batch-option-d-1-1");
    await waitFor(() => expect(optionState("decision-batch-option-d-1-0").checked).toBe(false));
    await waitFor(() => expect(optionState("decision-batch-option-d-1-1").checked).toBe(true));
  });

  it("submits all picked cards in one batch call and patches the cache", async () => {
    const open = [card("d-1"), card("d-2")];
    mockBatch.mockResolvedValue({
      results: [
        { decision_id: "d-1", status: "answered", decision: { ...open[0]!, status: "answered", selected_indices: [0] } },
        { decision_id: "d-2", status: "answered", decision: { ...open[1]!, status: "answered", selected_indices: [1] } },
      ],
      echo_comment_id: "c-echo",
    });
    const qc = await renderBar(open);

    await pressPressable("decision-batch-option-d-1-0");
    await pressPressable("decision-batch-option-d-2-1");
    await pressPressable("decision-batch-submit");

    await waitFor(() =>
      expect(mockBatch).toHaveBeenCalledWith("issue-1", [
        { decision_id: "d-1", selected_indices: [0] },
        { decision_id: "d-2", selected_indices: [1] },
      ]),
    );
    await waitFor(() => {
      const cached = qc.getQueryData<IssueDecision[]>(issueKeys.decisions("issue-1"));
      expect(cached?.[0]?.status).toBe("answered");
      expect(cached?.[1]?.status).toBe("answered");
    });
  });

  it("multi-select rows toggle sorted; unpicked cards stay out of the request", async () => {
    mockBatch.mockResolvedValue({
      results: [
        { decision_id: "d-1", status: "answered", decision: card("d-1", { status: "answered" }) },
      ],
    });
    await renderBar([
      card("d-1", { multi_select: true, options: [{ label: "one" }, { label: "two" }, { label: "three" }] }),
      card("d-2"),
    ]);

    expect(screen.getByTestId("decision-batch-option-d-1-0").props.accessibilityRole).toBe("checkbox");

    await pressPressable("decision-batch-option-d-1-2");
    await pressPressable("decision-batch-option-d-1-0");
    await pressPressable("decision-batch-submit");

    await waitFor(() =>
      expect(mockBatch).toHaveBeenCalledWith("issue-1", [
        { decision_id: "d-1", selected_indices: [0, 2] },
      ]),
    );
  });

  it("per-card failure alerts, patches the answered half, and never throws", async () => {
    const alert = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    mockBatch.mockResolvedValue({
      results: [
        { decision_id: "d-1", status: "answered", decision: card("d-1", { status: "answered", selected_indices: [0] }) },
        { decision_id: "d-2", status: "conflict", error: "already answered" },
      ],
    });
    const qc = await renderBar([card("d-1"), card("d-2")]);

    await pressPressable("decision-batch-option-d-1-0");
    await pressPressable("decision-batch-submit");

    await waitFor(() => expect(alert).toHaveBeenCalled());
    await waitFor(() => {
      const cached = qc.getQueryData<IssueDecision[]>(issueKeys.decisions("issue-1"));
      expect(cached?.[0]?.status).toBe("answered");
    });
    alert.mockRestore();
  });

  // RUYI-620: legacy data written under the degraded "A 选项文本" form
  // (letter + space, half- or full-width) must not double-number against
  // the bar's own index letter, same as the single card.
  it("strips legacy letter+space prefixes so each letter renders once", async () => {
    await renderBar([
      card("d-620", { options: [{ label: "A 存量方案一" }, { label: "B　存量方案二" }] }),
    ]);

    expect(screen.getByText("A")).toBeTruthy();
    expect(screen.getByText("B")).toBeTruthy();
    expect(screen.getByText("存量方案一")).toBeTruthy();
    expect(screen.getByText("存量方案二")).toBeTruthy();
    expect(screen.queryByText("A 存量方案一")).toBeNull();
    expect(screen.queryByText("B　存量方案二")).toBeNull();
  });

  // RUYI-620: trailing "（推荐）" is stripped by the shared display helper;
  // labels without it render as-is.
  it("strips the trailing recommended marker from option text", async () => {
    await renderBar([
      card("d-621", { options: [{ label: "方案一（推荐）" }, { label: "方案二" }] }),
    ]);

    expect(screen.getByText("方案一")).toBeTruthy();
    expect(screen.queryByText("方案一（推荐）")).toBeNull();
    expect(screen.getByText("方案二")).toBeTruthy();
  });
});
