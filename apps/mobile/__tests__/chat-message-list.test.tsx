// @ts-nocheck

import React from "react";
import { Text, View } from "react-native";
import { render, userEvent } from "@testing-library/react-native";
import type { ChatMessage } from "@multica/core/types";

/**
 * RUYI-416 rework: the Android long-press menu is a <Modal>-based
 * ActionSheetModal that must be MOUNTED by the row that owns the
 * long-press. The user-bubble branch rendered the Pressable but never
 * mounted the modal, so onLongPress fired (highlight ring) and the sheet
 * never appeared — Android-only, because iOS's ActionSheetIOS is
 * imperative and needs no mounted component.
 */

jest.mock("@shopify/flash-list", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    FlashList: (props: Record<string, unknown>) => {
      const data = props.data as Array<{ id: string }>;
      const renderItem = props.renderItem as (mockArgs: { item: unknown; index: number }) => React.ReactNode;
      return React.createElement(
        View,
        { testID: "chat-message-list" },
        data.map((item, index) =>
          React.createElement(
            View,
            { key: item.id, testID: `chat-row-${item.id}` },
            renderItem({ item, index }),
          ),
        ),
      );
    },
  };
});
jest.mock("@expo/vector-icons", () => ({
  Ionicons: () => null,
}));

jest.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: [] }),
  queryOptions: (opts: unknown) => opts,
}));

jest.mock("@/data/queries/chat", () => ({
  taskMessagesOptions: () => ({ queryKey: ["task-messages"] }),
}));

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});

jest.mock("@/components/ui/selectable-markdown", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    SelectableMarkdown: ({ content }: { content: string }) =>
      React.createElement(Text, null, content),
  };
});

jest.mock("@/lib/markdown/image-sequence", () => ({
  ImageSequenceProvider: ({ children }: { children: React.ReactNode }) => children,
}));

jest.mock("@/components/issue/comment-attachment-list", () => ({
  CommentAttachmentList: () => null,
}));

jest.mock("@/components/chat/chat-timeline", () => ({ ChatTimeline: () => null }));
jest.mock("@/components/chat/status-pill", () => ({ StatusPill: () => null }));
jest.mock("@/components/chat/chat-empty-state", () => ({ ChatEmptyState: () => null }));

jest.mock("@/components/ui/collapsible", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    Collapsible: ({ children }: { children: React.ReactNode }) =>
      React.createElement(View, null, children),
    CollapsibleContent: ({ children }: { children: React.ReactNode }) =>
      React.createElement(View, null, children),
    CollapsibleTrigger: ({ children }: { children: React.ReactNode }) =>
      React.createElement(View, null, children),
  };
});

const mockSelectState = {
  selectingId: null as string | null,
  setSelecting: jest.fn(),
  clear: jest.fn(),
};

jest.mock("@/data/chat-select-store", () => ({
  useChatSelectStore: Object.assign(
    (selector: (state: typeof mockSelectState) => unknown) => selector(mockSelectState),
    { getState: () => mockSelectState },
  ),
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (_key: string, fallback: string) => fallback }),
}));

jest.mock("expo-haptics", () => ({
  selectionAsync: jest.fn().mockResolvedValue(undefined),
  notificationAsync: jest.fn().mockResolvedValue(undefined),
  NotificationFeedbackType: { Success: "success" },
}));

jest.mock("expo-clipboard", () => ({
  setStringAsync: jest.fn().mockResolvedValue(undefined),
}));

jest.mock("react-native-safe-area-context", () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light" }),
}));

jest.mock("@/lib/theme", () => ({
  THEME: { light: { background: "#fff", border: "#ddd", muted: "#eee" } },
}));

// The Android Modal branch of useActionSheet is the code under test —
// the iOS branch calls the native ActionSheetIOS module, which does not
// exist under jest. (Same harness as message-long-press.test.tsx.)
import { Platform } from "react-native";
Object.defineProperty(Platform, "OS", { value: "android", configurable: true });

import { ChatMessageList } from "@/components/chat/chat-message-list";

function userMessage(): ChatMessage {
  return {
    id: "msg-user-1",
    role: "user",
    content: "user bubble content",
    attachments: [],
  } as unknown as ChatMessage;
}

function assistantMessage(): ChatMessage {
  return {
    id: "msg-assistant-1",
    role: "assistant",
    content: "assistant reply content",
    attachments: [],
  } as unknown as ChatMessage;
}

function renderList(messages: ChatMessage[]) {
  return render(
    <ChatMessageList
      messages={messages}
      loading={false}
      hasSessions={true}
      agent={null}
      onPickPrompt={jest.fn()}
    />,
  );
}

describe("ChatMessageList Android long-press sheet (RUYI-416)", () => {
  beforeEach(() => {
    mockSelectState.selectingId = null;
    mockSelectState.setSelecting.mockClear();
    mockSelectState.clear.mockClear();
  });

  async function pressLong(el: React.TestInstance) {
    const user = userEvent.setup({ advanceTimers: jest.advanceTimersByTime });
    await user.longPress(el);
  }

  it("both bubbles mount their own action sheet on long-press", async () => {
    // One list, one test: long-press the assistant bubble first (regression
    // guard — that branch always mounted the sheet), then the user bubble.
    // Each row owns an ActionSheetModal instance, so the second long-press
    // must yield a SECOND set of sheet items. Before the fix the user row
    // never mounted its modal — the long-press fired (highlight ring) but
    // no sheet ever appeared on Android.
    jest.useFakeTimers();
    try {
      const utils = await renderList([assistantMessage(), userMessage()]);
      expect(utils.getByText("assistant reply content")).toBeTruthy();
      expect(utils.getByText("user bubble content")).toBeTruthy();

      await pressLong(utils.getByText("assistant reply content"));
      expect(utils.getAllByText("Copy")).toHaveLength(1);

      await pressLong(utils.getByText("user bubble content"));
      expect(utils.getAllByText("Copy")).toHaveLength(2);
      expect(utils.getAllByText("Select Text")).toHaveLength(2);
      expect(utils.getAllByText("Cancel")).toHaveLength(2);
    } finally {
      jest.useRealTimers();
    }
  });
});
