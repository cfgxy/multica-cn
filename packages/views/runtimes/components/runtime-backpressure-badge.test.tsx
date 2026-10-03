// @vitest-environment jsdom

import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import type { AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import { BackpressureBadge } from "./runtime-backpressure-badge";
import enRuntimes from "../../locales/en/runtimes.json";
import enCommon from "../../locales/en/common.json";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes },
};

function renderBadge(metadata: AgentRuntime["metadata"]) {
  const runtime = {
    id: "rt-1",
    provider: "cli",
    metadata,
  } as unknown as AgentRuntime;
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <BackpressureBadge runtime={runtime} />
    </I18nProvider>,
  );
}

describe("BackpressureBadge", () => {
  it("renders the warning chip with the hint for an active report", () => {
    renderBadge({
      backpressure: {
        active: true,
        reason: "mem+swap",
        mem_available_pct: 8.4,
        swap_used_pct: 82.1,
        deferred_claims: 12,
      },
    });
    expect(screen.getByTestId("backpressure-badge")).toHaveTextContent(
      "Memory backpressure",
    );
  });

  it("renders nothing for an inactive or missing report", () => {
    const inactive = renderBadge({
      backpressure: { active: false, reason: "", mem_available_pct: 30, swap_used_pct: 10 },
    });
    expect(screen.queryByTestId("backpressure-badge")).toBeNull();
    inactive.unmount();

    renderBadge({});
    expect(screen.queryByTestId("backpressure-badge")).toBeNull();
  });
});
