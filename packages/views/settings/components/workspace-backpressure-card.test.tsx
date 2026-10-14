// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { WorkspaceBackpressureSettings } from "@multica/core/types";
import enSettings from "../../locales/en/settings.json";
import {
  WorkspaceBackpressureCard,
  validateBackpressureDraft,
} from "./workspace-backpressure-card";

/**
 * The host-backpressure card (RUYI-740). What only a mount can show: every
 * numeric input carries a visible label and unit suffix (no aria-only
 * columns), the client-side validator mirrors the server's Validate rules
 * before the 422 round trip — relation violations are flagged on the
 * recovery side only, a 0 pause threshold is fatal on memory but a
 * disable-sentinel on swap/PSI, empty fields stay unflagged while blocking
 * the save — and the save button's summary names the reason.
 */

const TEST_RESOURCES = { en: { settings: enSettings } };

const PSI_INFO =
  "PSI (Pressure Stall Information) is the kernel's share of time tasks stalled waiting for memory; avg10 averages the last 10 seconds.";

const m = vi.hoisted(() => ({
  settings: null as WorkspaceBackpressureSettings | null,
  saveMutate: vi.fn(),
}));

vi.mock("@multica/core/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/workspace")>(
      "@multica/core/workspace",
    );
  return {
    ...actual,
    workspaceBackpressureSettingsOptions: (wsId: string) => ({
      queryKey: ["backpressure-settings", wsId],
      queryFn: () => Promise.resolve(m.settings),
    }),
    useSaveWorkspaceBackpressureSettings: () => ({
      mutateAsync: m.saveMutate,
      isPending: false,
    }),
  };
});

function settingsFixture(
  overrides: Partial<WorkspaceBackpressureSettings> = {},
): WorkspaceBackpressureSettings {
  return {
    enabled: true,
    mem_high_pct: 15,
    mem_recovery_pct: 25,
    swap_high_pct: 80,
    swap_recovery_pct: 60,
    psi_high_pct: 50,
    psi_recovery_pct: 20,
    sample_interval_seconds: 5,
    window_size: 6,
    custom: false,
    ...overrides,
  };
}

function mount(canManage = true) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <WorkspaceBackpressureCard wsId="ws-1" canManage={canManage} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

/** The three threshold pairs share visible labels; document order is mem/swap/psi. */
async function pair(pairIndex: number): Promise<[HTMLInputElement, HTMLInputElement]> {
  // findAllBy waits for the settings query to resolve and the form to mount.
  const highs = await screen.findAllByLabelText("Pause threshold");
  const recoveries = await screen.findAllByLabelText("Resume threshold");
  return [highs[pairIndex] as HTMLInputElement, recoveries[pairIndex] as HTMLInputElement];
}

async function samplingFields(): Promise<[HTMLInputElement, HTMLInputElement]> {
  return [
    (await screen.findByLabelText("Sample interval")) as HTMLInputElement,
    (await screen.findByLabelText("Smoothing window")) as HTMLInputElement,
  ];
}

function saveButton(): HTMLButtonElement {
  return screen.getByRole("button", { name: "Save" }) as HTMLButtonElement;
}

function saveSummary(): HTMLElement | null {
  return screen.queryByRole("status");
}

beforeEach(() => {
  m.settings = settingsFixture();
  m.saveMutate.mockReset();
  m.saveMutate.mockImplementation((vars) =>
    Promise.resolve({ ...vars, custom: true }),
  );
});

