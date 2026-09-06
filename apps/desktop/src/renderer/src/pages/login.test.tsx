import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("@multica/views/auth", () => ({
  LoginPage: ({ extra }: { extra?: React.ReactNode }) => (
    <div data-testid="login-page">{extra}</div>
  ),
}));

vi.mock("@multica/views/platform", () => ({
  DragStrip: () => <div data-testid="drag-strip" />,
}));

vi.mock("@multica/ui/components/common/multica-icon", () => ({
  MulticaIcon: () => <div />,
}));

vi.mock("../components/login-server-switcher", () => ({
  LoginServerSwitcher: () => <div data-testid="login-server-switcher" />,
}));

Object.defineProperty(window, "desktopAPI", {
  value: {
    runtimeConfig: {
      ok: true,
      config: { appUrl: "https://app.multica.example" },
    },
    openExternal: vi.fn(),
  },
  configurable: true,
});

import { DesktopLoginPage } from "./login";

describe("DesktopLoginPage", () => {
  it("renders the server switcher on the signed-out screen", () => {
    render(<DesktopLoginPage />);

    expect(screen.getByTestId("drag-strip")).toBeInTheDocument();
    expect(screen.getByTestId("login-page")).toBeInTheDocument();
    expect(screen.getByTestId("login-server-switcher")).toBeInTheDocument();
  });
});
