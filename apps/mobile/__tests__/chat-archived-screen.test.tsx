// @ts-nocheck
import React from "react";
import { Alert } from "react-native";
import { fireEvent, render, screen } from "@testing-library/react-native";
import ArchivedChatsScreen from "@/app/(app)/[workspace]/chat/archived";

/**
 * RUYI-533 — the Archived chats sub-view: reached from the footer entry at
 * the bottom of the chat tab list (same pattern as the RUYI-532 archived
 * inbox sub-view). These tests pin the contract: only archived sessions
 * render, rows open the (read-only) chat detail, long-press offers
 * Unarchive / Delete, and the empty state renders once the view drains.
 */

const mockRouterPush = jest.fn();

jest.mock("expo-router", () => ({
  router: {
    push: (...args) => mockRouterPush(...args),
    back: jest.fn(),
    replace: jest.fn(),
  },
}));

const mockQueryFlags = { isLoading: false, isError: false };
const mockSessions = [];
const mockRefetch = jest.fn();

jest.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[] }) => {
    const isSessions = opts.queryKey[1] === "ws-1" && opts.queryKey[2] === "sessions";
    if (isSessions) {
      return {
        data: mockSessions,
        isLoading: mockQueryFlags.isLoading,
        isError: mockQueryFlags.isError,
        error: mockQueryFlags.isError ? new Error("boom") : null,
        refetch: mockRefetch,
        isRefetching: false,
      };
    }
    return { data: [], isLoading: false };
  },
}));

jest.mock("@/data/queries/chat", () => ({
  chatSessionsOptions: (wsId) => ({
    queryKey: ["chat", wsId, "sessions"],
    queryFn: jest.fn(),
  }),
  // Mirror of the real helper's split (unit-tested on the vitest lane).
  splitChatSessions: (arr) => ({
    active: arr.filter((s) => s.status !== "archived"),
    archived: arr.filter((s) => s.status === "archived"),
  }),
}));

const mockSetArchived = jest.fn();
const mockDelete = jest.fn();

jest.mock("@/data/mutations/chat", () => ({
  useDeleteChatSession: () => ({ mutate: mockDelete }),
  useSetChatSessionArchived: () => ({ mutate: mockSetArchived }),
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector) =>
    selector({ currentWorkspaceId: "ws-1", currentWorkspaceSlug: "acme" }),
}));

const mockSheetShow = jest.fn();

jest.mock("@/components/ui/action-sheet", () => ({
  useActionSheet: () => ({ show: (...args) => mockSheetShow(...args) }),
  ActionSheetModal: () => null,
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (key, fallback) => fallback ?? key }),
}));

jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light", isDarkColorScheme: false }),
}));

jest.mock("@/lib/theme", () => ({
  THEME: {
    light: { background: "#fff", foreground: "#000", mutedForeground: "#666", secondary: "#eee" },
    dark: {},
  },
}));

jest.mock("@/components/ui/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});

jest.mock("@/components/ui/button", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Pressable } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    Button: ({ variant: _v, size: _s, ...props }) => React.createElement(Pressable, props),
  };
});

jest.mock("@/components/ui/skeleton", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Skeleton: (props) => React.createElement(View, { testID: "skeleton", ...props }) };
});

jest.mock("expo-haptics", () => ({
  selectionAsync: jest.fn(async () => {}),
  impactAsync: jest.fn(async () => {}),
}));

beforeEach(() => {
  jest.clearAllMocks();
  mockSessions.length = 0;
  mockQueryFlags.isLoading = false;
  mockQueryFlags.isError = false;
});

function seedSessions() {
  mockSessions.push(
    {
      id: "sc",
      workspace_id: "ws-1",
      agent_id: "agent-1",
      creator_id: "user-1",
      title: "Old chat",
      status: "archived",
      has_unread: false,
      updated_at: "2026-10-06T07:00:00Z",
    },
    {
      id: "sd",
      workspace_id: "ws-1",
      agent_id: "agent-1",
      creator_id: "user-1",
      title: "Newer archived chat",
      status: "archived",
      has_unread: false,
      updated_at: "2026-10-07T07:00:00Z",
    },
    {
      id: "sb",
      workspace_id: "ws-1",
      agent_id: "agent-1",
      creator_id: "user-1",
      title: "Regular chat",
      status: "active",
      has_unread: false,
      updated_at: "2026-10-07T09:00:00Z",
    },
  );
}

describe("ArchivedChatsScreen (RUYI-533)", () => {
  it("renders only archived sessions — active ones stay in the tab list", async () => {
    seedSessions();
    await render(<ArchivedChatsScreen />);
    expect(await screen.findByTestId("chat-row-sc")).toBeTruthy();
    expect(screen.getByTestId("chat-row-sd")).toBeTruthy();
    expect(screen.queryByText("Regular chat")).toBeNull();
  });

  it("tapping a row opens the (read-only) chat detail", async () => {
    seedSessions();
    await render(<ArchivedChatsScreen />);
    fireEvent.press(await screen.findByTestId("chat-row-sc"));
    expect(mockRouterPush).toHaveBeenCalledWith(
      expect.objectContaining({
        pathname: "/[workspace]/chat/[sessionId]",
        params: expect.objectContaining({ workspace: "acme", sessionId: "sc" }),
      }),
    );
  });

  it("long-press unarchives the session via the action sheet", async () => {
    seedSessions();
    await render(<ArchivedChatsScreen />);
    fireEvent(await screen.findByTestId("chat-row-sc"), "longPress");
    const sheet = mockSheetShow.mock.calls[0][0];
    const unarchiveIndex = sheet.options.indexOf("Unarchive chat");
    expect(unarchiveIndex).toBeGreaterThanOrEqual(0);
    sheet.onSelect(unarchiveIndex);
    expect(mockSetArchived).toHaveBeenCalledWith({
      sessionId: "sc",
      archived: false,
    });
  });

  it("long-press delete confirms via Alert before mutating", async () => {
    seedSessions();
    const alertSpy = jest.spyOn(Alert, "alert");
    await render(<ArchivedChatsScreen />);
    fireEvent(await screen.findByTestId("chat-row-sc"), "longPress");
    const sheet = mockSheetShow.mock.calls[0][0];
    sheet.onSelect(sheet.options.indexOf("Delete chat"));
    expect(mockDelete).not.toHaveBeenCalled();
    const buttons = alertSpy.mock.calls[0][2];
    buttons.find((b) => b.text === "Delete").onPress();
    expect(mockDelete).toHaveBeenCalledWith("sc");
    alertSpy.mockRestore();
  });

  it("shows an empty state once the archived view drains", async () => {
    await render(<ArchivedChatsScreen />);
    expect(await screen.findByTestId("chat-archived-empty")).toBeTruthy();
  });

  it("shows a loading skeleton while sessions load", async () => {
    mockQueryFlags.isLoading = true;
    await render(<ArchivedChatsScreen />);
    expect(screen.getByTestId("chat-archived-loading")).toBeTruthy();
  });

  it("shows an error state whose retry refetches", async () => {
    mockQueryFlags.isError = true;
    await render(<ArchivedChatsScreen />);
    fireEvent.press(await screen.findByText("Retry"));
    expect(mockRefetch).toHaveBeenCalled();
  });
});
