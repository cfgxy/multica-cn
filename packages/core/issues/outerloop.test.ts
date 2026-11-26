// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseWithFallback } from "../api/schema";
import { OuterLoopFireResponseSchema, OuterLoopPlanResponseSchema } from "./outerloop";

const fallback = {
  enabled: false,
  has_plan: false,
  plan: { state: "stopped" as const, goal: "", max_rounds: 20, round: 0, wait_reason: "" },
  issue_revision: 0,
};

describe("outer loop API contracts", () => {
  it("keeps the snake-case plan fields used by the panel", () => {
    const response = OuterLoopPlanResponseSchema.parse({
      enabled: true,
      has_plan: true,
      plan: { state: "waiting_human", goal: "完成验收", max_rounds: 8, round: 3, wait_reason: "等待批准" },
      issue_revision: 12,
    });
    expect(response.plan).toMatchObject({ state: "waiting_human", max_rounds: 8, round: 3, wait_reason: "等待批准" });
  });

  it("degrades a malformed GET response without exposing undefined plan fields", () => {
    expect(parseWithFallback({ has_plan: true, plan: { State: "running" } }, OuterLoopPlanResponseSchema, fallback, { endpoint: "test" })).toEqual(fallback);
  });

  it("retains a pending-task fire outcome for the panel", () => {
    expect(OuterLoopFireResponseSchema.parse({ dispatched: false, reason: "pending_task" })).toMatchObject({ dispatched: false, reason: "pending_task" });
  });
});
