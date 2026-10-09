// RUYI-554 unified status language for the dense-row activity badge:
//   ≥1 running task      → stack flagged "running" (breathing avatars)
//   0 running, ≥1 queued → stack flagged "queued" (grayed avatars)
//   nothing              → renders null
// The PulseDot beside the stack is retired — the badge renders no dot; the
// stack avatars themselves carry the state.
import { render, screen } from "@testing-library/react-native";
import type { AgentTask } from "@multica/core/types";
import { IssueAgentActivityBadge } from "@/components/issue/issue-agent-activity-badge";

const mockStackProps: Array<Record<string, unknown>> = [];
jest.mock("@/components/ui/avatar-stack", () => ({
  AvatarStack: (props: Record<string, unknown>) => {
    mockStackProps.push(props);
    const { View } = require("react-native");
    return <View testID="avatar-stack" />;
  },
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (key: string, fallback?: string) =>
      typeof fallback === "string" ? fallback : key,
  }),
}));

// toJSON() hands back library JSON whose text leaves are plain strings
// (react-test-renderer ≥1.2 node types), so the walker accepts both shapes.
type JsonNode = string | { props?: Record<string, unknown>; children?: JsonNode[] } | null;

/** Walk the rendered JSON tree collecting every className — tree-wide
 *  negative assertions without renderer-specific instance APIs. */
function collectClassNames(node: JsonNode, out: string[] = []): string[] {
  if (!node || typeof node === "string") return out;
  const cls = node.props?.className;
  if (typeof cls === "string") out.push(cls);
  for (const child of node.children ?? []) collectClassNames(child, out);
  return out;
}

function task(overrides: Partial<AgentTask> = {}): AgentTask {
  return {
    id: "task-1",
    issue_id: "issue-1",
    agent_id: "agent-1",
    status: "running",
    kind: "comment",
    created_at: "2026-10-02T00:00:00Z",
    ...overrides,
  } as AgentTask;
}

describe("IssueAgentActivityBadge unified language (RUYI-554)", () => {
  it("flags the stack running — and renders no dot", async () => {
    await render(<IssueAgentActivityBadge running={[task()]} queued={[]} />);

    expect(screen.getByTestId("avatar-stack")).toBeTruthy();
    expect(mockStackProps.at(-1)?.activity).toBe("running");
    expect(collectClassNames(screen.toJSON()).some((c) => c.includes("bg-brand"))).toBe(
      false,
    );
  });

  it("flags the stack queued when only queued tasks exist", async () => {
    await render(
      <IssueAgentActivityBadge
        running={[]}
        queued={[task({ status: "queued" })]}
      />,
    );

    expect(screen.getByTestId("avatar-stack")).toBeTruthy();
    expect(mockStackProps.at(-1)?.activity).toBe("queued");
  });

  it("prefers running over queued when both exist", async () => {
    await render(
      <IssueAgentActivityBadge
        running={[task()]}
        queued={[task({ id: "task-2", status: "queued" })]}
      />,
    );

    expect(mockStackProps.at(-1)?.activity).toBe("running");
  });

  it("renders nothing without active tasks", async () => {
    await render(<IssueAgentActivityBadge running={[]} queued={[]} />);

    expect(screen.queryByTestId("avatar-stack")).toBeNull();
  });
});
