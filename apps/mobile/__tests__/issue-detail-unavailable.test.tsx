import React from "react";
import { fireEvent, render, waitFor } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const ISSUE_ID = "cb240efb-154c-42a8-ae92-42b02676feca";
const mockGetIssue = jest.fn();
const mockBack = jest.fn();
const mockReplace = jest.fn();

jest.mock("expo-router", () => ({
  Stack: { Screen: () => null },
  router: {
    canGoBack: () => true,
    back: (...args: unknown[]) => mockBack(...args),
    replace: (...args: unknown[]) => mockReplace(...args),
    push: jest.fn(),
  },
  useLocalSearchParams: () => ({ id: ISSUE_ID, workspace: "current-workspace" }),
}));

jest.mock("@/data/queries/issues", () => ({
  issueDetailOptions: (_wsId: string, id: string) => ({
    queryKey: ["issues", "workspace-1", "detail", id],
    queryFn: () => mockGetIssue(id),
    enabled: true,
  }),
  issueTimelineOptions: () => ({
    queryKey: ["issues", "workspace-1", "timeline", ISSUE_ID],
    queryFn: async () => ({ entries: [] }),
    enabled: false,
  }),
  issueKeys: { timeline: (_wsId: string, id: string) => ["issues", "workspace-1", "timeline", id] },
}));

jest.mock("@/data/queries/github", () => ({
  githubKeys: { pullRequests: (id: string) => ["github", "pull-requests", id] },
}));
jest.mock("@/data/queries/pins", () => ({
  pinListOptions: () => ({ queryKey: ["pins"], queryFn: async () => [], enabled: false }),
}));
jest.mock("@/data/mutations/issues", () => ({ useDeleteIssue: () => ({ mutate: jest.fn() }) }));
jest.mock("@/data/mutations/pins", () => ({
  useCreatePin: () => ({ mutate: jest.fn() }),
  useDeletePin: () => ({ mutate: jest.fn() }),
}));
jest.mock("@/data/auth-store", () => ({
  useAuthStore: (selector: (state: { user: null }) => unknown) => selector({ user: null }),
}));
jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (state: { currentWorkspaceId: string }) => unknown) =>
    selector({ currentWorkspaceId: "workspace-1" }),
}));
jest.mock("@/data/server-store", () => ({ getWebUrl: () => "https://app.example.test" }));
jest.mock("@/data/viewed-issues-store", () => ({
  useViewedIssuesStore: { getState: () => ({ push: jest.fn() }) },
}));
jest.mock("@/data/comment-select-store", () => ({
  useCommentSelectStore: { getState: () => ({ clear: jest.fn() }) },
}));
jest.mock("@/data/stores/reply-target-store", () => ({
  useReplyTargetStore: { getState: () => ({ clear: jest.fn() }) },
}));
jest.mock("@/data/stores/timeline-sort-store", () => ({
  useTimelineSortStore: (selector: (state: { mode: string; setMode: jest.Mock }) => unknown) =>
    selector({ mode: "oldest", setMode: jest.fn() }),
}));
jest.mock("@/data/realtime/use-issue-realtime", () => ({ useIssueRealtime: jest.fn() }));
jest.mock("@/lib/issue-picker-routes", () => ({ issuePickerHref: () => null }));
jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (_key: string, fallback?: string) => fallback ?? "" }),
}));
jest.mock("@/lib/current-issue-context", () => ({
  CurrentIssueIdProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});
jest.mock("@/components/ui/button", () => {
  const { Pressable } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Button: (props: React.ComponentProps<typeof Pressable>) => <Pressable {...props} /> };
});
jest.mock("@/components/ui/icon-button", () => ({ IconButton: () => null }));
jest.mock("@/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuTrigger: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuItem: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuSeparator: () => null,
}));
jest.mock("@/components/issue/timeline-list", () => ({
  TimelineList: ({ issue }: { issue: { title: string } }) => {
    const { Text } = require("react-native");
    return <Text testID="issue-timeline">{issue.title}</Text>;
  },
}));
jest.mock("@/components/issue/agent-header-badge", () => ({ AgentHeaderBadge: () => null }));
jest.mock("@/components/issue/issue-menu-field-items", () => ({ IssueMenuFieldItems: () => null }));
jest.mock("@/components/issue/inline-comment-composer", () => ({ InlineCommentComposer: () => null }));

import IssueDetail from "@/app/(app)/[workspace]/issue/[id]";

function unavailable(code: string) {
  return Object.assign(new Error("issue unavailable"), {
    body: { error: "issue unavailable", code },
  });
}

async function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return await render(
    <QueryClientProvider client={client}>
      <IssueDetail />
    </QueryClientProvider>,
  );
}

describe("issue detail unavailable states", () => {
  beforeEach(() => {
    mockGetIssue.mockReset();
    mockBack.mockReset();
    mockReplace.mockReset();
  });

  it("renders the cross-workspace state without resource metadata and provides only Back", async () => {
    mockGetIssue.mockRejectedValue(unavailable("cross_workspace_unavailable"));
    const screen = await renderPage();

    await waitFor(() => expect(screen.getByText("This task belongs to another workspace")).toBeTruthy());
    expect(screen.queryByText(/Example|MUL-123|other-workspace/i)).toBeNull();
    const back = screen.getByText("Back");
    expect(back).toBeTruthy();
    fireEvent.press(back);
    expect(mockBack).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("Retry")).toBeNull();
  });

  it("renders the deleted/not-found state and still provides Back", async () => {
    mockGetIssue.mockRejectedValue(unavailable("not_found"));
    const screen = await renderPage();

    await waitFor(() => expect(screen.getByText("This task doesn't exist or has been deleted.")).toBeTruthy());
    expect(screen.getByText("Back")).toBeTruthy();
    expect(screen.queryByText("Retry")).toBeNull();
  });

  it("keeps the normal detail path when the issue loads", async () => {
    mockGetIssue.mockResolvedValue({
      id: ISSUE_ID,
      identifier: "MUL-123",
      title: "Visible current workspace issue",
      assignee_type: null,
      assignee_id: null,
    });
    const screen = await renderPage();

    await waitFor(() => expect(screen.getByTestId("issue-timeline")).toBeTruthy());
    expect(screen.getByText("Visible current workspace issue")).toBeTruthy();
    expect(screen.queryByText("This task belongs to another workspace")).toBeNull();
  });
});
