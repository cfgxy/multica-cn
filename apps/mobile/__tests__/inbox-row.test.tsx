// Render tests for InboxRow's issue identifier (RUYI-314): the row must show
// the server-assembled identifier (e.g. `RUYI-314`) next to the title, and
// render nothing for issue-less notifications whose identifier is null.
// The visual half of inbox-row is deliberately dependency-light, so the mocks
// below only stand in for lookups (status catalog, display title, time) —
// presence/absence of the identifier text is what these pin.
import { render, screen } from "@testing-library/react-native";
import type { InboxItem } from "@multica/core/types";
import { InboxRow } from "@/components/inbox/inbox-row";

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});
jest.mock("@/components/ui/actor-avatar", () => ({
  ActorAvatar: () => null,
}));
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

describe("InboxRow unread affordance", () => {
  function unreadItem(): InboxItem {
    return inboxItem({ read: false });
  }

  it("shows the unread dot for an unread row by default", async () => {
    await render(<InboxRow item={unreadItem()} onPress={jest.fn()} />);

    expect(screen.queryByTestId("inbox-row-unread-dot")).toBeTruthy();
  });

  // Archived view parity with web (packages/views/inbox/components/
  // inbox-list-item.tsx): archiving leaves `read` untouched, so the archived
  // list would otherwise pin an unread marker the user cannot clear from
  // there — the affordance is suppressed in that view only.
  it("suppresses the unread dot when the view opts out", async () => {
    await render(
      <InboxRow item={unreadItem()} onPress={jest.fn()} showUnread={false} />,
    );

    expect(screen.queryByTestId("inbox-row-unread-dot")).toBeNull();
  });
});
