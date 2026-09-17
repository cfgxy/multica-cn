// @vitest-environment jsdom

import { cleanup, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentTask, TaskUsage } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <span data-testid="actor-avatar" />,
}));

import { IssueUsageDialog } from "./issue-usage-dialog";

function makeTask(overrides: Partial<AgentTask> = {}): AgentTask {
  return {
    id: "task-1",
    agent_id: "agent-1",
    runtime_id: "runtime-1",
    issue_id: "issue-1",
    status: "completed",
    priority: 0,
    dispatched_at: null,
    started_at: "2026-08-05T08:00:00Z",
    completed_at: "2026-08-05T08:11:00Z",
    result: null,
    error: null,
    created_at: "2026-08-05T08:00:00Z",
    trigger_summary: "Initial run",
    ...overrides,
  };
}

function usage(overrides: Partial<TaskUsage> = {}): TaskUsage {
  return {
    provider: "anthropic",
    model: "claude-opus-5",
    input_tokens: 1_000,
    output_tokens: 1_000,
    cache_read_tokens: 1_000,
    cache_write_tokens: 0,
    ...overrides,
  };
}

function open(tasks: AgentTask[]) {
  renderWithI18n(
    <IssueUsageDialog open onOpenChange={() => {}} identifier="ACM-1" tasks={tasks} />,
  );
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
});

afterEach(cleanup);

describe("IssueUsageDialog", () => {
  it("floors the cache hit rate instead of rounding it up to 100%", () => {
    // 99.55% — rounding would print "100% hit rate" and claim every token came
    // from cache on an issue that plainly read some fresh input.
    open([
      makeTask({
        usage: [usage({ input_tokens: 573_500, cache_read_tokens: 126_000_000 })],
      }),
    ]);

    expect(screen.getByText(/99% hit rate/)).toBeInTheDocument();
    expect(screen.queryByText(/100% hit rate/)).not.toBeInTheDocument();
  });

  it("still says 100% when the hit rate really is 100%", () => {
    open([
      makeTask({ usage: [usage({ input_tokens: 0, cache_read_tokens: 1_000 })] }),
    ]);

    expect(screen.getByText(/100% hit rate/)).toBeInTheDocument();
  });

  it("keeps the full model list reachable when the cell truncates", () => {
    // A run that spilled across models carries ids long enough to set the
    // table's width on their own; the cell is capped, so the untruncated list
    // has to survive somewhere.
    const models = "claude-haiku-4-5-20251001, claude-opus-5[1m]";
    open([
      makeTask({
        usage: [
          usage({ model: "claude-haiku-4-5-20251001" }),
          usage({ model: "claude-opus-5[1m]" }),
        ],
      }),
    ]);

    expect(screen.getByTitle(models)).toBeInTheDocument();
  });

  it("gives every terminal run a status a screen reader can read", () => {
    // The status glyph is aria-hidden, so without the paired label a failed
    // run and a completed one are indistinguishable to assistive tech.
    open([
      makeTask({ id: "t-ok", usage: [usage()] }),
      makeTask({ id: "t-bad", status: "failed", usage: [usage()] }),
    ]);

    expect(screen.getByText("Completed")).toBeInTheDocument();
    expect(screen.getByText("Failed")).toBeInTheDocument();
  });

  it("renders the RUYI-154 run-stat columns when the run reported them", () => {
    open([
      makeTask({
        usage: [usage()],
        turns: 12,
        compactions: 2,
        max_context_tokens: 150_000,
        context_tokens: 80_000,
      }),
    ]);

    const row = screen.getByRole("table").querySelector("tbody tr");
    expect(row?.textContent).toContain("12");
    expect(row?.textContent).toContain("2");
    // formatTokens renders large counts abbreviated (e.g. "150.0K"), so assert
    // the raw values are not what's shown as an em dash rather than pinning
    // formatTokens' own formatting, which has its own test coverage.
    expect(screen.queryAllByText("—")).toHaveLength(0);
  });

  it("renders an em dash, not 0, for a run with no recorded run stats", () => {
    // Same "absent must not read as zero" contract TestListTasksByIssueHydratesUsage
    // pins server-side: a run predating RUYI-154 has undefined turns/compactions/
    // max_context_tokens/context_tokens, which must not collapse to "0 turns".
    open([makeTask({ usage: [usage()] })]);

    const row = screen.getByRole("table").querySelector("tbody tr");
    const dashCount = (row?.textContent?.match(/—/g) ?? []).length;
    expect(dashCount).toBe(4);
    expect(row?.textContent).not.toContain("0 turns");
  });

  it("lets the run table scroll rather than widening the dialog", () => {
    // jsdom has no layout engine, so the overflow itself is verified in a
    // browser. What is pinned here is the contract that produced the bug: the
    // scroll container must be able to shrink below its content. Drop
    // `min-w-0` and the box grows to the table's min-content width instead of
    // clipping, which is what pushed the table outside the dialog.
    open([makeTask({ usage: [usage()] })]);

    const table = screen.getByRole("table");
    const scroller = table.parentElement;
    expect(scroller?.className).toContain("overflow-auto");
    expect(scroller?.className).toContain("min-w-0");
  });
});
