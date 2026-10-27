import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enAdmin from "../locales/en/admin.json";

const TEST_RESOURCES = { en: { admin: enAdmin } };

// ---------------------------------------------------------------------------
// Hoisted mocks — API client + auth store (callable-store shape per the
// repo testing rules). The react-query key invalidation is left real.
// ---------------------------------------------------------------------------

const mockApiListUsers = vi.hoisted(() => vi.fn());
const mockSetUserDisabled = vi.hoisted(() => vi.fn());
const mockSetSuperAdmin = vi.hoisted(() => vi.fn());
const mockImpersonate = vi.hoisted(() => vi.fn());
const mockApplySession = vi.hoisted(() => vi.fn());

const mockAuthState = vi.hoisted(() => ({
  user: {
    id: "me-1",
    name: "Me Admin",
    email: "me@test.local",
    is_super_admin: true,
    impersonator_id: null,
  } as Record<string, unknown> | null,
  applySession: mockApplySession,
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector?: (s: unknown) => unknown) =>
      selector ? selector(mockAuthState) : mockAuthState,
    { getState: () => mockAuthState },
  ),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    adminListUsers: mockApiListUsers,
    adminSetUserDisabled: mockSetUserDisabled,
    adminSetUserSuperAdmin: mockSetSuperAdmin,
    adminImpersonate: mockImpersonate,
    stopImpersonation: vi.fn(),
  },
}));

const USERS = [
  {
    id: "u-1",
    name: "Alice",
    email: "alice@test.local",
    avatar_url: null,
    is_super_admin: false,
    disabled_at: null,
    workspace_count: 2,
    created_at: "2026-01-01T00:00:00Z",
  },
  {
    id: "me-1",
    name: "Me Admin",
    email: "me@test.local",
    avatar_url: null,
    is_super_admin: true,
    disabled_at: null,
    workspace_count: 1,
    created_at: "2026-01-02T00:00:00Z",
  },
  {
    id: "u-2",
    name: "Bob Disabled",
    email: "bob@test.local",
    avatar_url: null,
    is_super_admin: false,
    disabled_at: "2026-06-01T00:00:00Z",
    workspace_count: 0,
    created_at: "2026-01-03T00:00:00Z",
  },
];

import { AdminUsersPage } from "./admin-users-page";

function renderPage() {
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
  return render(<AdminUsersPage />, { wrapper: Wrapper });
}

describe("AdminUsersPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApiListUsers.mockResolvedValue({ users: USERS, total: USERS.length });
    mockSetUserDisabled.mockImplementation((_id: string, disabled: boolean) =>
      Promise.resolve({ ...USERS[0], disabled_at: disabled ? "now" : null }));
    mockSetSuperAdmin.mockResolvedValue(USERS[0]);
  });

  it("renders the directory rows with status and role", async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("alice@test.local")).toBeInTheDocument();
    });
    expect(screen.getByText("Bob Disabled")).toBeInTheDocument();
    // Active/Disabled badges + one Super admin badge.
    expect(screen.getAllByText("Active")).toHaveLength(2);
    expect(screen.getByText("Disabled")).toBeInTheDocument();
    expect(screen.getByText("Super admin")).toBeInTheDocument();
  });

  it("renders an empty state when nothing matches", async () => {
    const user = userEvent.setup();
    mockApiListUsers.mockResolvedValue({ users: [], total: 0 });
    renderPage();
    await user.type(screen.getByRole("textbox"), "zzz");
    await waitFor(() => {
      expect(screen.getByText("Nothing matches this search.")).toBeInTheDocument();
    });
    expect(mockApiListUsers).toHaveBeenCalledWith(expect.objectContaining({ query: "zzz" }));
  });

  it("disables the self-disable and self-revoke buttons for the acting admin", async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("Me Admin")).toBeInTheDocument();
    });
    const row = screen.getByText("Me Admin").closest("tr") as HTMLElement;
    expect(row).not.toBeNull();
    const revokeButton = row.querySelector("button:nth-of-type(1)");
    const disableButton = row.querySelectorAll("button")[1];
    // Revoke (first action for a super admin) is disabled for self...
    expect(revokeButton).toBeDisabled();
    // ...and Disable (second action) too.
    expect(disableButton).toBeDisabled();
    // Impersonate (third) is disabled for self as well.
    expect(row.querySelectorAll("button")[2]).toBeDisabled();
  });

  it("sends the disable request after confirmation", async () => {
    const user = userEvent.setup();
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("Alice")).toBeInTheDocument();
    });
    const row = screen.getByText("Alice").closest("tr") as HTMLElement;
    const disableButton = [...row.querySelectorAll("button")].find((b) =>
      b.textContent?.includes("Disable"),
    );
    expect(disableButton).not.toBeUndefined();
    await user.click(disableButton as HTMLElement);

    // Confirmation dialog with optional reason.
    expect(screen.getByText("Disable Alice?")).toBeInTheDocument();
    await user.type(screen.getByPlaceholderText("Why are you doing this?"), "abuse");
    await user.click(screen.getByRole("button", { name: "Confirm" }));

    await waitFor(() => {
      expect(mockSetUserDisabled).toHaveBeenCalledWith("u-1", true, "abuse");
    });
  });

  it("impersonation is blocked for disabled and super-admin rows", async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("Bob Disabled")).toBeInTheDocument();
    });
    const bobRow = screen.getByText("Bob Disabled").closest("tr") as HTMLElement;
    const bobButtons = bobRow.querySelectorAll("button");
    // Grant / Enable+Disable / Impersonate — impersonate is the last one.
    expect(bobButtons[bobButtons.length - 1]).toBeDisabled();

    const meRow = screen.getByText("Me Admin").closest("tr") as HTMLElement;
    const meButtons = meRow.querySelectorAll("button");
    expect(meButtons[meButtons.length - 1]).toBeDisabled();
  });
});
