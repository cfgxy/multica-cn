import React from "react";
import { act, renderHook, waitFor } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { TimelineEntry } from "@multica/core/types";

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (state: { currentWorkspaceSlug: string }) => unknown) =>
    selector({ currentWorkspaceSlug: "ws" }),
}));

jest.mock("@/data/auth-store", () => ({
  useAuthStore: (selector: (state: { user?: { id: string } }) => unknown) =>
    selector({ user: { id: "member-1" } }),
}));

jest.mock("@/data/server-store", () => ({
  getWebUrl: () => "https://web.example.com",
}));

jest.mock("@/data/comment-select-store", () => ({
  useCommentSelectStore: {
    getState: () => ({ setSelecting: jest.fn() }),
  },
}));

jest.mock("@/data/stores/reply-target-store", () => ({
  useReplyTargetStore: {
    getState: () => ({ setTarget: jest.fn(), clear: jest.fn() }),
  },
}));

jest.mock("@/data/use-actor-name", () => ({
  useActorLookup: () => ({ getName: () => "Actor Name" }),
}));

jest.mock("@/data/queries/members", () => ({
  memberListOptions: () => ({
    queryKey: ["members", "ws"] as const,
    queryFn: () => Promise.resolve([]),
    enabled: false,
  }),
}));

const mockMutate = jest.fn();
jest.mock("@/data/mutations/issues", () => ({
  useToggleCommentReaction: () => ({ mutate: mockMutate }),
  useDeleteComment: () => ({ mutate: mockMutate }),
  useResolveComment: () => ({ mutate: mockMutate }),
}));

jest.mock("expo-haptics", () => ({
  selectionAsync: jest.fn().mockResolvedValue(undefined),
  notificationAsync: jest.fn().mockResolvedValue(undefined),
  NotificationFeedbackType: { Success: "success" },
}));

jest.mock("expo-clipboard", () => ({
  setStringAsync: jest.fn().mockResolvedValue(undefined),
}));

jest.mock("expo-router", () => ({
  router: { push: jest.fn() },
}));

// Switchable per test: the default passthrough keeps the fallback-English
// assertions above valid; the i18n suite swaps in a key-prefixed renderer
// to prove the menu labels flow through t() (RUYI-416).
const mockT = jest.fn((_key: string, fallback: string) => fallback);

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: mockT,
  }),
}));

jest.mock("@/lib/quick-emojis", () => ({
  QUICK_EMOJIS: ["😀", "😍", "👍", "🎉", "🚀"],
}));

import { Platform } from "react-native";

// The Android Modal-state branch of useActionSheet is the code under
// test — the iOS branch calls the native ActionSheetIOS module, which
// does not exist under jest. useActionSheet reads Platform.OS at call
// time, so pinning it here routes every sheet through the modal state.
Object.defineProperty(Platform, "OS", { value: "android", configurable: true });

import { useCommentLongPress } from "@/components/issue/comment-context-menu";

function ownComment(): TimelineEntry {
  return {
    id: "comment-1",
    type: "comment",
    actor_type: "member",
    actor_id: "member-1",
    actor_name: "Me",
    content: "hello world",
    parent_id: null,
    created_at: "2026-10-03T01:00:00Z",
    updated_at: "2026-10-03T01:00:00Z",
    resolved_at: null,
    reactions: [],
    attachments: [],
  } as unknown as TimelineEntry;
}

function otherComment(): TimelineEntry {
  const entry = ownComment();
  entry.id = "comment-2";
  entry.actor_id = "member-2";
  entry.actor_name = "Someone else";
  return entry;
}

async function renderLongPress(entry: TimelineEntry) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const harness = await renderHook(
    () => useCommentLongPress(entry, "issue-1", "MUL-381"),
    {
      wrapper: ({ children }: { children: React.ReactNode }) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      ),
    },
  );
  return { ...harness, queryClient };
}

describe("useCommentLongPress cross-platform sheets", () => {
  it("shows the MAIN menu on Android via its own modal props, not the react sheet's", async () => {
    const { result, unmount } = await renderLongPress(ownComment());
    await act(async () => {
      result.current.onLongPress();
    });
    await waitFor(() => {
      expect(result.current.mainModalProps.visible).toBe(true);
    });
    expect(result.current.reactModalProps.visible).toBe(false);
    await unmount();
  });

  it("does not leave the pressed highlight stuck when the sheet opens", async () => {
    const { result, unmount } = await renderLongPress(ownComment());
    await act(async () => {
      result.current.onLongPress();
    });
    await waitFor(() => {
      expect(result.current.mainModalProps.visible).toBe(true);
    });
    const sheet = result.current.mainModalProps.sheet!;
    await act(async () => {
      result.current.mainModalProps.onSelect(
        sheet.options.findIndex((label: string) => label === "Cancel"),
      );
    });
    await waitFor(() => {
      expect(result.current.mainModalProps.visible).toBe(false);
    });
    await unmount();
  });
});

