import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

vi.mock("@multica/views/platform", () => ({ DragStrip: () => null }));
vi.mock("@multica/ui/components/common/multica-icon", () => ({ MulticaIcon: () => null }));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (select: (state: { retryAuthentication: () => void }) => unknown) =>
    select({ retryAuthentication: vi.fn() }),
}));
vi.mock("@multica/views/i18n", () => ({
  useT: () => ({ t: (select: (value: unknown) => string) => select({ desktop: { recovery: {
    title: "Reconnect", description: "Saved session", retry: "Retry", retrying: "Retrying",
    switch_server: "Switch server",
  } } }) }),
}));
vi.mock("./startup-server-select", () => ({
  StartupServerSelect: ({ onClose }: { onClose: () => void }) => <button onClick={onClose}>Server list</button>,
}));

import { DesktopAuthRecoveryPage } from "./auth-recovery";

describe("DesktopAuthRecoveryPage", () => {
  it("opens the same explicit selection from auth and workspace recovery", () => {
    const { rerender } = render(<DesktopAuthRecoveryPage />);
    fireEvent.click(screen.getByText("Switch server"));
    expect(screen.getByText("Server list")).toBeInTheDocument();
    fireEvent.click(screen.getByText("Server list"));
    rerender(<DesktopAuthRecoveryPage onRetry={vi.fn()} />);
    fireEvent.click(screen.getByText("Switch server"));
    expect(screen.getByText("Server list")).toBeInTheDocument();
  });

  it("disables switching while a workspace retry is active", () => {
    render(<DesktopAuthRecoveryPage onRetry={vi.fn()} isRetrying />);
    expect(screen.getByText("Switch server")).toBeDisabled();
  });
});
