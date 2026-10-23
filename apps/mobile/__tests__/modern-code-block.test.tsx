import React from "react";
import { fireEvent, render } from "@testing-library/react-native";

jest.mock("@/lib/markdown/shiki", () => ({
  highlight: jest.fn(),
  resolveLang: () => null,
  SHIKI_THEME_DARK: "dark",
  SHIKI_THEME_LIGHT: "light",
}));
jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ isDarkColorScheme: false }),
}));
jest.mock("@/lib/theme", () => ({
  THEME: {
    light: { success: "green", mutedForeground: "gray" },
    dark: { success: "green", mutedForeground: "gray" },
  },
}));
jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual("react-native");
  return { Text };
});
jest.mock("expo-clipboard", () => ({ setStringAsync: jest.fn() }));
jest.mock("expo-haptics", () => ({
  impactAsync: jest.fn(),
  ImpactFeedbackStyle: { Light: "light" },
}));

import { ModernCodeBlock } from "@/lib/markdown/code-block";

describe("ModernCodeBlock", () => {
  it("switches code between horizontal scrolling and wrapped lines", async () => {
    const screen = await render(<ModernCodeBlock code="long code line" />);

    expect(screen.getByTestId("code-block-scroll").props.horizontal).toBe(true);
    expect(screen.getByRole("button", { name: "Wrap code lines" })).toBeTruthy();

    await fireEvent.press(screen.getByRole("button", { name: "Wrap code lines" }));
    expect(screen.getByTestId("code-block-scroll").props.horizontal).toBe(false);
    expect(screen.getByTestId("code-block-scroll").props.scrollEnabled).toBe(false);

    await fireEvent.press(screen.getByRole("button", { name: "Scroll code horizontally" }));
    expect(screen.getByTestId("code-block-scroll").props.horizontal).toBe(true);
    expect(screen.getByTestId("code-block-scroll").props.scrollEnabled).toBe(true);
  });
});
