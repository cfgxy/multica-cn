import { describe, expect, it } from "vitest";
import type { InboxItem } from "@multica/core/types";
import {
  getQuickCreateEditSeed,
  isQuickCreateOutcome,
} from "./quick-create-edit";

/**
 * Render-condition + seed-shape tests for the inbox detail's
 * "edit in the full form" button (RUYI-605). The screen renders the button
 * iff `getQuickCreateEditSeed(item)` is non-null, so these cases ARE the
 * button's conditional-rendering contract — mirroring web's gate in
 * packages/views/inbox/components/inbox-page.tsx (`isQuickCreateOutcome`
 * only, no original_prompt requirement).
 */
function makeItem(overrides: Partial<InboxItem>): InboxItem {
  return {
    id: "inbox-1",
    workspace_id: "ws-1",
    recipient_type: "member",
    recipient_id: "user-1",
    actor_type: "system",
    actor_id: null,
    type: "quick_create_failed",
    title: "Created issue failed",
    body: null,
    read: false,
    archived: false,
    issue_id: null,
    created_at: "2026-10-09T00:00:00Z",
    details: {},
    ...overrides,
  } as InboxItem;
}

describe("isQuickCreateOutcome", () => {
  it("matches exactly the two non-success quick-create outcomes", () => {
    expect(isQuickCreateOutcome("quick_create_failed")).toBe(true);
    expect(isQuickCreateOutcome("quick_create_unconfirmed")).toBe(true);
  });

  it("rejects success and unrelated types", () => {
    expect(isQuickCreateOutcome("quick_create_done")).toBe(false);
    expect(isQuickCreateOutcome("issue_assigned")).toBe(false);
    expect(isQuickCreateOutcome("autopilot_quota_exceeded")).toBe(false);
  });
});

describe("getQuickCreateEditSeed", () => {
  it("seeds description and agent from a failed outcome's details", () => {
    const seed = getQuickCreateEditSeed(
      makeItem({
        details: {
          original_prompt: "Deploy the staging build",
          agent_id: "agent-1",
        },
      }),
    );
    expect(seed).toEqual({
      description: "Deploy the staging build",
      agentId: "agent-1",
    });
  });

  it("seeds an unconfirmed outcome too — recovery affordance without failure framing", () => {
    const seed = getQuickCreateEditSeed(
      makeItem({
        type: "quick_create_unconfirmed",
        details: { original_prompt: "Fix the flaky test" },
      }),
    );
    expect(seed).toEqual({ description: "Fix the flaky test", agentId: null });
  });

  it("renders with an empty description when original_prompt is missing — web parity", () => {
    const seed = getQuickCreateEditSeed(makeItem({ details: {} }));
    expect(seed).toEqual({ description: "", agentId: null });
  });

  it("treats missing details the same as empty details", () => {
    const seed = getQuickCreateEditSeed(makeItem({ details: undefined }));
    expect(seed).toEqual({ description: "", agentId: null });
  });

  it("keeps a multi-line prompt verbatim", () => {
    const prompt = "Line one\n\nLine two with 中文";
    const seed = getQuickCreateEditSeed(
      makeItem({ details: { original_prompt: prompt } }),
    );
    expect(seed?.description).toBe(prompt);
  });

  it("returns null for quick_create_done and non-quick-create types", () => {
    expect(getQuickCreateEditSeed(makeItem({ type: "quick_create_done" }))).toBe(
      null,
    );
    expect(getQuickCreateEditSeed(makeItem({ type: "issue_assigned" }))).toBe(
      null,
    );
  });
});
