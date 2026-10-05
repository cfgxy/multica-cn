// @ts-nocheck

import React from "react";
import { render } from "@testing-library/react-native";
import type { TaskMessagePayload } from "@multica/core/types";

/**
 * RUYI-444 — the chat "N steps" fold must count the web-chat pipeline output
 * (buildTimeline coalesce + splitTimeline middle), not the raw payload stream.
 * Same scenario as lib/chat-process-steps.test.ts: 6 streaming thinking
 * fragments + 2 tool pairs + 1 sandwiched text + preface/final text.
 */

jest.mock("@expo/vector-icons", () => ({
  Ionicons: () => null,
}));

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (_key: string, fallback: string, opts?: Record<string, unknown>) =>
      typeof fallback === "string"
        ? fallback.replace(/\{\{(\w+)\}\}/g, (_m, k: string) => String(opts?.[k] ?? ""))
        : _key,
  }),
}));

jest.mock("@/components/ui/collapsible", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    Collapsible: ({ children }: { children: React.ReactNode }) =>
      React.createElement(View, null, children),
    CollapsibleContent: ({ children }: { children: React.ReactNode }) =>
      React.createElement(View, null, children),
    CollapsibleTrigger: ({ children }: { children: React.ReactNode }) =>
      React.createElement(View, null, children),
  };
});

import { ChatTimeline } from "@/components/chat/chat-timeline";

function msg(overrides: Partial<TaskMessagePayload> & { seq: number }): TaskMessagePayload {
  return {
    task_id: "t1",
    issue_id: "i1",
    type: "thinking",
    ...overrides,
  } as TaskMessagePayload;
}

function screenshotScenario(): TaskMessagePayload[] {
  return [
    msg({ seq: 1, type: "text", content: "preface" }),
    msg({ seq: 2, content: "The" }),
    msg({ seq: 3, content: " quick" }),
    msg({ seq: 4, content: " brown" }),
    msg({ seq: 5, content: " fox" }),
    msg({ seq: 6, content: " jumps" }),
    msg({ seq: 7, content: " higher." }),
    msg({ seq: 8, type: "tool_use", tool: "Read", input: { file_path: "a.ts" } }),
    msg({ seq: 9, type: "tool_result", tool: "Read", output: "ok" }),
    msg({ seq: 10, type: "text", content: "mid-progress note" }),
    msg({ seq: 11, type: "tool_use", tool: "Edit", input: { file_path: "a.ts" } }),
    msg({ seq: 12, type: "tool_result", tool: "Edit", output: "done" }),
    msg({ seq: 13, type: "text", content: "final answer" }),
  ];
}

describe("ChatTimeline step count (RUYI-444)", () => {
  it("badges the merged middle count, not the raw payload count", async () => {
    const utils = await render(<ChatTimeline items={screenshotScenario()} />);
    // 6 merged middle items, not 10 raw non-text payloads (2138 vs 416 class).
    expect(utils.getByText("6 steps")).toBeTruthy();
    expect(utils.queryByText("10 steps")).toBeNull();
  });

  it("renders one merged thinking row plus the sandwiched text row", async () => {
    const utils = await render(<ChatTimeline items={screenshotScenario()} />);
    // The six fragments exist only inside the single merged string (rendered
    // twice: collapsed preview + expanded body) — never as standalone rows.
    expect(utils.getAllByText("The quick brown fox jumps higher.")).toHaveLength(2);
    expect(utils.queryByText("The")).toBeNull();
    expect(utils.getAllByText("mid-progress note").length).toBeGreaterThan(0);
    // Preface/final stay outside the fold (the parent bubble owns the reply).
    expect(utils.queryByText("preface")).toBeNull();
    expect(utils.queryByText("final answer")).toBeNull();
  });

  it("renders nothing for an all-text stream", async () => {
    const utils = await render(
      <ChatTimeline
        items={[
          msg({ seq: 1, type: "text", content: "a" }),
          msg({ seq: 2, type: "text", content: "b" }),
        ]}
      />,
    );
    expect(utils.queryByText(/steps?/)).toBeNull();
  });
});
