/**
 * @vitest-environment jsdom
 *
 * SuppressedBadge two-state rendering contract (RUYI-275 AC3): renders only
 * for `run_suppressed === true`, and stays out of the DOM for `false` and —
 * the old-backend compatibility negative — `undefined`. No placeholder, no
 * skeleton: an unsuppressed card must be pixel-identical to pre-RUYI-275.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Issue } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));

// No live/queued tasks anywhere: the badge's indicator-first mutual
// exclusion only yields when the activity indicator would render, and an
// empty snapshot means it never does.
vi.mock("@multica/core/agents", () => ({
  agentTaskSnapshotOptions: (wsId: string) => ({
    queryKey: ["agent-tasks", wsId],
    queryFn: async () => [],
  }),
}));

import { SuppressedBadge } from "./suppressed-badge";

afterEach(cleanup);

function renderBadge(issue: Partial<Issue>) {
  const full: Issue = {
    id: "i-1",
    workspace_id: "ws-1",
    number: 1,
    identifier: "WS-1",
    title: "t",
    description: null,
    status: "todo",
    status_category: "todo",
    priority: "none",
    assignee_type: "agent",
    assignee_id: "a-1",
    creator_type: "member",
    creator_id: "u-1",
    parent_issue_id: null,
    project_id: null,
    position: 0,
    stage: null,
    start_date: null,
    due_date: null,
    metadata: {},
    properties: {},
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...issue,
  } as Issue;
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  client.setQueryData(["agent-tasks", "ws-1"], []);
  return renderWithI18n(
    <QueryClientProvider client={client}>
      <SuppressedBadge issue={full} />
    </QueryClientProvider>,
  );
}

describe("SuppressedBadge", () => {
  it("renders the on-hold pill for a suppressed issue", () => {
    const { container } = renderBadge({ run_suppressed: true });
    expect(container.textContent).toContain("On hold");
    // Amber info tone, per the UX spec — deliberately not destructive red.
    expect(container.querySelector(".text-amber-600")).not.toBeNull();
  });

  it("renders nothing for run_suppressed false", () => {
    const { container } = renderBadge({ run_suppressed: false });
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when the field is absent (old backend)", () => {
    const { container } = renderBadge({ run_suppressed: undefined });
    expect(container).toBeEmptyDOMElement();
  });
});
