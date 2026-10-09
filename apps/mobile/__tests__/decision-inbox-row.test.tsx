// Render tests for the Decision Center row (RUYI-530): decided rows
// (answered/cancelled) render the inbox's read style — muted title, faded
// secondary line — while open rows keep the full-contrast look, and every
// row carries the card creator's avatar with a non-blank fallback.
// The row is deliberately presentational, so the mocks only stand in for
// lookups (i18n, time) — graying and avatar identity are what these pin.
import { render, screen } from "@testing-library/react-native";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import type { DecisionInboxRow as DecisionRowData } from "@/lib/decision-inbox-display";
import { DecisionInboxRow } from "@/components/decision/decision-inbox-row";

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});
jest.mock("@/components/ui/actor-avatar", () => ({
  ActorAvatar: (props: { type: string; id: string | null }) => {
    mockAvatarProps.push(props);
    return null;
  },
}));
jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (_key: string, fallback?: string) => fallback ?? _key }),
}));
jest.mock("@/lib/time-ago", () => ({
  timeAgo: () => "2h",
}));
jest.mock("@/lib/utils", () => ({
  cn: (...classes: unknown[]) =>
    classes.filter(Boolean).join(" "),
}));

const mockAvatarProps: Array<{ type: string; id: string | null }> = [];

function makeRow(overrides: Partial<DecisionRowData> = {}): DecisionRowData {
  return {
    id: "d1",
    issueId: "issue-1",
    identifier: "RUYI-530",
    issueTitle: "决策中心优化",
    question: "按哪条路径推进？",
    recommended: false,
    status: "open",
    createdAt: "2026-10-07T12:00:00Z",
    createdByType: "agent",
    createdById: "agent-1",
    ...overrides,
  };
}

beforeEach(() => {
  mockAvatarProps.length = 0;
});

describe("DecisionInboxRow graying (inbox read style)", () => {
  it("keeps full-contrast styling on an open row (negative assertion)", async () => {
    await render(<DecisionInboxRow row={makeRow()} onPress={jest.fn()} />);

    const title = screen.getByText("决策中心优化");
    expect(title.props.className).toContain("text-foreground");
    expect(title.props.className).not.toContain("text-muted-foreground");
  });

  it("grays an answered row's title and secondary line", async () => {
    await render(
      <DecisionInboxRow
        row={makeRow({ status: "answered", recommended: true })}
        onPress={jest.fn()}
      />,
    );

    const title = screen.getByText("决策中心优化");
    expect(title.props.className).toContain("text-muted-foreground");
    expect(title.props.className).not.toContain("text-foreground");

    const question = screen.getByText("按哪条路径推进？");
    expect(question.props.className).toContain("text-muted-foreground/60");

    // 已决策行同时保留 「有推荐」 badge — 灰化不吞掉推荐标记。
    expect(screen.getByText("Has recommendation")).toBeTruthy();
  });

  it("grays a cancelled row the same way", async () => {
    await render(
      <DecisionInboxRow row={makeRow({ status: "cancelled" })} onPress={jest.fn()} />,
    );

    const title = screen.getByText("决策中心优化");
    expect(title.props.className).toContain("text-muted-foreground");
  });
});

// RUYI-622: the row's typography tokens must mirror the tasks-tab issue row
// (issue-row-inbox.tsx) — muted text-xs identifier, semibold text-sm title on
// open rows, text-xs secondary line — so the two bottom-tab lists read as one
// visual language. Decided rows keep the inbox read-style fade on top of the
// same token set.
describe("DecisionInboxRow typography (tasks-tab alignment)", () => {
  it("renders the identifier like the tasks-tab row: text-xs muted, not bold", async () => {
    await render(<DecisionInboxRow row={makeRow()} onPress={jest.fn()} />);

    const id = screen.getByText("RUYI-530");
    expect(id.props.className).toContain("text-xs");
    expect(id.props.className).toContain("text-muted-foreground");
    expect(id.props.className).not.toContain("text-sm");
    expect(id.props.className).not.toContain("font-semibold");
  });

  it("gives an open row's title the tasks-tab weight (font-semibold)", async () => {
    await render(<DecisionInboxRow row={makeRow()} onPress={jest.fn()} />);

    const title = screen.getByText("决策中心优化");
    expect(title.props.className).toContain("text-sm");
    expect(title.props.className).toContain("font-semibold");
    expect(title.props.className).not.toContain("font-medium");
  });

  it("keeps the decided title on the same token set, only the fade differs", async () => {
    await render(
      <DecisionInboxRow row={makeRow({ status: "answered" })} onPress={jest.fn()} />,
    );

    const title = screen.getByText("决策中心优化");
    expect(title.props.className).toContain("text-sm");
    expect(title.props.className).toContain("text-muted-foreground");
    expect(title.props.className).not.toContain("font-semibold");
  });

  it("renders the secondary line at text-xs like the tasks-tab row", async () => {
    await render(<DecisionInboxRow row={makeRow()} onPress={jest.fn()} />);

    const question = screen.getByText("按哪条路径推进？");
    expect(question.props.className).toContain("text-xs");
    expect(question.props.className).not.toContain("text-sm");
    expect(question.props.className).toContain("text-muted-foreground");
  });
});

describe("DecisionInboxRow creator avatar", () => {
  it("passes the card creator's actor identity to the avatar", async () => {
    await render(
      <DecisionInboxRow
        row={makeRow({ createdByType: "agent", createdById: "agent-9" })}
        onPress={jest.fn()}
      />,
    );

    expect(mockAvatarProps[0]).toMatchObject({ type: "agent", id: "agent-9" });
  });

  it("falls back to the system actor when the creator type is unknown", async () => {
    await render(
      <DecisionInboxRow
        row={makeRow({ createdByType: "bot" })}
        onPress={jest.fn()}
      />,
    );

    expect(mockAvatarProps[0]).toMatchObject({ type: "system" });
  });

  it("never renders a blank avatar when the creator id is missing", async () => {
    await render(
      <DecisionInboxRow
        row={makeRow({ createdByType: "", createdById: "" })}
        onPress={jest.fn()}
      />,
    );

    expect(mockAvatarProps[0]).toMatchObject({ type: "system", id: null });
  });
});
