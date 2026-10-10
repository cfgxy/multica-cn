// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { WorkspaceBackpressureSettings } from "@multica/core/types";
import enSettings from "../../locales/en/settings.json";
import { WorkspaceBackpressureCard } from "./workspace-backpressure-card";

/**
 * The host-backpressure card (RUYI-618). What only a mount can show: saves
 * are explicit (no auto-save on nine coupled hysteresis fields), a rejected
 * card puts the server's shared validation sentence next to the fields
 * instead of a generic toast, and non-owners get a read-only card with no
 * save affordance at all.
 */

const TEST_RESOURCES = { en: { settings: enSettings } };

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

/** Three pairs share the two aria labels; document order is mem/swap/psi. */
async function pair(pairIndex: number): Promise<[HTMLInputElement, HTMLInputElement]> {
  // findAllBy waits for the settings query to resolve and the form to mount.
  const highs = await screen.findAllByLabelText("High watermark");
  const recoveries = await screen.findAllByLabelText("Recovery watermark");
  return [highs[pairIndex] as HTMLInputElement, recoveries[pairIndex] as HTMLInputElement];
}

beforeEach(() => {
  m.settings = settingsFixture();
  m.saveMutate.mockReset();
  m.saveMutate.mockImplementation((vars) =>
    Promise.resolve({ ...vars, custom: true }),
  );
});

describe("WorkspaceBackpressureCard", () => {
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

    await userEvent.click(screen.getByRole("button", { name: "Save" }));
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

  it("surfaces the shared validation sentence when the server refuses the card", async () => {
    m.saveMutate.mockRejectedValueOnce(
      new Error(
        "backpressure: mem high watermark 80% must sit below recovery 30%",
      ),
    );
    mount();

    await userEvent.click(
      await screen.findByRole("button", { name: "Save" }),
    );
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Invalid settings:");
    expect(alert.textContent).toContain(
      "backpressure: mem high watermark 80% must sit below recovery 30%",
    );
  });

  it("disables saving while a numeric field is empty or unparsable", async () => {
    mount();

    const memHigh = (await pair(0))[0];
    await userEvent.clear(memHigh);
    expect(
      (screen.getByRole("button", { name: "Save" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);

    // Typing a value re-enables the save.
    await userEvent.type(memHigh, "18");
    expect(
      (screen.getByRole("button", { name: "Save" }) as HTMLButtonElement)
        .disabled,
    ).toBe(false);
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
