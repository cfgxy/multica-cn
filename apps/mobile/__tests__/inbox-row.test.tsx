// Render tests for InboxRow (RUYI-314 identifier, RUYI-554 status language):
//   - the row shows the server-assembled identifier (e.g. `RUYI-314`) next
//     to the title, and renders nothing for issue-less notifications;
//   - unread vs read is typographic only (semibold foreground vs muted) and
//     mutually exclusive — the old leading blue dot is gone;
//   - the actor avatar receives the unified activity state derived from the
//     row's issue activity slice, and no presence corner dot.
// The visual half of inbox-row is deliberately dependency-light, so the mocks
// below only stand in for lookups (status catalog, display title, time).
import { render, screen } from "@testing-library/react-native";
import { View } from "react-native";
import type { InboxItem } from "@multica/core/types";
import type { IssueActivity } from "@/lib/issue-agent-activity";
import { InboxRow } from "@/components/inbox/inbox-row";

const mockAvatarProps: Array<Record<string, unknown>> = [];
jest.mock("@/components/ui/actor-avatar", () => ({
  ActorAvatar: (props: Record<string, unknown>) => {
    mockAvatarProps.push(props);
    return null;
  },
}));
jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});
jest.mock("@/components/ui/status-icon", () => ({
  StatusIcon: () => null,
}));
jest.mock("@/components/issue/issue-agent-activity-badge", () => ({
  IssueAgentActivityBadge: () => null,
}));
jest.mock("@/components/inbox/detail-label", () => ({
  InboxDetailLabel: () => null,
}));
jest.mock("@/lib/inbox-display", () => ({
  getInboxDisplayTitle: (item: { title: string }) => item.title,
}));
jest.mock("@/lib/use-issue-statuses", () => ({
  useIssueStatuses: () => ({
    categoryOf: () => "todo",
    colorOf: () => null,
  }),
}));
jest.mock("@/lib/time-ago", () => ({
  timeAgo: () => "2h",
}));
jest.mock("@/lib/utils", () => ({
  cn: (...classes: unknown[]) => classes.filter(Boolean).join(" "),
}));

type JsonNode = { props?: Record<string, unknown>; children?: JsonNode[] } | null;

/** Walk the rendered JSON tree collecting every className — used for
 *  tree-wide negative assertions (no dot anywhere) without touching
 *  renderer-specific instance APIs. */
function collectClassNames(node: JsonNode, out: string[] = []): string[] {
  if (!node) return out;
  const cls = node.props?.className;
  if (typeof cls === "string") out.push(cls);
  for (const child of node.children ?? []) collectClassNames(child, out);
  return out;
}

function inboxItem(overrides: Partial<InboxItem> = {}): InboxItem {
  return {
    id: "item-1",
    workspace_id: "ws-1",
    recipient_type: "member",
    recipient_id: "user-1",
    actor_type: "agent",
    actor_id: "agent-1",
    type: "status_changed",
    severity: "info",
    issue_id: "issue-1",
    title: "Fix login loop",
    body: null,
    issue_status: "in_progress",
    issue_identifier: "RUYI-314",
    read: false,
    archived: false,
    created_at: "2026-09-30T10:00:00Z",
    details: null,
    ...overrides,
  };
}

describe("InboxRow issue identifier", () => {
  it("renders the identifier next to the title", async () => {
    await render(<InboxRow item={inboxItem()} onPress={jest.fn()} />);

    expect(screen.getByText("RUYI-314")).toBeTruthy();
    expect(screen.getByText("Fix login loop")).toBeTruthy();
  });

  it("renders no identifier for issue-less notifications", async () => {
    await render(
      <InboxRow
        item={inboxItem({ issue_id: null, issue_status: null, issue_identifier: null })}
        onPress={jest.fn()}
      />,
    );

    expect(screen.getByText("Fix login loop")).toBeTruthy();
    expect(screen.queryByText("RUYI-314")).toBeNull();
  });
});

// RUYI-554: unread vs read is typographic and mutually exclusive; the blue
// unread dot is gone; the avatar carries the derived activity state.
describe("InboxRow unified status language (RUYI-554)", () => {
  it("styles unread titles semibold foreground and read titles muted — mutually exclusive", async () => {
    // Both states in ONE tree: mid-test unmount + re-render poisons later
    // renders on RNTL v14 (jest-expo), so multi-state tests render rows
    // side by side instead of cycling renders.
    await render(
      <View>
        <InboxRow item={inboxItem({ id: "item-unread", read: false })} onPress={jest.fn()} />
        <InboxRow item={inboxItem({ id: "item-read", read: true })} onPress={jest.fn()} />
      </View>,
    );

    const titles = screen.getAllByText("Fix login loop");
    expect(titles).toHaveLength(2);
    const unreadTitle = titles[0];
    const readTitle = titles[1];
    expect(unreadTitle.props.className).toContain("font-semibold");
    expect(unreadTitle.props.className).toContain("text-foreground");
    expect(unreadTitle.props.className).not.toContain("text-muted-foreground");
    expect(readTitle.props.className).toContain("text-muted-foreground");
    expect(readTitle.props.className).not.toContain("font-semibold");
    expect(readTitle.props.className).not.toContain("text-foreground");
  });

  it("renders no brand-dot element anywhere in the row", async () => {
    const rowScreen = await render(
      <InboxRow item={inboxItem({ read: false })} onPress={jest.fn()} />,
    );

    expect(collectClassNames(rowScreen.toJSON()).some((c) => c.includes("bg-brand"))).toBe(
      false,
    );
  });

  it("derives the avatar activity from the row's issue slice", async () => {
    const running: IssueActivity = {
      running: [
        {
          id: "task-1",
          agent_id: "agent-1",
          status: "running",
        } as IssueActivity["running"][number],
      ],
      queued: [],
    };
    const queued: IssueActivity = {
      running: [],
      queued: [
        {
          id: "task-2",
          agent_id: "agent-1",
          status: "queued",
        } as IssueActivity["queued"][number],
      ],
    };
    mockAvatarProps.length = 0;
    await render(
      <View>
        <InboxRow item={inboxItem({ id: "item-r", read: true })} activity={running} onPress={jest.fn()} />
        <InboxRow item={inboxItem({ id: "item-q", read: true })} activity={queued} onPress={jest.fn()} />
        <InboxRow item={inboxItem({ id: "item-n", read: true })} onPress={jest.fn()} />
      </View>,
    );

    expect(mockAvatarProps).toHaveLength(3);
    expect(mockAvatarProps[0]?.activity).toBe("running");
    expect(mockAvatarProps[1]?.activity).toBe("queued");
    expect(mockAvatarProps[2]?.activity).toBeUndefined();
  });

  it("no longer passes showPresence — the corner dot is retired from rows", async () => {
    mockAvatarProps.length = 0;
    await render(<InboxRow item={inboxItem()} onPress={jest.fn()} />);

    expect(mockAvatarProps.at(-1)?.showPresence).toBeUndefined();
  });
});