describe("WorkspaceBackpressureCard", () => {
  it("labels every numeric input visibly and inlines its unit", async () => {
    mount();

    // Nine inputs across four rows: 3×(pause, resume) + interval + window.
    expect(await screen.findAllByLabelText("Pause threshold")).toHaveLength(3);
    expect(screen.getAllByLabelText("Resume threshold")).toHaveLength(3);
    expect(screen.getByLabelText("Sample interval")).toBeTruthy();
    expect(screen.getByLabelText("Smoothing window")).toBeTruthy();

    // Units render inside every box; the two disable-sentinel rows advertise
    // "0 = off" under the pause box; PSI's ⓘ carries its explainer.
    expect(screen.getAllByText("%")).toHaveLength(6);
    expect(screen.getByText("s")).toBeTruthy();
    expect(screen.getByText("samples")).toBeTruthy();
    expect(screen.getAllByText("Set 0 to disable this check")).toHaveLength(2);
    expect(screen.getByRole("button", { name: PSI_INFO })).toBeTruthy();
  });

  it("renders the untouched defaults and saves an explicit edited card", async () => {
    mount();

    const [memHigh, memRecovery] = await pair(0);

    // Defaults prefill the form; the defaults note is visible until a save.
    expect(memHigh.value).toBe("15");
    expect(
      screen.getByText(
        "Showing the code defaults — nothing saved for this workspace yet.",
      ),
    ).toBeTruthy();

    await userEvent.clear(memHigh);
    await userEvent.type(memHigh, "20");
    await userEvent.clear(memRecovery);
    await userEvent.type(memRecovery, "30");

    await userEvent.click(saveButton());
    await waitFor(() => expect(m.saveMutate).toHaveBeenCalledTimes(1));
    expect(m.saveMutate).toHaveBeenCalledWith({
      enabled: true,
      mem_high_pct: 20,
      mem_recovery_pct: 30,
      swap_high_pct: 80,
      swap_recovery_pct: 60,
      psi_high_pct: 50,
      psi_recovery_pct: 20,
      sample_interval_seconds: 5,
      window_size: 6,
    });

    // The mock echoes the saved card: the defaults note must go away.
    await waitFor(() =>
      expect(
        screen.queryByText(
          "Showing the code defaults — nothing saved for this workspace yet.",
        ),
      ).toBeNull(),
    );
  });

  it("flags a memory recovery at or below the pause threshold on the recovery side only", async () => {
    mount();

    const [memHigh, memRecovery] = await pair(0);
    await userEvent.clear(memHigh);
    await userEvent.type(memHigh, "25");
    await userEvent.clear(memRecovery);
    await userEvent.type(memRecovery, "25");

    // Condition: recovery == pause on the memory row → the recovery box goes
    // red with the hysteresis message, the pause box stays clean.
    expect(memRecovery.getAttribute("aria-invalid")).toBe("true");
    expect(memHigh.getAttribute("aria-invalid")).toBeNull();
    expect(
      screen.getByText(
        "The resume threshold must be above the pause threshold",
      ),
    ).toBeTruthy();
    expect(saveButton().disabled).toBe(true);
    expect(saveSummary()?.textContent).toBe(
      "1 invalid fields — fix them to save",
    );

    // Condition: recovery back above pause → the error, the red border and
    // the summary all clear and saving re-enables.
    await userEvent.clear(memRecovery);
    await userEvent.type(memRecovery, "30");
    expect(memRecovery.getAttribute("aria-invalid")).toBeNull();
    expect(saveSummary()).toBeNull();
    expect(saveButton().disabled).toBe(false);
  });

  it("treats a 0 pause threshold as fatal on memory but a sentinel on swap", async () => {
    mount();

    // Memory has no 0-disables sentinel: 0 would pause every claim forever.
    const [memHigh] = await pair(0);
    await userEvent.clear(memHigh);
    await userEvent.type(memHigh, "0");
    expect(memHigh.getAttribute("aria-invalid")).toBe("true");
    expect(
      screen.getByText("The pause threshold must be greater than 0"),
    ).toBeTruthy();
    expect(saveButton().disabled).toBe(true);

    // Swap's 0 disables the whole row: any recovery value stays legal.
    const [swapHigh, swapRecovery] = await pair(1);
    await userEvent.clear(memHigh);
    await userEvent.type(memHigh, "15");
    await userEvent.clear(swapHigh);
    await userEvent.type(swapHigh, "0");
    await userEvent.clear(swapRecovery);
    await userEvent.type(swapRecovery, "99");
    expect(swapHigh.getAttribute("aria-invalid")).toBeNull();
    expect(swapRecovery.getAttribute("aria-invalid")).toBeNull();
    expect(saveButton().disabled).toBe(false);
  });

  it("flags a swap or PSI recovery at or above an enabled pause threshold", async () => {
    mount();

    // Swap: recovery == pause (80) on an enabled row → red on the recovery
    // side, save blocked with the below-threshold message.
    const [, swapRecovery] = await pair(1);
    await userEvent.clear(swapRecovery);
    await userEvent.type(swapRecovery, "80");
    expect(swapRecovery.getAttribute("aria-invalid")).toBe("true");
    expect(
      screen.getByText(
        "The resume threshold must sit below the pause threshold",
      ),
    ).toBeTruthy();
    expect(saveButton().disabled).toBe(true);

    // PSI: same rule, independently flagged.
    const [, psiRecovery] = await pair(2);
    await userEvent.clear(psiRecovery);
    await userEvent.type(psiRecovery, "50");
    expect(psiRecovery.getAttribute("aria-invalid")).toBe("true");
    expect(saveButton().disabled).toBe(true);
  });

  it("rejects out-of-range and fractional sampling values at the protocol bounds", async () => {
    mount();

    const [interval, window] = await samplingFields();
    // The native clamp still advertises the protocol ceiling (3600).
    expect(interval.min).toBe("1");
    expect(interval.max).toBe("3600");
    expect(window.min).toBe("1");
    expect(window.max).toBe("3600");

    // Condition: hand-typed out-of-range value → red border + range message;
    // hand-typing must not be silently rewritten by min/max.
    await userEvent.clear(interval);
    await userEvent.type(interval, "3601");
    expect(interval.value).toBe("3601");
    expect(interval.getAttribute("aria-invalid")).toBe("true");
    expect(
      screen.getByText("Must be a whole number from 1 to 3600"),
    ).toBeTruthy();
    expect(saveButton().disabled).toBe(true);

    // Condition: fractional seconds → same failure (the wire type is int).
    await userEvent.clear(interval);
    await userEvent.type(interval, "2.5");
    expect(interval.getAttribute("aria-invalid")).toBe("true");
    expect(saveButton().disabled).toBe(true);

    // Condition: legal value → error clears, save re-enables.
    await userEvent.clear(interval);
    await userEvent.type(interval, "30");
    expect(interval.getAttribute("aria-invalid")).toBeNull();
    expect(saveButton().disabled).toBe(false);
  });

  it("stays unflagged but unsaveable while a field is empty", async () => {
    mount();

    const [memHigh, memRecovery] = await pair(0);
    await userEvent.clear(memHigh);
    await userEvent.type(memHigh, "40");
    await userEvent.clear(memRecovery);

    // Condition: empty field mid-edit → no red anywhere (typing states must
    // not flash), save disabled, summary names the incompleteness.
    const inputs = screen.getAllByRole("spinbutton");
    expect(
      inputs.every((el) => el.getAttribute("aria-invalid") === null),
    ).toBe(true);
    expect(saveButton().disabled).toBe(true);
    expect(saveSummary()?.textContent).toBe(
      "Some fields are empty — fill in every field to save",
    );
  });

  it("surfaces the shared validation sentence when the server refuses the card", async () => {
    m.saveMutate.mockRejectedValueOnce(
      new Error(
        "backpressure: mem high watermark 80% must sit below recovery 30%",
      ),
    );
    mount();

    await userEvent.click(await screen.findByRole("button", { name: "Save" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Invalid settings:");
    expect(alert.textContent).toContain(
      "backpressure: mem high watermark 80% must sit below recovery 30%",
    );
  });

  it("gives non-owners a read-only card with no save affordance", async () => {
    mount(false);

    expect(((await pair(0))[0] as HTMLInputElement).disabled).toBe(true);
    expect(
      screen.queryByText(
        "Showing the code defaults — nothing saved for this workspace yet.",
      ),
    ).toBeNull();
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
    expect(m.saveMutate).not.toHaveBeenCalled();
  });
});

function draftFrom(over: Partial<Record<string, string>> = {}) {
  return {
    mem_high_pct: "15",
    mem_recovery_pct: "25",
    swap_high_pct: "80",
    swap_recovery_pct: "60",
    psi_high_pct: "50",
    psi_recovery_pct: "20",
    sample_interval_seconds: "5",
    window_size: "6",
    ...over,
  };
}

describe("validateBackpressureDraft", () => {
  it("accepts the defaults and every legal combination", () => {
    expect(validateBackpressureDraft(draftFrom())).toEqual({});
    expect(
      validateBackpressureDraft(
        draftFrom({ mem_high_pct: "25", mem_recovery_pct: "25.5" }),
      ),
    ).toEqual({});
    expect(
      validateBackpressureDraft(
        draftFrom({ sample_interval_seconds: "1", window_size: "3600" }),
      ),
    ).toEqual({});
  });

  it("rejects a memory recovery at or below its pause threshold", () => {
    const equal = validateBackpressureDraft(
      draftFrom({ mem_high_pct: "25", mem_recovery_pct: "25" }),
    );
    expect(equal).toEqual({ mem_recovery_pct: "bp_err_mem_order" });

    const below = validateBackpressureDraft(
      draftFrom({ mem_recovery_pct: "10" }),
    );
    expect(below).toEqual({ mem_recovery_pct: "bp_err_mem_order" });
  });

  it("charges range violations to the offending field", () => {
    // Recovery left empty so the out-of-range pause threshold is the only
    // violation on the row (no legal recovery exists above 100).
    expect(
      validateBackpressureDraft(
        draftFrom({ mem_high_pct: "101", mem_recovery_pct: "" }),
      ),
    ).toEqual({ mem_high_pct: "bp_err_range" });
    expect(
      validateBackpressureDraft(draftFrom({ mem_recovery_pct: "101" })),
    ).toEqual({ mem_recovery_pct: "bp_err_range" });
    expect(validateBackpressureDraft(draftFrom({ swap_high_pct: "150" }))).toEqual({
      swap_high_pct: "bp_err_range",
    });
    expect(validateBackpressureDraft(draftFrom({ psi_high_pct: "-2" }))).toEqual(
      {},
    );
  });

  it("rejects a swap or PSI recovery at or above an enabled pause threshold", () => {
    const equal = validateBackpressureDraft(
      draftFrom({ swap_recovery_pct: "80" }),
    );
    expect(equal).toEqual({ swap_recovery_pct: "bp_err_down_order" });

    const above = validateBackpressureDraft(draftFrom({ psi_recovery_pct: "90" }));
    expect(above).toEqual({ psi_recovery_pct: "bp_err_down_order" });

    const negative = validateBackpressureDraft(
      draftFrom({ swap_recovery_pct: "-5" }),
    );
    expect(negative).toEqual({ swap_recovery_pct: "bp_err_down_order" });
  });

  it("reads a 0 pause threshold as fatal on memory and off elsewhere", () => {
    expect(validateBackpressureDraft(draftFrom({ mem_high_pct: "0" }))).toEqual({
      mem_high_pct: "bp_err_mem_pause_positive",
    });

    // 0 disables the swap/PSI rows entirely: the recovery value is unconstrained.
    expect(
      validateBackpressureDraft(
        draftFrom({ swap_high_pct: "0", swap_recovery_pct: "99" }),
      ),
    ).toEqual({});
    expect(
      validateBackpressureDraft(
        draftFrom({ psi_high_pct: "0", psi_recovery_pct: "100" }),
      ),
    ).toEqual({});
  });

  it("requires whole sampling values inside 1–3600", () => {
    for (const value of ["0", "-4", "3601", "2.5"]) {
      expect(
        validateBackpressureDraft(draftFrom({ sample_interval_seconds: value })),
      ).toEqual({ sample_interval_seconds: "bp_err_range_1_3600" });
      expect(
        validateBackpressureDraft(draftFrom({ window_size: value })),
      ).toEqual({ window_size: "bp_err_range_1_3600" });
    }
  });

  it("leaves empty or unparsable fields unflagged for parseDraft to catch", () => {
    expect(validateBackpressureDraft(draftFrom({ mem_recovery_pct: "" }))).toEqual(
      {},
    );
    expect(
      validateBackpressureDraft(
        draftFrom(Object.fromEntries(Object.keys(draftFrom()).map((k) => [k, ""]))),
      ),
    ).toEqual({});
  });
});
