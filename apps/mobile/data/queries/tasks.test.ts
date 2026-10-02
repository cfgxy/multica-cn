/**
 * Query assembly behind the full-space Tasks tab (RUYI-344).
 *
 * Guards the wire contract the five quadrant TABs rely on:
 *   - each TAB narrows server-side via `status_categories` ("全部" sends none),
 *   - sort travels as `sort_by` + `sort_direction` (default updated_at desc),
 *   - status/priority stay OFF the wire (client-side, same-N parity with the
 *     pre-refactor 全部 list),
 *   - mine relations map to the three server predicates,
 *   - actor picker selections map to `assignee_filters`/`creator_filters`
 *     (+ `include_no_assignee`),
 *   - the agent-running toggle maps to the live running-issue id set,
 *   - merged (≥2 relations) results dedupe by id and re-sort by the active
 *     key client-side.
 */
import { describe, expect, it, vi } from "vitest";
import type { AgentTask, Issue } from "@multica/core/types";
import type { MineRelation, TaskTab } from "@/data/stores/tasks-view-store";
import { issueKeys } from "./issue-keys";
import {
  RUNNING_TASK_STATUSES,
  TASK_TAB_CATEGORIES,
  buildTaskListFilter,
  mergeTaskIssues,
  runningIssueIdsFromSnapshot,
  taskListOptions,
} from "./tasks";

// data-layer tests must never load the native fetch chain (vitest.config.ts).
vi.mock("@/data/api", () => ({ api: {} }));

const ME = "user-me";

describe("TASK_TAB_CATEGORIES", () => {
  it("maps each quadrant to its server status_categories", () => {
    expect(TASK_TAB_CATEGORIES.all).toBeUndefined();
    expect(TASK_TAB_CATEGORIES.open).toEqual(["backlog", "todo"]);
    expect(TASK_TAB_CATEGORIES.active).toEqual(["in_progress", "in_review"]);
    expect(TASK_TAB_CATEGORIES.blocked).toEqual(["blocked"]);
    expect(TASK_TAB_CATEGORIES.completed).toEqual(["done"]);
  });
});

describe("buildTaskListFilter", () => {
  const base = (tab: TaskTab, extra: Partial<Parameters<typeof buildTaskListFilter>[0]> = {}) =>
    buildTaskListFilter({ tab, sortBy: "updated_at", userId: ME, ...extra });

  it("sends only the default sort for the 全部 tab", () => {
    const filter = base("all");
    expect(filter).toEqual({
      sort_by: "updated_at",
      sort_direction: "desc",
    });
  });

  it("never puts status or priority filters on the wire", () => {
    // Status multi-select and priority stay client-side on every TAB (the
    // chips row filters the loaded window); the wire must not carry them.
    const filter = base("open") as Record<string, unknown>;
    expect(filter).not.toHaveProperty("statuses");
    expect(filter).not.toHaveProperty("priorities");
    expect(filter).not.toHaveProperty("status");
    expect(filter).not.toHaveProperty("priority");
  });

  it("narrow each quadrant TAB server-side", () => {
    expect(base("open").status_categories).toEqual(["backlog", "todo"]);
    expect(base("blocked").status_categories).toEqual(["blocked"]);
  });

  it("carries the chosen sort key, always descending", () => {
    expect(base("all", { sortBy: "created_at" })).toEqual({
      sort_by: "created_at",
      sort_direction: "desc",
    });
  });

  it("maps each single mine relation to its server predicate", () => {
    expect(base("all", { mine: "assigned" }).assignee_id).toBe(ME);
    expect(base("all", { mine: "created" }).creator_id).toBe(ME);
    expect(base("all", { mine: "involved" }).involves_user_id).toBe(ME);
  });

  it("omits relation params without a userId", () => {
    const filter = buildTaskListFilter({
      tab: "all",
      sortBy: "updated_at",
      userId: null,
      mine: "assigned",
    });
    expect(filter.assignee_id).toBeUndefined();
  });

  it("passes actor picker selections through", () => {
    const assigneeRefs = [
      { type: "member" as const, id: "u-1" },
      { type: "agent" as const, id: "a-1" },
    ];
    const filter = base("all", {
      assigneeRefs,
      includeNoAssignee: true,
      creatorRefs: [{ type: "squad" as const, id: "s-1" }],
    });
    expect(filter.assignee_filters).toEqual(assigneeRefs);
    expect(filter.include_no_assignee).toBe(true);
    expect(filter.creator_filters).toEqual([{ type: "squad", id: "s-1" }]);
  });

  it("restricts the window to the running-issue set when the toggle is on", () => {
    const running = ["i-1", "i-2"];
    expect(base("active", { runningIssueIds: running }).ids).toEqual(running);
    expect(base("active", { runningIssueIds: [] }).ids).toBeUndefined();
  });

  it("combines TAB, relation and actor filters in one query", () => {
    const filter = base("open", {
      mine: "created",
      assigneeRefs: [{ type: "member" as const, id: "u-2" }],
      sortBy: "created_at",
    });
    expect(filter).toEqual({
      status_categories: ["backlog", "todo"],
      sort_by: "created_at",
      sort_direction: "desc",
      creator_id: ME,
      assignee_filters: [{ type: "member", id: "u-2" }],
    });
  });
});

