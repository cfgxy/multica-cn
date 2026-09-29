import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";

const mocks = vi.hoisted(() => ({
  switchToServer: vi.fn(() => true),
  applyServerSwitch: vi.fn(),
  probeServer: vi.fn(async () => false),
  openManage: vi.fn(),
}));

const servers = [
  { id: "default", name: "Built-in", apiUrl: "https://built-in.example.test", builtIn: true },
  { id: "srv_b", name: "Second", apiUrl: "https://second.example.test", builtIn: false },
];

vi.mock("@multica/views/i18n", () => ({
  useT: () => ({ t: (selector: (resource: Record<string, unknown>) => string, values?: Record<string, string | number>) => {
    const resource = { server: { startup: {
      title: "Choose a server", countdown: "{{n}}s until {{name}}", connect_now: "Connect now",
      cancel_auto: "Cancel auto-connect", next: "Connecting next", checking: "Checking",
      reachable: "Reachable", unreachable: "Unreachable", recheck: "Recheck",
    }, built_in: "Built-in", manage_title: "Manage servers", cancel: "Cancel", switch_failed_message: "Failed" } };
    const key = selector(resource);
    return Object.entries(values ?? {}).reduce((value, [name, replacement]) =>
      value.replace(`{{${name}}}`, String(replacement)), key);
  } }),
}));
vi.mock("@multica/views/platform", () => ({ DragStrip: () => null }));
vi.mock("@multica/ui/components/common/multica-icon", () => ({ MulticaIcon: () => null }));
vi.mock("../components/server-settings-dialog", () => ({ ServerSettingsDialog: () => null }));
vi.mock("../platform/desktop-servers", () => ({
  useServerStore: (select: (state: { servers: typeof servers; activeServerId: string }) => unknown) =>
    select({ servers, activeServerId: "default" }),
  switchToServer: mocks.switchToServer,
}));
vi.mock("../platform/probe-server", () => ({ probeServer: mocks.probeServer }));
vi.mock("../stores/server-switcher-store", () => ({
  useServerSwitcherStore: (select: (state: { openManage: typeof mocks.openManage; manageOpen: boolean; closeManage: () => void }) => unknown) =>
    select({ openManage: mocks.openManage, manageOpen: false, closeManage: vi.fn() }),
}));

import { StartupServerSelect } from "./startup-server-select";

describe("StartupServerSelect", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    Object.defineProperty(window, "desktopAPI", {
      value: { applyServerSwitch: mocks.applyServerSwitch }, configurable: true,
    });
  });
  afterEach(() => vi.useRealTimers());

  it("auto-connects the persisted instance after five seconds without interaction", async () => {
    const onContinue = vi.fn();
    render(<StartupServerSelect previousId="default" onContinue={onContinue} />);
    for (let second = 0; second < 5; second += 1) {
      await act(async () => { await vi.advanceTimersByTimeAsync(1_000); });
    }
    expect(onContinue).toHaveBeenCalledTimes(1);
    expect(mocks.switchToServer).not.toHaveBeenCalled();
  });

  it("cancels the timer permanently and still allows an unreachable server", async () => {
    render(<StartupServerSelect previousId="default" onContinue={vi.fn()} />);
    fireEvent.click(screen.getByText("Cancel auto-connect"));
    await act(async () => { await vi.advanceTimersByTimeAsync(6_000); });
    expect(mocks.switchToServer).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: /Second/ }));
    expect(mocks.switchToServer).toHaveBeenCalledWith("srv_b");
    expect(mocks.applyServerSwitch).toHaveBeenCalledTimes(1);
  });

  it("does not auto-connect when the previous instance is invalid", async () => {
    render(<StartupServerSelect previousId={null} onContinue={vi.fn()} />);
    await act(async () => { await vi.advanceTimersByTimeAsync(6_000); });
    expect(mocks.switchToServer).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: /Built-in/ }));
    expect(mocks.switchToServer).toHaveBeenCalledWith("default");
  });
});
