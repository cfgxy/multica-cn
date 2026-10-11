/**
 * RUYI-697: the issue-detail "…" menu's first group (assignee / status /
 * project / priority) doubles as a KV readout — key left, current value
 * (+ status/priority glyph) right-aligned — so the common case no longer
 * requires opening a picker just to see the current value.
 *
 * Menu primitives, the svg icon components and the data hooks are mocked as
 * transparent pass-throughs (the @rn-primitives layer measures layout and
 * portals content; react-native-svg doesn't load under jest); assertions run
 * against the rendered texts and the props captured from the icon mocks.
 * Label resolution (`localizedStatusLabel` / `priorityLabel`) stays REAL —
 * only i18next itself is stubbed to its inline defaults.
 */
import { cleanup, fireEvent, render, screen } from "@testing-library/react-native";
import type {
  Issue,
  IssueStatusEntry,
} from "@multica/core/types";

const mockOpenPicker = jest.fn();

/** Mutable state the lazy mock factories read at render time. */
const catalogState: { entries: IssueStatusEntry[] } = { entries: [] };
const projectState: { projects: Array<{ id: string; title: string }> } = {
  projects: [],
};
const actorState: { names: Record<string, string> } = { names: {} };
const iconCalls: { status: unknown[]; priority: unknown[] } = {
  status: [],
  priority: [],
};

jest.mock("@/components/ui/text", () => ({
  Text: jest.requireActual("react-native").Text,
}));

jest.mock("@/components/ui/dropdown-menu", () => {
  const React = jest.requireActual("react");
  const { Pressable, View } = jest.requireActual("react-native");
  return {
    DropdownMenuItem: ({
      onPress,
      children,
    }: {
      onPress?: () => void;
      children?: React.ReactNode;
    }) => React.createElement(Pressable, { onPress }, children),
    // The component only renders items (no content/trigger/separator) —
    // anything else renders as an empty View to keep imports happy.
    DropdownMenu: ({ children }: { children?: React.ReactNode }) =>
      React.createElement(View, null, children),
    DropdownMenuContent: ({ children }: { children?: React.ReactNode }) =>
      React.createElement(View, null, children),
    DropdownMenuSeparator: () => React.createElement(View, null),
  };
});

jest.mock("@/components/ui/status-icon", () => ({
  StatusIcon: (props: unknown) => {
    iconCalls.status.push(props);
    const { View } = jest.requireActual("react-native");
    return <View testID="status-icon" />;
  },
}));

jest.mock("@/components/ui/priority-icon", () => ({
  PriorityIcon: (props: unknown) => {
    iconCalls.priority.push(props);
    const { View } = jest.requireActual("react-native");
    return <View testID="priority-icon" />;
  },
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (state: Record<string, unknown>) => unknown) =>
    selector({ currentWorkspaceId: "workspace-1" }),
}));

jest.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: projectState.projects }),
}));

jest.mock("@/data/queries/projects", () => ({
  projectListOptions: () => ({ queryKey: ["projects"] }),
  findProject: (projects: Array<{ id: string }>, id: string | null) =>
    id ? projects.find((p) => p.id === id) : undefined,
}));

jest.mock("@/data/use-actor-name", () => ({
  useActorLookup: () => ({
    getName: (type: string | null, id: string | null) => {
      if (!type || !id) return "System";
      return actorState.names[`${type}:${id}`] ?? "Unknown";
    },
    getAvatarUrl: () => null,
  }),
}));

jest.mock("@/lib/use-issue-statuses", () => ({
  useIssueStatuses: () =>
    jest
      .requireActual("@/lib/issue-status")
      .buildIssueStatusCatalog(catalogState.entries),
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (key: string, fallback?: string) =>
      typeof fallback === "string" ? fallback : key,
  }),
}));

// The label helpers read the shared i18next instance; under jest it is
// uninitialized, so resolve to the inline English defaults.
jest.mock("i18next", () => ({
  __esModule: true,
  default: {
    t: (_key: string, defaultValue?: unknown) =>
      typeof defaultValue === "string" ? defaultValue : _key,
  },
}));

const IssueMenuFieldItems = jest.requireActual<
  typeof import("@/components/issue/issue-menu-field-items")
>("@/components/issue/issue-menu-field-items").IssueMenuFieldItems;

function makeIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "issue-1",
    identifier: "MUL-1",
    title: "Test issue",
    status: "in_progress",
    priority: "high",
    project_id: "p1",
    assignee_type: "member",
    assignee_id: "u1",
    ...overrides,
  } as Issue;
}

function statusEntry(
  overrides: Partial<IssueStatusEntry> = {},
): IssueStatusEntry {
  return {
    id: "st-1",
    workspace_id: "workspace-1",
    key: "in_progress",
    name: "In Progress",
    description: "",
    category: "in_progress",
    color: "#f59e0b",
    is_system: true,
    position: 0,
    archived_at: null,
    created_at: "2026-01-01T00:00:00Z",
    ...overrides,
  } as IssueStatusEntry;
}

// Async: React 19's act() is a thenable — a sync test would read the render
// result before the tree mounts (same convention as the other screen suites).
async function renderItems(issue: Issue) {
  return render(
    <IssueMenuFieldItems issue={issue} onOpenPicker={mockOpenPicker} />,
  );
}

