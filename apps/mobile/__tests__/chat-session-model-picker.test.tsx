// @ts-nocheck
import React from "react";
import { fireEvent, render, screen } from "@testing-library/react-native";
import { StyleSheet } from "react-native";
import { ChatSessionModelPicker } from "@/components/chat/chat-session-model-picker";
import { PlusPanel } from "@/components/chat/plus-panel";

jest.mock("@expo/vector-icons", () => ({ Ionicons: () => null }));
jest.mock("react-native-reanimated", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  const animation = { duration: () => undefined };
  return {
    __esModule: true,
    default: { View: React.forwardRef((props, ref) => React.createElement(View, { ...props, ref })) },
    SlideInUp: animation,
    SlideOutDown: animation,
  };
});
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
  const plusPanelProps = {
    visible: true,
    maxHeight: 400,
    onClose: jest.fn(),
    onMention: jest.fn(),
    onCamera: jest.fn(),
    onImage: jest.fn(),
    onFiles: jest.fn(),
  };

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

  it("offers model choices inside the composer more panel and forwards selection", async () => {
    const onSelect = jest.fn(async () => {});
    await render(
      <PlusPanel
        {...plusPanelProps}
        modelPicker={
          <ChatSessionModelPicker
            runtimeId="runtime-1"
            modelOverride={null}
            agentModel="model-a"
            disabled={false}
            onSelect={onSelect}
            embedded
          />
        }
      />,
    );

    const choices = screen.getAllByTestId("model-option");
    expect(choices).toHaveLength(3);
    expect(screen.getAllByRole("button").some((button) =>
      button.props.className?.includes("h-full w-full"),
    )).toBe(true);
    fireEvent.press(choices[1]);
    expect(onSelect).toHaveBeenCalledWith("model-a");
  });

  it("keeps an eight-cell grid and composer-aligned bounds without a selectable runtime", async () => {
    await render(<PlusPanel {...plusPanelProps} />);

    expect(screen.getAllByTestId("plus-grid-tile")).toHaveLength(7);
    expect(screen.getByTestId("plus-model-cell")).toBeTruthy();
    expect(screen.getByLabelText("Model unavailable").props.accessibilityState?.disabled).toBe(true);
    expect(StyleSheet.flatten(screen.getByTestId("plus-panel").props.style)).toMatchObject({
      left: 12,
      right: 12,
    });
  });
});
