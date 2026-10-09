// @vitest-environment jsdom

import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSelfEvolution from "../../locales/en/self-evolution.json";
import { DataSourceStatusCard } from "./data-source-status-card";

/**
 * The four-state status card (RUYI-551 §2.5 / walk #8): every state must
 * answer 状态 → 原因 → 影响 → 操作 in one card, and the status dot carries
 * the health vocabulary as a data attribute so the state is assertable and
 * styleable without parsing English copy.
 */

const TEST_RESOURCES = { en: { common: enCommon, "self-evolution": enSelfEvolution } };

function mount(status: "ok" | "unconfigured" | "error" | "disabled", extra = {}) {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <DataSourceStatusCard
        title="Scoring model"
        status={status}
        reason={<span>because</span>}
        impact={<span>so</span>}
        testId="card"
        {...extra}
      />
    </I18nProvider>,
  );
}

describe("DataSourceStatusCard", () => {
  it("renders each of the four states with its dot and its label", () => {
    const expected: Record<string, string> = {
      ok: "Available",
      unconfigured: "Unconfigured",
      error: "Config error",
      disabled: "Switched off",
    };
    for (const [status, label] of Object.entries(expected)) {
      const { unmount } = mount(status as "ok");
      const dot = screen.getByTestId("card").querySelector("[data-status]");
      expect(dot?.getAttribute("data-status")).toBe(status);
      expect(screen.getByText(label)).toBeTruthy();
      unmount();
    }
  });

  it("answers why → impact → action inside one card, and the source badge only when configured", () => {
    const { rerender } = mount("ok", {
      effectiveSource: "module_config",
      actions: <button>Configure</button>,
    });
    expect(screen.getByText("Why:")).toBeTruthy();
    expect(screen.getByText("Impact:")).toBeTruthy();
    expect(screen.getByText("Source: Module config")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Configure" })).toBeTruthy();

    rerender(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <DataSourceStatusCard
          title="Scoring model"
          status="unconfigured"
          reason={<span>because</span>}
          impact={<span>so</span>}
          testId="card"
        />
      </I18nProvider>,
    );
    // Nothing configured → no origin badge, no origin pretending to be health.
    expect(screen.queryByText(/^Source:/)).toBeNull();
  });
});
