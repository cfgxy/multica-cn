// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import type { TimelineItem } from "@multica/core/task-transcript";
import type { AgentTask } from "@multica/core/types";
import {
  buildRunStepViews,
  cancelReasonLabel,
  formatRunDuration,
  formatStepDuration,
  runCopyAllText,
  runDurationMs,
  runErrorText,
  runOutcomeSummary,
  runStepCopyText,
  redactedDetailBody,
} from "./run-detail";

// i18next 未 init 时 t() 返回 undefined（failure-reason-label.test.ts 同款
// 处理）：mock 成「缺失 → 英文兜底」的 i18next 语义，只关心兜底串本身。
vi.mock("i18next", () => ({
  default: {
    t: (_key: string, fallback?: string) =>
      typeof fallback === "string" ? fallback : _key,
  },
}));

/**
 * RUYI-33 — pure model for the mobile run-detail screen. Everything the
 * screen renders or copies flows through here, so the redaction policy
 * (PM 加严版：所有展示与复制出口必须经 redactSecrets 等价脱敏，含 input
 * 派生文本) is asserted at the model level, not left to each JSX exit.
 */

function task(overrides: Partial<AgentTask>): AgentTask {
  return {
    id: "0b9e6c1a-1111-4222-8333-444455556666",
    agent_id: "a1",
    runtime_id: "r1",
    issue_id: "i1",
    status: "completed",
    priority: 0,
    dispatched_at: null,
    started_at: "2026-08-30T10:00:00Z",
    completed_at: "2026-08-30T10:03:30Z",
    result: null,
    error: null,
    created_at: "2026-08-30T09:59:00Z",
    ...overrides,
  } as AgentTask;
}

function item(overrides: Partial<TimelineItem>): TimelineItem {
  return { seq: 1, type: "text", content: "hi", ...overrides } as TimelineItem;
}

const SECRET = "sk-VERYSECRETKEY123456789012";

describe("runDurationMs / formatRunDuration", () => {
  it("measures started → completed", () => {
    expect(runDurationMs(task({}))).toBe(210_000);
    expect(formatRunDuration(210_000)).toBe("3m 30s");
  });

  it("measures a running run against now", () => {
    const t = task({ status: "running", completed_at: null });
    const now = Date.parse("2026-08-30T10:01:05Z");
    expect(runDurationMs(t, now)).toBe(65_000);
    expect(formatRunDuration(65_000)).toBe("1m 05s");
  });

  it("returns null without a usable start", () => {
    expect(runDurationMs(task({ started_at: null, status: "queued" }))).toBeNull();
  });

  it("formats sub-minute durations like the web header", () => {
    expect(formatRunDuration(42_000)).toBe("42s");
  });
});

describe("formatStepDuration / step clock", () => {
  it("mirrors the web step-duration column", () => {
    expect(formatStepDuration(400)).toBe("0.4s");
    expect(formatStepDuration(4_200)).toBe("4.2s");
    expect(formatStepDuration(65_000)).toBe("1m 05s");
  });
});

describe("redactedDetailBody", () => {
  it("passes short bodies through redaction", () => {
    const r = redactedDetailBody(`token ${SECRET} in log`);
    expect(r.truncated).toBe(false);
    expect(r.body).not.toContain(SECRET);
    expect(r.body).toContain("[REDACTED API KEY]");
  });

  it("clamps over-long bodies and still redacts the visible slice", () => {
    const long = `${"x".repeat(8000)} ${SECRET}`;
    const r = redactedDetailBody(long);
    expect(r.truncated).toBe(true);
    expect(r.body).not.toContain(SECRET);
    expect(r.body.length).toBeLessThanOrEqual(8000 + "[REDACTED API KEY]".length + 1);
  });
});

describe("runErrorText — failure/cancel panel exit (RUYI-33 review fix)", () => {
  it("redacts credentials embedded in persisted error text", () => {
    const r = runErrorText(
      'FATAL: password authentication failed for user "app"\n' +
        "connection string: postgres://app:Sup3rSecret@db.internal:5432/prod",
    );
    expect(r.truncated).toBe(false);
    expect(r.body).not.toContain("Sup3rSecret");
    expect(r.body).toContain("[REDACTED CONNECTION STRING]");
  });

  it("clamps an over-long error and still redacts the visible slice", () => {
    const secret = "xoxb-123456789-abcdefghijklmnop";
    const err = `slack call failed with token ${secret}\n${"z".repeat(9000)}`;
    const r = runErrorText(err);
    expect(r.truncated).toBe(true);
    expect(r.body).not.toContain(secret);
  });
});

