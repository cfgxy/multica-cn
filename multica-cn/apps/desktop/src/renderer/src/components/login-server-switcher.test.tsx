import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("@multica/views/i18n", () => ({
  useT: () => ({
    t: (selector: (resources: { mobile: { server_settings: string } }) => string) =>
      selector({ mobile: { server_settings: "Server settings" } }),
  }),
}));

vi.mock("@multica/ui/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children }: { children: React.ReactNode }) => (
    <button type="button">{children}</button>
  ),
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
}));

vi.mock("./server-switcher-group", () => ({
  ServerSwitcherGroup: () => <div data-testid="server-switcher-group" />,
}));

vi.mock("./server-switcher-dialogs", () => ({
  ServerSwitcherDialogs: () => <div data-testid="server-switcher-dialogs" />,
}));

import { LoginServerSwitcher } from "./login-server-switcher";

describe("LoginServerSwitcher", () => {
  it("keeps server selection and dialog hosts available while signed out", () => {
    render(<LoginServerSwitcher />);

    expect(screen.getByRole("button", { name: "Server settings" })).toBeInTheDocument();
    expect(screen.getByTestId("server-switcher-group")).toBeInTheDocument();
    expect(screen.getByTestId("server-switcher-dialogs")).toBeInTheDocument();
  });
});
