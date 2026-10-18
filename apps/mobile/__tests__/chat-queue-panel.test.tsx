// @ts-nocheck
import React from "react";
import { Alert } from "react-native";
import { createInstance } from "i18next";
import { act, fireEvent, render, screen } from "@testing-library/react-native";
import type { ChatPendingTask } from "@multica/core/types";
import { RESOURCES } from "@multica/views/locales";
import { ChatQueuePanel } from "@/components/chat/chat-queue-panel";

jest.mock("@expo/vector-icons", () => ({ Ionicons: () => null }));
jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});
jest.mock("@/lib/use-color-scheme", () => ({ useColorScheme: () => ({ colorScheme: "light" }) }));
jest.mock("@/lib/theme", () => ({ THEME: { light: { mutedForeground: "#666", brand: "#00f" } } }));
jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (_key: string, fallback: string, values?: { position?: number; number?: number }) => fallback?.replace("{{position}}", String(values?.position ?? "")).replace("{{number}}", String(values?.number ?? "")) }),
}));
jest.mock("@/components/ui/dropdown-menu", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Pressable, View } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    DropdownMenu: ({ children }: { children: React.ReactNode }) => React.createElement(View, null, children),
    DropdownMenuTrigger: ({ children }: { children: React.ReactNode }) => React.createElement(View, null, children),
    DropdownMenuContent: ({ children }: { children: React.ReactNode }) => React.createElement(View, null, children),
    DropdownMenuItem: ({ children, onPress, disabled }: { children: React.ReactNode; onPress?: () => void; disabled?: boolean }) => React.createElement(Pressable, { onPress, disabled }, children),
  };
});

function pendingTask(count = 1): ChatPendingTask {
  return {
    task_id: "active-task",
    status: "running",
    supports_queue: true,
    queued_tasks: Array.from({ length: count }, (_, index) => ({
      task_id: `queued-${index + 1}`,
      content: "Long queued message",
    })),
  } as ChatPendingTask;
}

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("ChatQueuePanel", () => {
  it("resolves localized queue text and count plurals from the registered resources", async () => {
    const translator = createInstance();
    await translator.init({
      resources: RESOURCES,
      lng: "en",
      fallbackLng: "en",
      defaultNS: "common",
      interpolation: { escapeValue: false },
    });

    expect(translator.t("queue.title_single", { ns: "chat", number: 1 })).toBe("1 queued message");
    expect(translator.t("queue.title_multiple", { ns: "chat", number: 2 })).toBe("2 queued messages");
    expect(translator.t("queue.clear_confirm_title", { ns: "chat" })).toBe("Clear queued messages?");
    expect(translator.t("queue.clear_confirm_body", { ns: "chat" })).toBe("This removes queued messages only. The current task will keep running.");

    await translator.changeLanguage("zh-Hans");
    const pluralRulesDescriptor = Object.getOwnPropertyDescriptor(Intl, "PluralRules");
    const NativePluralRules = Intl.PluralRules;
    Object.defineProperty(Intl, "PluralRules", {
      configurable: true,
      value: class extends NativePluralRules {
        select() { return "one"; }
      },
    });
    try {
      const legacyTranslator = createInstance();
      await legacyTranslator.init({
        resources: {
          en: { chat: { queue: { title_one: "{{count}} queued message", title_other: "{{count}} queued messages" } } },
          "zh-Hans": { chat: { queue: { title_other: "{{count}} 条排队消息" } } },
        },
        lng: "zh-Hans",
        fallbackLng: "en",
        interpolation: { escapeValue: false },
      });
      expect(legacyTranslator.t("queue.title", { ns: "chat", count: 1 })).toBe("1 queued message");
      expect(translator.t("queue.title_single", { ns: "chat", number: 1 })).toBe("1 条排队消息");
      expect(translator.t("queue.title_multiple", { ns: "chat", number: 2 })).toBe("2 条排队消息");
    } finally {
      if (pluralRulesDescriptor) Object.defineProperty(Intl, "PluralRules", pluralRulesDescriptor);
    }
    expect(translator.t("queue.clear", { ns: "chat" })).toBe("全部清空");
    expect(translator.t("queue.clear_confirm_title", { ns: "chat" })).toBe("清空排队消息？");
    expect(translator.t("queue.clear_confirm_body", { ns: "chat" })).toBe("仅清空排队中的消息，当前任务会继续运行。");
    expect(translator.t("cancel", { ns: "common" })).toBe("取消");
  });

  it("uses clear confirmation copy and the shared cancel label", async () => {
    const alertSpy = jest.spyOn(Alert, "alert");
    await render(
      <ChatQueuePanel
        pendingTask={pendingTask()}
        maxHeight={96}
        canManage
        onClear={jest.fn(async () => {})}
        onEdit={jest.fn(async () => {})}
        onRemove={jest.fn(async () => {})}
        onPrioritize={jest.fn(async () => {})}
      />,
    );

    expect(screen.getByText("1 queued message")).toBeTruthy();
    expect(screen.getByLabelText("1 queued message")).toBeTruthy();
    fireEvent.press(screen.getByLabelText("Clear all"));
    const [title, body, buttons] = alertSpy.mock.calls.at(-1)!;
    expect(title).toBe("Clear queued messages?");
    expect(body).toBe("This removes queued messages only. The current task will keep running.");
    expect(buttons?.[0]?.text).toBe("Cancel");
    alertSpy.mockRestore();
  });

  it("disables queue management when the session cannot be managed", async () => {
    const onClear = jest.fn(async () => {});
    const onEdit = jest.fn(async () => {});
    await render(
      <ChatQueuePanel
        pendingTask={pendingTask()}
        maxHeight={96}
        canManage={false}
        onClear={onClear}
        onEdit={onEdit}
        onRemove={jest.fn(async () => {})}
        onPrioritize={jest.fn(async () => {})}
      />,
    );

    const alertSpy = jest.spyOn(Alert, "alert");
    fireEvent.press(screen.getByLabelText("Clear all"));
    fireEvent.press(screen.getByText("Edit queued message"));
    expect(alertSpy).not.toHaveBeenCalled();
    expect(onClear).not.toHaveBeenCalled();
    expect(onEdit).not.toHaveBeenCalled();
    alertSpy.mockRestore();
  });

  it("shares one busy guard between clear and item actions", async () => {
    const clear = deferred();
    const alertSpy = jest.spyOn(Alert, "alert");
    const onClear = jest.fn(() => clear.promise);
    const onEdit = jest.fn(async () => {});
    await render(
      <ChatQueuePanel
        pendingTask={pendingTask()}
        maxHeight={96}
        canManage
        onClear={onClear}
        onEdit={onEdit}
        onRemove={jest.fn(async () => {})}
        onPrioritize={jest.fn(async () => {})}
      />,
    );

    fireEvent.press(screen.getByLabelText("Clear all"));
    const confirm = alertSpy.mock.calls.at(-1)?.[2]?.find((button) => button.style === "destructive");
    expect(confirm).toBeTruthy();
    await act(async () => {
      confirm!.onPress?.();
      await Promise.resolve();
    });
    fireEvent.press(screen.getByText("Edit queued message"));
    fireEvent.press(screen.getByLabelText("Clear all"));
    await act(async () => {
      clear.resolve();
      await clear.promise;
    });

    expect(onClear).toHaveBeenCalledTimes(1);
    expect(onEdit).not.toHaveBeenCalled();
    expect(alertSpy).toHaveBeenCalledTimes(1);
    alertSpy.mockRestore();
  });
});