describe("taskListOptions", () => {
  it("keys on the workspace-scoped task list family", () => {
    const filter = buildTaskListFilter({
      tab: "open",
      sortBy: "updated_at",
      userId: ME,
    });
    const options = taskListOptions("ws-1", filter);
    expect(options.queryKey).toEqual(
      issueKeys.taskList("ws-1", filter),
    );
    // Must sit under the list(wsId) prefix so the existing WS patchers and
    // reconnect invalidation reach it without knowing the filter shape.
    expect(options.queryKey.slice(0, 3)).toEqual(
      issueKeys.list("ws-1").slice(0, 3),
    );
  });

  it("produces distinct keys for distinct filters", () => {
    const a = buildTaskListFilter({ tab: "open", sortBy: "updated_at", userId: ME });
    const b = buildTaskListFilter({ tab: "open", sortBy: "created_at", userId: ME });
    expect(issueKeys.taskList("ws-1", a)).not.toEqual(
      issueKeys.taskList("ws-1", b),
    );
  });

  it("returns the bare issue array from the response", async () => {
    const { api } = await import("@/data/api");
    const issue = { id: "i-1" } as Issue;
    (api as unknown as { listIssues: ReturnType<typeof vi.fn> }).listIssues = vi
      .fn()
      .mockResolvedValue({ issues: [issue], total: 1 });

    const options = taskListOptions("ws-1", {
      sort_by: "updated_at",
      sort_direction: "desc",
    });
    const data = await (options.queryFn as (ctx: unknown) => Promise<Issue[]>)(
      {},
    );
    expect(data).toEqual([issue]);
  });
});

describe("mergeTaskIssues", () => {
  const row = (id: string, updated: string, created = "2026-01-01T00:00:00Z"): Issue =>
    ({
      id,
      updated_at: updated,
      created_at: created,
    }) as Issue;

  it("unions the relation lists, keeping one row per issue", () => {
    const merged = mergeTaskIssues(
      {
        assigned: [row("a", "2026-01-03T00:00:00Z")],
        created: [row("a", "2026-01-03T00:00:00Z"), row("b", "2026-01-02T00:00:00Z")],
        involved: [row("c", "2026-01-01T00:00:00Z")],
      },
      "updated_at",
    );
    expect(merged.map((i) => i.id)).toEqual(["a", "b", "c"]);
  });

  it("re-sorts the union by the active sort key, descending", () => {
    const merged = mergeTaskIssues(
      {
        assigned: [row("old", "2026-01-01T00:00:00Z")],
        created: [row("new", "2026-01-05T00:00:00Z")],
        involved: [row("mid", "2026-01-03T00:00:00Z", "2026-02-01T00:00:00Z")],
      },
      "updated_at",
    );
    expect(merged.map((i) => i.id)).toEqual(["new", "mid", "old"]);

    const byCreated = mergeTaskIssues(
      {
        assigned: [row("old", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")],
        created: [row("new", "2026-01-05T00:00:00Z", "2026-03-01T00:00:00Z")],
        involved: [row("mid", "2026-01-03T00:00:00Z", "2026-02-01T00:00:00Z")],
      },
      "created_at",
    );
    expect(byCreated.map((i) => i.id)).toEqual(["new", "mid", "old"]);
  });
});

describe("runningIssueIdsFromSnapshot", () => {
  const task = (
    id: string,
    status: AgentTask["status"],
    issueId: string,
  ): Pick<AgentTask, "id" | "status" | "issue_id"> => ({
    id,
    status,
    issue_id: issueId,
  });

  it("collects issue ids of active tasks only, deduped", () => {
    expect(RUNNING_TASK_STATUSES).toEqual([
      "queued",
      "dispatched",
      "waiting_local_directory",
      "running",
    ]);
    const ids = runningIssueIdsFromSnapshot([
      task("t1", "running", "i-1"),
      task("t2", "queued", "i-1"),
      task("t3", "waiting_local_directory", "i-2"),
      task("t4", "dispatched", "i-3"),
      task("t5", "completed", "i-4"),
      task("t6", "failed", "i-5"),
      task("t7", "cancelled", "i-6"),
    ]);
    expect(ids).toEqual(["i-1", "i-2", "i-3"]);
  });

  it("skips tasks without an issue", () => {
    expect(
      runningIssueIdsFromSnapshot([task("t1", "running", "")]),
    ).toEqual([]);
  });
});

describe("MineRelation", () => {
  it("exposes exactly the three server relations", () => {
    const all: MineRelation[] = ["assigned", "created", "involved"];
    expect(all).toHaveLength(3);
  });
});
