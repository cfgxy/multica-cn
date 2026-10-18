// @ts-nocheck
import React from "react";
import { render, screen } from "@testing-library/react-native";
import { ChatSessionModelPicker } from "@/components/chat/chat-session-model-picker";

jest.mock("@expo/vector-icons", () => ({ Ionicons: () => null }));
jest.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: { supported: true, models: [{ id: "model-a" }, { id: "model-b" }] } }),
}));
jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});
jest.mock("@/components/ui/dropdown-menu", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Pressable, View } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    DropdownMenu: ({ children }) => React.createElement(View, null, children),
    DropdownMenuTrigger: ({ children }) => React.createElement(View, null, children),
    DropdownMenuContent: ({ children }) => React.createElement(View, null, children),
    DropdownMenuItem: ({ children, onPress, disabled }) => React.createElement(Pressable, {
      onPress,
      disabled,
      testID: "model-option",
    }, children),
  };
});
jest.mock("@/lib/runtime-discovery", () => ({ resolveRuntimeModels: jest.fn() }));
jest.mock("@/lib/use-color-scheme", () => ({ useColorScheme: () => ({ colorScheme: "light" }) }));
jest.mock("@/lib/theme", () => ({ THEME: { light: { mutedForeground: "#666", brand: "#00f" } } }));
jest.mock("@/lib/use-t", () => ({ useT: () => ({ t: (_key, fallback) => fallback }) }));

describe("ChatSessionModelPicker", () => {
  it("disables the trigger and all model choices while saving", async () => {
    const onSelect = jest.fn(async () => {});
    await render(
      <ChatSessionModelPicker
        runtimeId="runtime-1"
        modelOverride={null}
        agentModel="model-a"
        disabled
        onSelect={onSelect}
      />,
    );

    const trigger = screen.getByRole("button");
    expect(trigger.props.accessibilityState?.disabled).toBe(true);
    const options = screen.getAllByTestId("model-option");
    expect(options).toHaveLength(3);
    expect(options.every((option) => option.props.accessibilityState?.disabled === true)).toBe(true);
    expect(onSelect).not.toHaveBeenCalled();
  });
});
