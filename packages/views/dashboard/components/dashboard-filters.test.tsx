import { describe, it, expect, vi, afterEach } from "vitest";
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../../test/i18n";
import { WindowFilter, type WindowSelection } from "./dashboard-filters";

// The ‹ / › / back-to-current buttons carry the period-navigation contract:
// › dies at the present (there is no data in the future), ‹ is unbounded,
// and any window away from "now" offers the one-click way home. The picker
// menu itself is Radix portal territory covered by the issues-header tests;
// what is dashboard-specific lives on these plain buttons and the trigger
// label, so that is what gets pinned here.

const QUICK: WindowSelection = { kind: "quick", days: 30, offset: 0 };

function renderFilter(
  selection: WindowSelection = QUICK,
  overrides: Partial<Parameters<typeof WindowFilter>[0]> = {},
) {
  const onQuickRange = vi.fn();
  const onCustomRange = vi.fn();
  const onShift = vi.fn();
  const onBackToCurrent = vi.fn();
  renderWithI18n(
    <WindowFilter
      selection={selection}
      window={overrides.window ?? { start: "2026-02-09", end: "2026-03-10" }}
      today={overrides.today ?? "2026-03-10"}
      onQuickRange={onQuickRange}
      onCustomRange={onCustomRange}
      onShift={onShift}
      onBackToCurrent={onBackToCurrent}
    />,
  );
  return { onQuickRange, onCustomRange, onShift, onBackToCurrent };
}

afterEach(cleanup);

describe("WindowFilter — period navigation", () => {
  it("disables next at the current window — no data lives in the future", () => {
    renderFilter(QUICK, { window: { start: "2026-02-09", end: "2026-03-10" } });

    expect(screen.getByRole("button", { name: "Next period" })).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Previous period" }),
    ).toBeEnabled();
  });

  it("enables next only while a forward step still lands today or earlier", () => {
    // Next from this 30-day window ends exactly on today (Feb 8 + 30 days,
    // 2026 not being a leap year) — allowed.
    renderFilter(QUICK, { window: { start: "2026-01-10", end: "2026-02-08" } });
    expect(
      screen.getByRole("button", { name: "Next period" }),
    ).toBeEnabled();

    // The current window itself: the forward step would cross into tomorrow.
    cleanup();
    renderFilter(QUICK, { window: { start: "2026-02-09", end: "2026-03-10" } });
    expect(screen.getByRole("button", { name: "Next period" })).toBeDisabled();
  });

  it("reports the shift intent instead of moving windows itself", () => {
    const { onShift } = renderFilter(QUICK, {
      window: { start: "2026-01-10", end: "2026-02-08" },
    });

    fireEvent.click(screen.getByRole("button", { name: "Next period" }));
    fireEvent.click(screen.getByRole("button", { name: "Previous period" }));

    expect(onShift).toHaveBeenCalledTimes(2);
    expect(onShift).toHaveBeenNthCalledWith(1, 1);
    expect(onShift).toHaveBeenNthCalledWith(2, -1);
  });

  it("offers the way home only away from the current window", () => {
    renderFilter(QUICK, {
      window: { start: "2026-02-09", end: "2026-03-10" },
    });
    expect(
      screen.queryByRole("button", { name: "Back to current period" }),
    ).not.toBeInTheDocument();

    cleanup();
    const historical = renderFilter(QUICK, {
      window: { start: "2026-01-10", end: "2026-02-08" },
    });
    const home = screen.getByRole("button", {
      name: "Back to current period",
    });
    fireEvent.click(home);
    expect(historical.onBackToCurrent).toHaveBeenCalledTimes(1);
  });

  it("labels the trigger with the actual dates at the current position too", () => {
    renderFilter(QUICK, { window: { start: "2026-02-09", end: "2026-03-10" } });
    // The picker states its position in dates everywhere — including "now",
    // where a bare length label ("30d") left the reader guessing which days
    // the KPIs actually cover. The length still lives on the KPI tiles.
    expect(screen.getByText("Feb 9 – Mar 10")).toBeInTheDocument();
    expect(screen.queryByText("30d")).not.toBeInTheDocument();

    cleanup();
    renderFilter(
      { kind: "quick", days: 30, offset: 1 },
      { window: { start: "2026-01-10", end: "2026-02-08" } },
    );
    // Feb 8 – Mar 9 would be "now"; the shifted window spells its position.
    expect(screen.getByText("Jan 10 – Feb 8")).toBeInTheDocument();
  });

  it("closes the whole menu when a custom range is applied", async () => {
    const user = userEvent.setup();
    const { onCustomRange } = renderFilter();

    fireEvent.click(screen.getByRole("button", { name: "Period" }));
    await waitFor(() =>
      expect(screen.getByText("Custom range")).toBeInTheDocument(),
    );
    await user.click(screen.getByText("Custom range"));
    // The calendar's range is pre-seeded from the current window, so Apply is
    // enabled without touching the day grid — the interaction under test is
    // the close behaviour, not the date picking.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Apply" })).toBeEnabled(),
    );
    await user.click(screen.getByRole("button", { name: "Apply" }));

    expect(onCustomRange).toHaveBeenCalledWith({
      start: "2026-02-09",
      end: "2026-03-10",
    });
    // The menu folds up together with the popover. It used to stay open
    // behind the closed popover, and its inert overlay swallowed every later
    // click on the page (QA BUG-2).
    await waitFor(() => {
      expect(
        screen.queryByRole("menuitemradio", { name: "30d" }),
      ).not.toBeInTheDocument();
    });
  });
});
