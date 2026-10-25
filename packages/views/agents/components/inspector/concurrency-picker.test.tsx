// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../../locales/en/common.json";
import enAgents from "../../../locales/en/agents.json";
import enIssues from "../../../locales/en/issues.json";
import { ConcurrencyPicker } from "./concurrency-picker";

const TEST_RESOURCES = {
  en: { common: enCommon, agents: enAgents, issues: enIssues },
};

function renderPicker(value = 4) {
  const onChange = vi.fn();
  const view = render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <ConcurrencyPicker value={value} onChange={onChange} />
    </I18nProvider>,
  );
  return { ...view, onChange };
}

describe("ConcurrencyPicker", () => {
  afterEach(cleanup);

  it("rejects a negative value without submitting it", () => {
    const { onChange } = renderPicker();
    fireEvent.click(screen.getByRole("button"));
    fireEvent.change(screen.getByRole("spinbutton"), {
      target: { value: "-1" },
    });
    fireEvent.click(screen.getByText("Save"));

    expect(onChange).not.toHaveBeenCalled();
  });

  it.each([0, 7])("submits valid concurrency %i", (value) => {
    const { onChange } = renderPicker();
    fireEvent.click(screen.getByRole("button"));
    fireEvent.change(screen.getByRole("spinbutton"), {
      target: { value: String(value) },
    });
    fireEvent.click(screen.getByText("Save"));

    expect(onChange).toHaveBeenCalledWith(value);
  });

  it("does not convert an empty draft to unlimited", () => {
    const { onChange } = renderPicker();
    fireEvent.click(screen.getByRole("button"));
    fireEvent.change(screen.getByRole("spinbutton"), {
      target: { value: "" },
    });
    fireEvent.click(screen.getByText("Save"));

    expect(onChange).not.toHaveBeenCalled();
  });
});
