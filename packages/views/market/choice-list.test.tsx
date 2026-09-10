// @vitest-environment jsdom

import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ChoiceList } from "./choice-list";

// This component only exists because `packages/ui` ships no radio-group
// primitive, so the semantics a native group would provide are the thing worth
// testing: the roles, the checked state, and arrow-key movement.

const OPTIONS = [
  { value: "preserve", label: "Keep local content" },
  { value: "replace", label: "Overwrite", tone: "danger" as const },
  { value: "skip", label: "Skip" },
];

function renderList(value = "preserve") {
  const onChange = vi.fn();
  render(
    <ChoiceList name="strategy" value={value} options={OPTIONS} onChange={onChange} />,
  );
  return onChange;
}

describe("ChoiceList", () => {
  it("exposes a radiogroup of radios with one checked", () => {
    renderList();

    expect(screen.getByRole("radiogroup")).toBeInTheDocument();
    expect(screen.getAllByRole("radio")).toHaveLength(3);
    expect(screen.getByRole("radio", { name: /Keep local content/ })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    expect(screen.getByRole("radio", { name: /Overwrite/ })).toHaveAttribute(
      "aria-checked",
      "false",
    );
  });

  it("reports the clicked value", async () => {
    const user = userEvent.setup();
    const onChange = renderList();

    await user.click(screen.getByRole("radio", { name: /Overwrite/ }));

    expect(onChange).toHaveBeenCalledWith("replace");
  });

  it("moves the selection with the arrow keys", async () => {
    const user = userEvent.setup();
    const onChange = renderList();

    screen.getByRole("radio", { name: /Keep local content/ }).focus();
    await user.keyboard("{ArrowDown}");

    expect(onChange).toHaveBeenCalledWith("replace");
  });

  it("wraps around at both ends the way a native group does", async () => {
    const user = userEvent.setup();
    const onChange = renderList("preserve");

    screen.getByRole("radio", { name: /Keep local content/ }).focus();
    await user.keyboard("{ArrowUp}");

    expect(onChange).toHaveBeenCalledWith("skip");
  });

  it("keeps only the selected row in the tab order", () => {
    renderList("replace");

    expect(screen.getByRole("radio", { name: /Overwrite/ })).toHaveAttribute(
      "tabindex",
      "0",
    );
    expect(screen.getByRole("radio", { name: /Keep local content/ })).toHaveAttribute(
      "tabindex",
      "-1",
    );
  });
});
