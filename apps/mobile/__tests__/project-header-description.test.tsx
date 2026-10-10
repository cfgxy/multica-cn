// @ts-nocheck
import React from "react";
import { fireEvent, render, screen } from "@testing-library/react-native";
import { ProjectHeaderCard } from "@/components/project/project-header-card";

/**
 * RUYI-541 — the project description is a long-text field: on the detail
 * header card it now renders truncated (numberOfLines) instead of in full;
 * the tap-through to the edit modal (the independent editing window) is
 * unchanged.
 */

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual("react-native");
  return { Text };
});

jest.mock("@/components/ui/project-icon", () => {
  const React = jest.requireActual("react");
  const { View } = jest.requireActual("react-native");
  return { ProjectIcon: (props) => React.createElement(View, props) };
});

jest.mock("@/lib/use-t", () => ({
  useT: () => ({ t: (key, fallback) => fallback ?? key }),
}));

const LONG_DESCRIPTION =
  "This project tracks the platform migration. ".repeat(30) + "Done by Q4.";

const baseProject = {
  id: "p-1",
  title: "Platform Migration",
  description: LONG_DESCRIPTION,
  icon: "📦",
  issue_count: 0,
  done_count: 0,
};

describe("ProjectHeaderCard description truncation (RUYI-541)", () => {
  it("renders a long description truncated", async () => {
    await render(<ProjectHeaderCard project={baseProject} onEdit={jest.fn()} />);
    expect(screen.getByText(LONG_DESCRIPTION).props.numberOfLines).toBe(4);
  });

  it("tapping the description still opens the edit window", async () => {
    const onEdit = jest.fn();
    await render(<ProjectHeaderCard project={baseProject} onEdit={onEdit} />);
    await fireEvent.press(screen.getByText(LONG_DESCRIPTION));
    expect(onEdit).toHaveBeenCalledTimes(1);
  });

  it("short descriptions are unaffected", async () => {
    await render(
      <ProjectHeaderCard
        project={{ ...baseProject, description: "Short." }}
        onEdit={jest.fn()}
      />,
    );
    expect(screen.getByText("Short.").props.numberOfLines).toBe(4);
  });
});