describe("useCommentLongPress edit entry", () => {
  it("offers Edit for the current user's own comment", async () => {
    const { result, unmount } = await renderLongPress(ownComment());
    await act(async () => {
      result.current.onLongPress();
    });
    await waitFor(() => {
      expect(result.current.mainModalProps.visible).toBe(true);
    });
    expect(result.current.mainModalProps.sheet!.options).toContain("Edit");
    await unmount();
  });

  it("offers no Edit on someone else's comment — removing the edit-permission gate makes this assertion fail", async () => {
    const { result, unmount } = await renderLongPress(otherComment());
    await act(async () => {
      result.current.onLongPress();
    });
    await waitFor(() => {
      expect(result.current.mainModalProps.visible).toBe(true);
    });
    expect(result.current.mainModalProps.sheet!.options).not.toContain("Edit");
    await unmount();
  });

  it("routes Edit selection to the edit modal instead of a menu action", async () => {
    const { result, unmount } = await renderLongPress(ownComment());
    await act(async () => {
      result.current.onLongPress();
    });
    await waitFor(() => {
      expect(result.current.mainModalProps.visible).toBe(true);
    });
    const sheet = result.current.mainModalProps.sheet!;
    const editIdx = sheet.options.findIndex((label: string) => label === "Edit");
    expect(editIdx).toBeGreaterThanOrEqual(0);
    await act(async () => {
      result.current.mainModalProps.onSelect(editIdx);
    });
    // onSelect reaches the hook through useActionSheet's deferred
    // (50 ms) completion callback — jest-expo runs real timers, so a
    // plain waitFor polls until the callback has fired.
    await waitFor(() => {
      expect(result.current.isEditing).toBe(true);
    });
    await act(async () => {
      result.current.closeEdit();
    });
    expect(result.current.isEditing).toBe(false);
    await unmount();
  });
});

describe("useCommentLongPress i18n labels (RUYI-416)", () => {
  beforeEach(() => {
    mockT.mockClear();
    mockT.mockImplementation((_key: string, fallback: string) => fallback);
  });

  it("builds the main menu labels through issues-namespace t() keys", async () => {
    mockT.mockImplementation((key: string, fallback: string) => `[${key}]${fallback}`);
    const { result, unmount } = await renderLongPress(ownComment());
    await act(async () => {
      result.current.onLongPress();
    });
    await waitFor(() => {
      expect(result.current.mainModalProps.visible).toBe(true);
    });
    const options = result.current.mainModalProps.sheet!.options as string[];
    expect(options).toContain("[mobile.comment.menu_reply]Reply");
    expect(options).toContain("[mobile.comment.menu_react]React…");
    expect(options).toContain("[mobile.comment.menu_copy]Copy");
    expect(options).toContain("[mobile.comment.menu_select_text]Select Text");
    expect(options).toContain("[mobile.comment.menu_copy_link]Copy Link");
    expect(options).toContain("[mobile.comment.menu_edit]Edit");
    expect(options).toContain("[common:cancel]Cancel");
    await unmount();
  });

  it("builds the nested React sheet labels through t() keys", async () => {
    mockT.mockImplementation((key: string, fallback: string) => `[${key}]${fallback}`);
    const { result, unmount } = await renderLongPress(ownComment());
    await act(async () => {
      result.current.onLongPress();
    });
    await waitFor(() => {
      expect(result.current.mainModalProps.visible).toBe(true);
    });
    const options = result.current.mainModalProps.sheet!.options as string[];
    await act(async () => {
      result.current.mainModalProps.onSelect(options.indexOf("[mobile.comment.menu_react]React…"));
    });
    await waitFor(() => {
      expect(result.current.reactModalProps.visible).toBe(true);
    });
    const reactOptions = result.current.reactModalProps.sheet!.options as string[];
    expect(reactOptions).toContain("[mobile.comment.menu_more_reactions]More reactions…");
    expect(reactOptions).toContain("[common:cancel]Cancel");
    mockT.mockImplementation((_key: string, fallback: string) => fallback);
    await unmount();
  });
});
