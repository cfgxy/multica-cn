// @ts-nocheck
import React from "react";
import { Alert } from "react-native";
import { act, fireEvent, render, screen } from "@testing-library/react-native";
import type { ChatPendingTask } from "@multica/core/types";
import { ChatQueuePanel } from "@/components/chat/chat-queue-panel";

jest.mock("@expo/vector-icons", () => ({ Ionicons: () => null }));
jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});
jest.mock("@/lib/use-color-scheme", () => ({ useColorScheme: () => ({ colorScheme: "light" }) }));
jest.mock("@/lib/theme", () => ({ THEME: { light: { mutedForeground: "#666", brand: "#00f" } } }));
jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (_key: string, fallback: string, values?: { position?: number }) => fallback?.replace("{{position}}", String(values?.position ?? "")) }),
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

function pendingTask(): ChatPendingTask {
  return {
    task_id: "active-task",
    status: "running",
    supports_queue: true,
    queued_tasks: [{ task_id: "queued-1", content: "Long queued message" }],
  } as ChatPendingTask;
}

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("ChatQueuePanel", () => {
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
    fireEvent.press(screen.getByLabelText("Clear queue"));
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

    fireEvent.press(screen.getByLabelText("Clear queue"));
    const confirm = alertSpy.mock.calls.at(-1)?.[2]?.find((button) => button.style === "destructive");
    expect(confirm).toBeTruthy();
    act(() => confirm!.onPress?.());
    fireEvent.press(screen.getByText("Edit queued message"));
    fireEvent.press(screen.getByLabelText("Clear queue"));
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
