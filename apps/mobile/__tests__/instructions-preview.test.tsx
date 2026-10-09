// @ts-nocheck
import React from "react";
import { fireEvent, render, screen } from "@testing-library/react-native";
import { InstructionsPreview } from "@/components/ui/instructions-preview";

/**
 * RUYI-541 — shared truncated preview for long prompt fields (squad
 * instructions on the squad detail page, agent instructions on the agent
 * overview). Contract under test:
 *
 *   - the text renders truncated (numberOfLines is forwarded to Text);
 *   - with `onTap` the whole preview is a button and presses route through
 *     it; `canEdit` separately controls the pencil glyph — the recognizable
 *     EDIT affordance. A reader may open a read-only view (onTap, no
 *     canEdit) without ever seeing an edit affordance (squad permission
 *     rule: hide, not disable);
 *   - without `onTap` nothing is pressable and no glyph renders;
 *   - an empty field renders the empty hint instead, and the tap affordance
 *     stays discoverable so a manager can still open the editor.
 */

jest.mock("@expo/vector-icons", () => {
  const React = jest.requireActual("react");
  const { Text } = jest.requireActual("react-native");
  return {
    Ionicons: ({ name }) => React.createElement(Text, { testID: name }),
  };
});

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual("react-native");
  return { Text };
});

const LONG_TEXT =
  "Always start by writing a failing test. ".repeat(40) + "Ship small commits.";

describe("InstructionsPreview", () => {
  it("renders the text truncated (numberOfLines forwarded)", async () => {
    await render(<InstructionsPreview text={LONG_TEXT} numberOfLines={6} />);
    const node = screen.getByText(LONG_TEXT);
    expect(node.props.numberOfLines).toBe(6);
  });

  it("with onTap+canEdit renders a pressable carrying the pencil affordance", async () => {
    const onTap = jest.fn();
    await render(
      <InstructionsPreview
        text={LONG_TEXT}
        numberOfLines={6}
        onTap={onTap}
        canEdit
        accessibilityLabel="Edit instructions"
      />,
    );
    const pressable = screen.getByLabelText("Edit instructions");
    expect(pressable.props.accessibilityRole).toBe("button");
    expect(screen.getByTestId("pencil")).toBeTruthy();
    await fireEvent.press(pressable);
    expect(onTap).toHaveBeenCalledTimes(1);
  });

  it("onTap without canEdit stays pressable but hides the pencil", async () => {
    const onTap = jest.fn();
    await render(
      <InstructionsPreview
        text={LONG_TEXT}
        numberOfLines={6}
        onTap={onTap}
        accessibilityLabel="View instructions"
      />,
    );
    expect(screen.queryByTestId("pencil")).toBeNull();
    await fireEvent.press(screen.getByLabelText("View instructions"));
    expect(onTap).toHaveBeenCalledTimes(1);
  });

  it("without onTap renders read-only: no button role, no pencil", async () => {
    await render(
      <InstructionsPreview text={LONG_TEXT} numberOfLines={6} />,
    );
    expect(screen.getByText(LONG_TEXT)).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
    expect(screen.queryByTestId("pencil")).toBeNull();
  });

  it("empty text renders the empty hint, tap affordance stays discoverable", async () => {
    const onTap = jest.fn();
    await render(
      <InstructionsPreview
        text=""
        emptyHint="No instructions yet"
        numberOfLines={6}
        onTap={onTap}
        canEdit
        accessibilityLabel="Edit instructions"
      />,
    );
    expect(screen.getByText("No instructions yet")).toBeTruthy();
    await fireEvent.press(screen.getByLabelText("Edit instructions"));
    expect(onTap).toHaveBeenCalledTimes(1);
  });

  it("empty text without onTap renders the hint read-only", async () => {
    await render(
      <InstructionsPreview text="" emptyHint="No instructions yet" />,
    );
    expect(screen.getByText("No instructions yet")).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });
});