/** Pre-order traversal collecting every rendered text string. */
function collectTexts(node: unknown, acc: string[] = []): string[] {
  if (node === null || node === undefined) return acc;
  if (typeof node !== "object") {
    if (typeof node === "string") acc.push(node);
    return acc;
  }
  const { props, children } = node as {
    props?: { children?: unknown };
    children?: unknown;
  };
  if (typeof props?.children === "string") acc.push(props.children);
  if (Array.isArray(children)) children.forEach((c) => collectTexts(c, acc));
  if (Array.isArray(props?.children))
    (props!.children as unknown[]).forEach((c) => collectTexts(c, acc));
  return acc;
}

afterEach(() => {
  cleanup();
  jest.clearAllMocks();
  catalogState.entries = [];
  projectState.projects = [];
  actorState.names = {};
  iconCalls.status = [];
  iconCalls.priority = [];
});

it("renders the four field rows in menu order, key left and value right", async () => {
  projectState.projects = [{ id: "p1", title: "Acme" }];
  actorState.names["member:u1"] = "Alice";
  const { toJSON } = await renderItems(makeIssue());

  const texts = collectTexts(toJSON());
  // Interleaved KV pairs in the group's canonical order.
  expect(texts).toEqual([
    "Assignee",
    "Alice",
    "Status",
    "In Progress",
    "Project",
    "Acme",
    "Priority",
    "High",
  ]);
});

it("shows the resolved assignee name for a member assignee", async () => {
  actorState.names["member:u1"] = "Alice";
  await renderItems(makeIssue());

  expect(screen.getByText("Alice")).toBeTruthy();
  expect(screen.queryByText("Unassigned")).toBeNull();
});

it("shows the Unassigned placeholder when no assignee is set", async () => {
  await renderItems(
    makeIssue({ assignee_type: null, assignee_id: null } as Partial<Issue>),
  );

  expect(screen.getByText("Unassigned")).toBeTruthy();
  const texts = collectTexts(screen.toJSON());
  expect(texts).not.toContain("undefined");
  expect(texts).not.toContain("null");
});

it("renders the status glyph from the workspace catalog next to the label", async () => {
  await renderItems(makeIssue({ status: "in_progress" }));

  expect(screen.getByText("In Progress")).toBeTruthy();
  expect(iconCalls.status).toHaveLength(1);
  expect(iconCalls.status[0]).toMatchObject({
    status: "in_progress",
    category: "in_progress",
    color: null,
  });
});

it("resolves a custom status to its catalog name and category glyph", async () => {
  catalogState.entries = [
    statusEntry({
      id: "st-9",
      key: "shipping",
      name: "Shipping",
      category: "in_review",
      color: "#22c55e",
      is_system: false,
    }),
  ];
  await renderItems(makeIssue({ status: "shipping" }));

  expect(screen.getByText("Shipping")).toBeTruthy();
  expect(iconCalls.status[0]).toMatchObject({
    status: "shipping",
    category: "in_review",
    color: "#22c55e",
  });
});

it("shows the project title from the cached project list", async () => {
  projectState.projects = [{ id: "p1", title: "Acme" }];
  await renderItems(makeIssue({ project_id: "p1" }));

  expect(screen.getByText("Acme")).toBeTruthy();
});

it("shows the No project placeholder when no project is set", async () => {
  await renderItems(makeIssue({ project_id: null }));

  expect(screen.getByText("No project")).toBeTruthy();
  const texts = collectTexts(screen.toJSON());
  expect(texts).not.toContain("undefined");
  expect(texts).not.toContain("null");
});

it("renders the priority glyph and label", async () => {
  await renderItems(makeIssue({ priority: "high" }));

  expect(screen.getByText("High")).toBeTruthy();
  expect(iconCalls.priority).toHaveLength(1);
  expect(iconCalls.priority[0]).toMatchObject({ priority: "high" });
});

it("renders a readable No priority placeholder for priority none", async () => {
  await renderItems(makeIssue({ priority: "none" }));

  expect(screen.getByText("No priority")).toBeTruthy();
  expect(iconCalls.priority[0]).toMatchObject({ priority: "none" });
  const texts = collectTexts(screen.toJSON());
  expect(texts).not.toContain("undefined");
  expect(texts).not.toContain("null");
});

it("opens the matching picker when each row is pressed", async () => {
  projectState.projects = [{ id: "p1", title: "Acme" }];
  actorState.names["member:u1"] = "Alice";
  await renderItems(makeIssue());

  fireEvent.press(screen.getByText("Assignee"));
  expect(mockOpenPicker).toHaveBeenLastCalledWith("assignee");
  fireEvent.press(screen.getByText("Status"));
  expect(mockOpenPicker).toHaveBeenLastCalledWith("status");
  fireEvent.press(screen.getByText("Project"));
  expect(mockOpenPicker).toHaveBeenLastCalledWith("project");
  fireEvent.press(screen.getByText("Priority"));
  expect(mockOpenPicker).toHaveBeenLastCalledWith("priority");
});
