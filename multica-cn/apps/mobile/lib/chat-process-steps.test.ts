// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { TaskMessagePayload } from "@multica/core/types";
import { chatProcessSteps } from "./chat-process-steps";

/**
 * RUYI-444 — mobile chat "N steps" fold must count the same thing web chat
 * counts: `buildTimeline` (shared packages/core/task-transcript/build-timeline.ts:
 * seq sort → merge adjacent streaming text/thinking fragments → redact) followed
 * by the splitTimeline middle carve (packages/views/chat/lib/copy-text.ts — items
 * from the first to the last non-text entry, sandwiched text included,
 * preface/final excluded).
 *
 * Regression shape (Owner 实机截图)：同一段流式思考被 daemon 逐 flush 落库，
 * 旧逻辑把每条原始 payload 各算一步——同一回复手机端 2138 步 vs PC 端 416 步。
 */

function msg(overrides: Partial<TaskMessagePayload> & { seq: number }): TaskMessagePayload {
  return {
    task_id: "t1",
    issue_id: "i1",
    type: "thinking",
    ...overrides,
  } as TaskMessagePayload;
}

/** Screenshot scenario: 6 thinking flush fragments → 1 step, tool pair rows
 *  stay separate (web chat renders use/result unpaired), one sandwiched text
 *  row inside the fold, preface/final text outside. */
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

describe("chatProcessSteps (RUYI-444)", () => {
  it("merges streaming thinking fragments and counts the middle like web", () => {
    const items = screenshotScenario();
    const { middle } = chatProcessSteps(items);

    // merged: 1 thinking + Read use/result + sandwich text + Edit use/result
    expect(middle).toHaveLength(6);
    // The old mobile semantics counted every raw non-text payload:
    expect(items.filter((i) => i.type !== "text")).toHaveLength(10);

    expect(middle[0]).toMatchObject({
      type: "thinking",
      seq: 2,
      content: "The quick brown fox jumps higher.",
    });
  });

  it("keeps tool_use/tool_result as separate rows, mirroring web chat", () => {
    const { middle } = chatProcessSteps(screenshotScenario());
    const types = middle.map((i) => i.type);
    expect(types).toEqual([
      "thinking",
      "tool_use",
      "tool_result",
      "text",
      "tool_use",
      "tool_result",
    ]);
    expect(middle.filter((i) => i.type === "tool_use").map((i) => i.tool)).toEqual([
      "Read",
      "Edit",
    ]);
  });

  it("includes sandwiched text in the fold and excludes preface/final", () => {
    const { middle } = chatProcessSteps(screenshotScenario());
    expect(middle.some((i) => i.type === "text" && i.content === "mid-progress note")).toBe(
      true,
    );
    expect(middle.some((i) => i.content === "preface")).toBe(false);
    expect(middle.some((i) => i.content === "final answer")).toBe(false);
  });

  it("returns an empty middle for empty or all-text streams", () => {
    expect(chatProcessSteps([]).middle).toEqual([]);
    const textOnly = [
      msg({ seq: 1, type: "text", content: "a" }),
      msg({ seq: 2, type: "text", content: "b" }),
    ];
    expect(chatProcessSteps(textOnly).middle).toEqual([]);
  });

  it("coalesces after sorting by seq, so WS arrivals out of order still merge", () => {
    const shuffled = [
      msg({ seq: 3, content: " b" }),
      msg({ seq: 1, type: "text", content: "preface" }),
      msg({ seq: 2, content: "a" }),
      msg({ seq: 4, type: "tool_use", tool: "Read", input: {} }),
    ];
    const { middle } = chatProcessSteps(shuffled);
    expect(middle).toHaveLength(2);
    expect(middle[0]).toMatchObject({ type: "thinking", content: "a b", seq: 2 });
    expect(middle[1]).toMatchObject({ type: "tool_use" });
  });

  it("redacts content and output through the shared buildTimeline pipeline", () => {
    const items = [
      msg({ seq: 1, content: "key is sk-abcdef1234567890abcdef ok" }),
      msg({ seq: 2, type: "tool_use", tool: "Bash", input: { command: "env" } }),
      msg({ seq: 3, type: "tool_result", tool: "Bash", output: "TOKEN=supersecret123" }),
    ];
    const { middle } = chatProcessSteps(items);
    expect(middle[0]!.content).not.toContain("sk-abcdef1234567890abcdef");
    expect(middle[0]!.content).toContain("[REDACTED API KEY]");
    expect(middle[2]!.output).not.toContain("supersecret123");
    expect(middle[2]!.output).toContain("[REDACTED CREDENTIAL]");
  });
});