describe("buildRunStepViews — redaction on every view field", () => {
  it("redacts a secret that arrives inside tool input (input-derived text)", () => {
    const views = buildRunStepViews([
      item({
        seq: 1,
        type: "tool_use",
        tool: "Bash",
        input: { command: `export API_KEY=${SECRET} && ./deploy.sh` },
      }),
      item({ seq: 2, type: "tool_result", tool: "Bash", output: `"done\\n"` }),
    ]);
    const call = views.find((v) => v.kind === "call");
    expect(call).toBeDefined();
    const rendered = JSON.stringify(call);
    expect(rendered).not.toContain(SECRET);
  });

  it("redacts secrets inside tool output", () => {
    const views = buildRunStepViews([
      item({
        seq: 1,
        type: "tool_use",
        tool: "Read",
        input: { path: "/tmp/a.txt" },
      }),
      item({
        seq: 2,
        type: "tool_result",
        tool: "Read",
        output: JSON.stringify(`ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmn`),
      }),
    ]);
    expect(JSON.stringify(views)).not.toContain("ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ");
  });

  it("redacts message content and thinking bodies", () => {
    const views = buildRunStepViews([
      item({ seq: 1, type: "thinking", content: `thinking about ${SECRET}` }),
      item({ seq: 2, type: "text", content: `answer ${SECRET}` }),
    ]);
    expect(JSON.stringify(views)).not.toContain(SECRET);
  });

  it("pairs tool_use with its result and carries the call duration", () => {
    const views = buildRunStepViews([
      item({
        seq: 1,
        type: "tool_use",
        tool: "Bash",
        created_at: "2026-08-30T10:00:00Z",
        input: { command: "pnpm test" },
      }),
      item({
        seq: 2,
        type: "tool_result",
        tool: "Bash",
        created_at: "2026-08-30T10:00:12Z",
        output: `"ok"`,
      }),
    ]);
    const call = views.find((v) => v.kind === "call");
    expect(call?.durationLabel).toBe("12s");
  });

  it("renders diff detail with add/remove rows and redacted line text", () => {
    const views = buildRunStepViews([
      item({
        seq: 1,
        type: "tool_use",
        tool: "Edit",
        input: {
          file_path: "/repo/src/a.ts",
          old_string: "const token = '" + SECRET + "';",
          new_string: "const token = process.env.TOKEN;",
        },
      }),
    ]);
    const call = views.find((v) => v.kind === "call");
    expect(call?.detail?.variant).toBe("diff");
    if (call?.detail?.variant === "diff") {
      expect(JSON.stringify(call.detail.lines)).not.toContain(SECRET);
    }
  });

  it("shows step clock offsets for message rows", () => {
    const views = buildRunStepViews([
      item({ seq: 1, type: "text", content: "start", created_at: "2026-08-30T10:00:00Z" }),
      item({ seq: 2, type: "text", content: "later", created_at: "2026-08-30T10:01:05Z" }),
    ], Date.parse("2026-08-30T10:00:00Z"));
    expect(views[0]?.clockLabel).toBe("+00:00");
    expect(views[1]?.clockLabel).toBe("+01:05");
  });
});

describe("copy exits", () => {
  it("per-step copy embeds full input JSON but redacts secrets", () => {
    const text = runStepCopyText(
      item({
        seq: 1,
        type: "tool_use",
        tool: "Bash",
        created_at: "2026-08-30T10:00:00Z",
        input: { command: `curl -H "Authorization: Bearer ${SECRET}"` },
      }),
    );
    expect(text).toContain("[Bash]");
    expect(text).toContain("[REDACTED");
    expect(text).not.toContain(SECRET);
  });

  it("copy-all joins every event and applies redaction to the whole", () => {
    const text = runCopyAllText([
      item({ seq: 1, type: "text", content: `step one`, created_at: "2026-08-30T10:00:00Z" }),
      item({ seq: 2, type: "tool_use", tool: "Bash", input: { command: `X=${SECRET}` } }),
    ]);
    expect(text.split("\n\n").length).toBe(2);
    expect(text).not.toContain(SECRET);
  });
});

describe("runOutcomeSummary", () => {
  it("derives paths, line counts and command count; redacts paths", () => {
    const summary = runOutcomeSummary([
      item({
        seq: 1,
        type: "tool_use",
        tool: "Edit",
        input: {
          file_path: "/repo/secret-" + SECRET + ".ts",
          old_string: "a\nb",
          new_string: "a\nc\nd",
        },
      }),
      item({ seq: 2, type: "tool_use", tool: "Bash", input: { command: "pnpm test" } }),
    ]);
    expect(summary).not.toBeNull();
    expect(summary?.commandCount).toBe(1);
    expect(JSON.stringify(summary)).not.toContain(SECRET);
  });

  it("returns null for a run that touched nothing", () => {
    expect(runOutcomeSummary([item({ seq: 1, type: "text", content: "hi" })])).toBeNull();
  });
});

describe("cancelReasonLabel", () => {
  it("surfaces a persisted server-cancel reason", () => {
    const label = cancelReasonLabel(
      task({ status: "cancelled", failure_reason: "runtime_offline" }),
    );
    expect(label).toBeTruthy();
  });

  it("labels a user-initiated manual cancel", () => {
    const label = cancelReasonLabel(
      task({ status: "cancelled", failure_reason: "manual" }),
    );
    expect(label).toBeTruthy();
  });

  it("falls back to the system-cancel wording on an unknown reason with error text", () => {
    const label = cancelReasonLabel(
      task({
        status: "cancelled",
        failure_reason: "agent_error.something_new",
        error: "worktree finalize aborted",
      }),
    );
    expect(label).toBeTruthy();
  });

  it("returns null for a plain user cancel without reason", () => {
    expect(cancelReasonLabel(task({ status: "cancelled" }))).toBeNull();
  });

  it("returns null for non-cancelled statuses", () => {
    expect(cancelReasonLabel(task({ status: "failed", failure_reason: "timeout" }))).toBeNull();
  });
});
