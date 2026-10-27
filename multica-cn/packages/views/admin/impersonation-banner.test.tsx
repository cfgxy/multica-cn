import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enAdmin from "../locales/en/admin.json";

const TEST_RESOURCES = { en: { admin: enAdmin } };

// ---------------------------------------------------------------------------
// Hoisted mocks
// ---------------------------------------------------------------------------

const mockApiStopImpersonation = vi.hoisted(() => vi.fn());

const mockAuthState = vi.hoisted(() => ({
  user: null as null | Record<string, unknown>,
  applySession: vi.fn(),
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector?: (s: unknown) => unknown) => {
      return selector ? selector(mockAuthState) : mockAuthState;
    },
    { getState: () => mockAuthState },
  ),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    stopImpersonation: mockApiStopImpersonation,
  },
}));

import { ImpersonationBanner } from "./impersonation-banner";

function renderWithProviders(ui: React.ReactElement) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  function Wrapper({ children }: { children: ReactNode }) {
    return (
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <QueryClientProvider client={qc}>{children}</QueryClientProvider>
      </I18nProvider>
    );
  }
  return render(ui, { wrapper: Wrapper });
}

describe("ImpersonationBanner", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockAuthState.user = null;
  });

  it("renders nothing for a normal session", () => {
    mockAuthState.user = {
      id: "admin-1",
      name: "Admin",
      email: "a@test.local",
      is_super_admin: true,
      impersonator_id: null,
    };
    const { container } = renderWithProviders(<ImpersonationBanner />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing for a user missing the field (older backend)", () => {
    mockAuthState.user = { id: "u1", name: "U", email: "u@test.local" };
    const { container } = renderWithProviders(<ImpersonationBanner />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders the banner with the target identity while impersonating", () => {
    mockAuthState.user = {
      id: "target-1",
      name: "Target User",
      email: "t@test.local",
      is_super_admin: false,
      impersonator_id: "admin-1",
    };
    renderWithProviders(<ImpersonationBanner />);
    expect(screen.getByRole("status")).toBeInTheDocument();
    expect(screen.getByText("Acting as Target User")).toBeInTheDocument();
  });

  it("exits impersonation and applies the returned session", async () => {
    const user = userEvent.setup();
    const restoredUser = {
      id: "admin-1",
      name: "Admin",
      email: "a@test.local",
      is_super_admin: true,
      impersonator_id: null,
    };
    mockApiStopImpersonation.mockResolvedValue({
      token: "fresh-token",
      user: restoredUser,
    });
    mockAuthState.user = {
      id: "target-1",
      name: "Target User",
      email: "t@test.local",
      is_super_admin: false,
      impersonator_id: "admin-1",
    };

    renderWithProviders(<ImpersonationBanner />);
    await user.click(screen.getByRole("button"));

    await waitFor(() => {
      expect(mockApiStopImpersonation).toHaveBeenCalledTimes(1);
      expect(mockAuthState.applySession).toHaveBeenCalledWith(
        "fresh-token",
        restoredUser,
      );
    });
  });

  it("keeps the banner and shows an error when the exit fails", async () => {
    const user = userEvent.setup();
    mockApiStopImpersonation.mockRejectedValue(new Error("403"));
    mockAuthState.user = {
      id: "target-1",
      name: "Target User",
      email: "t@test.local",
      is_super_admin: false,
      impersonator_id: "admin-1",
    };

    renderWithProviders(<ImpersonationBanner />);
    await user.click(screen.getByRole("button"));

    await waitFor(() => {
      expect(screen.getByText("Could not exit impersonation. Your session may have expired — sign in again.")).toBeInTheDocument();
    });
    // Still impersonating: the banner itself must not disappear.
    expect(screen.getByText("Acting as Target User")).toBeInTheDocument();
    expect(mockAuthState.applySession).not.toHaveBeenCalled();
  });
});
