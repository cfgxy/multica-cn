import React from "react";
import { act, renderHook, waitFor } from "@testing-library/react-native";
import type { ChatMessage } from "@multica/core/types";

/**
 * RUYI-416: the chat long-press menu must build its labels through the
 * i18n `chat` namespace (falling back to the same English strings) instead
 * of hardcoded literals, so system-language users see localized items.
 */

const mockT = jest.fn((_key: string, fallback: string) => fallback);

jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: mockT }),
}));

const mockSetSelecting = jest.fn();
jest.mock("@/data/chat-select-store", () => ({
  useChatSelectStore: {
    getState: () => ({ setSelecting: mockSetSelecting }),
  },
}));

jest.mock("expo-haptics", () => ({
  selectionAsync: jest.fn().mockResolvedValue(undefined),
  notificationAsync: jest.fn().mockResolvedValue(undefined),
  NotificationFeedbackType: { Success: "success" },
}));

jest.mock("expo-clipboard", () => ({
  setStringAsync: jest.fn().mockResolvedValue(undefined),
}));

import * as Clipboard from "expo-clipboard";
import { Platform } from "react-native";

// The Android Modal-state branch of useActionSheet is the code under
// test — the iOS branch calls the native ActionSheetIOS module, which
// does not exist under jest. (Same harness as comment-long-press.test.tsx.)
Object.defineProperty(Platform, "OS", { value: "android", configurable: true });

import { useChatMessageLongPress } from "@/components/chat/message-long-press";

function messageWithContent(): ChatMessage {
  return {
    id: "msg-1",
    role: "user",
    content: "hello chat",
    attachments: [],
  } as unknown as ChatMessage;
}

function messageWithoutContent(): ChatMessage {
  const message = messageWithContent();
  message.content = "";
  return message;
}

async function renderLongPress(message: ChatMessage) {
  return renderHook(() => useChatMessageLongPress(message));
}

describe("useChatMessageLongPress i18n labels (RUYI-416)", () => {
  beforeEach(() => {
    mockT.mockClear();
    mockT.mockImplementation((_key: string, fallback: string) => fallback);
    mockSetSelecting.mockClear();
    (Clipboard.setStringAsync as jest.Mock).mockClear();
  });

  it("builds menu labels through chat-namespace t() keys", async () => {
    mockT.mockImplementation((key: string, fallback: string) => `[${key}]${fallback}`);
    const { result, unmount } = await renderLongPress(messageWithContent());
    await act(async () => {
      result.current.onLongPress();
    });
    await waitFor(() => {
      expect(result.current.modalProps.visible).toBe(true);
    });
    const options = result.current.modalProps.sheet!.options as string[];
    expect(options).toContain("[mobile.message_list.menu_copy]Copy");
    expect(options).toContain("[mobile.message_list.menu_select_text]Select Text");
    expect(options).toContain("[common:cancel]Cancel");
    await unmount();
  });

  it("routes Copy to the clipboard with the message content", async () => {
    const { result, unmount } = await renderLongPress(messageWithContent());
    await act(async () => {
      result.current.onLongPress();
    });
    await waitFor(() => {
      expect(result.current.modalProps.visible).toBe(true);
    });
    const options = result.current.modalProps.sheet!.options as string[];
    await act(async () => {
      result.current.modalProps.onSelect(options.indexOf("Copy"));
    });
    await waitFor(() => {
      expect(Clipboard.setStringAsync).toHaveBeenCalledWith("hello chat");
    });
    await unmount();
  });

  it("routes Select Text to the chat select store with the message id", async () => {
    const { result, unmount } = await renderLongPress(messageWithContent());
    await act(async () => {
      result.current.onLongPress();
    });
    await waitFor(() => {
      expect(result.current.modalProps.visible).toBe(true);
    });
    const options = result.current.modalProps.sheet!.options as string[];
    await act(async () => {
      result.current.modalProps.onSelect(options.indexOf("Select Text"));
    });
    await waitFor(() => {
      expect(mockSetSelecting).toHaveBeenCalledWith("msg-1");
    });
    await unmount();
  });

  it("offers only Cancel when the message has no content", async () => {
    const { result, unmount } = await renderLongPress(messageWithoutContent());
    await act(async () => {
      result.current.onLongPress();
    });
    await waitFor(() => {
      expect(result.current.modalProps.visible).toBe(true);
    });
    const options = result.current.modalProps.sheet!.options as string[];
    expect(options).toEqual(["Cancel"]);
    expect(options.indexOf("Cancel")).toBe(
      (result.current.modalProps.sheet!.cancelButtonIndex ?? -1) as number,
    );
    await unmount();
  });
});
