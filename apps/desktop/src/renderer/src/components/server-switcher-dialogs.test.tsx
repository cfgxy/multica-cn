import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";

const mocks = vi.hoisted(() => ({
  switchToServer: vi.fn(),
  reload: vi.fn(),
  toastError: vi.fn(),
}));

const authState = { user: null as { id: string } | null };

// Same selector-style i18n stub as the other desktop suites.
const translations = {
  server: {
    switch_title: "Switch server?",
    switch_message: "Switch to {{name}}?",
    switch_confirm: "Switch",
    cancel: "Cancel",
    switch_failed_message: "Could not switch servers.",
  },
};

vi.mock("@multica/views/i18n", () => ({
  useT: () => ({
    t: (
      selector: (resources: typeof translations) => string,
      values?: Record<string, string>,
    ) => {
      const template = selector(translations);
      return Object.entries(values ?? {}).reduce(
        (result, [key, value]) => result.replace(`{{${key}}}`, value),
        template,
      );
    },
  }),
}));

vi.mock("sonner", () => ({ toast: { error: mocks.toastError, success: vi.fn() } }));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector: (s: typeof authState) => unknown) => selector(authState),
    { getState: () => authState },
  ),
}));

vi.mock("../platform/desktop-servers", () => ({
  switchToServer: mocks.switchToServer,
  // Unused by the dialogs host itself; present so the re-export surface exists.
  useServerStore: Object.assign(() => undefined, { getState: () => ({}) }),
  resetActiveServerSessionStorage: vi.fn(),
}));

import { useServerSwitcherStore } from "../stores/server-switcher-store";
import { ServerSwitcherDialogs } from "./server-switcher-dialogs";

Object.defineProperty(window, "location", {
  value: { reload: mocks.reload },
  writable: true,
});

const ENTRY = { id: "srv_b", name: "Home lab", apiUrl: "https://b.example.com", webUrl: null, builtIn: false };

function resetState() {
  useServerSwitcherStore.setState({
    manageOpen: false,
    pendingSwitch: null,
    editingServerId: null,
  });
}

describe("ServerSwitcherDialogs — switch confirmation", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    authState.user = null;
    resetState();
  });

  it("signed-out users switch immediately without a dialog", () => {
    mocks.switchToServer.mockReturnValue(true);
    render(<ServerSwitcherDialogs />);

    act(() => {
      useServerSwitcherStore.getState().requestSwitch(ENTRY);
    });

    expect(mocks.switchToServer).toHaveBeenCalledWith("srv_b");
    expect(mocks.reload).toHaveBeenCalledTimes(1);
  });

  it("signed-in users confirm first, then switch and reload", () => {
    authState.user = { id: "user-1" };
    mocks.switchToServer.mockReturnValue(true);
    render(<ServerSwitcherDialogs />);

    act(() => {
      useServerSwitcherStore.getState().requestSwitch(ENTRY);
    });
    expect(screen.getByText("Switch to Home lab?")).toBeInTheDocument();

    fireEvent.click(screen.getByText("Switch", { selector: "[data-slot=alert-dialog-action]" }));
    expect(mocks.switchToServer).toHaveBeenCalledWith("srv_b");
    expect(mocks.reload).toHaveBeenCalledTimes(1);
  });

  it("shows a toast instead of reloading when the switch is rejected", () => {
    authState.user = { id: "user-1" };
    mocks.switchToServer.mockReturnValue(false);
    render(<ServerSwitcherDialogs />);

    act(() => {
      useServerSwitcherStore.getState().requestSwitch(ENTRY);
    });
    fireEvent.click(screen.getByText("Switch", { selector: "[data-slot=alert-dialog-action]" }));

    expect(mocks.switchToServer).toHaveBeenCalledWith("srv_b");
    expect(mocks.reload).not.toHaveBeenCalled();
    expect(mocks.toastError).toHaveBeenCalledWith("Could not switch servers.");
  });

  it("cancel clears the pending switch without side effects", () => {
    authState.user = { id: "user-1" };
    render(<ServerSwitcherDialogs />);

    act(() => {
      useServerSwitcherStore.getState().requestSwitch(ENTRY);
    });
    fireEvent.click(screen.getByText("Cancel"));

    expect(mocks.switchToServer).not.toHaveBeenCalled();
    expect(useServerSwitcherStore.getState().pendingSwitch).toBeNull();
  });
});
