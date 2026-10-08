import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "@multica/views/locales/en/common.json";
import enAuth from "@multica/views/locales/en/auth.json";
import enSettings from "@multica/views/locales/en/settings.json";
import type { ReactNode } from "react";

const TEST_RESOURCES = {
  en: { common: enCommon, auth: enAuth, settings: enSettings },
};

function createWrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    </I18nProvider>
  );
}

const {
  mockIssueCliToken,
  mockListWorkspaces,
  mockListMyInvitations,
  mockPush,
  mockReplace,
  searchParamsState,
  authStateRef,
} = vi.hoisted(() => ({
  mockIssueCliToken: vi.fn(),
  mockListWorkspaces: vi.fn(),
  mockListMyInvitations: vi.fn(),
  mockPush: vi.fn(),
  mockReplace: vi.fn(),
  searchParamsState: { params: new URLSearchParams() },
  authStateRef: {
    state: {
      sendCode: vi.fn(),
      verifyCode: vi.fn(),
      user: null as null | { id: string; email: string; onboarded_at?: string | null },
      isLoading: false,
    },
  },
}));

// Mock next/navigation — router spies are hoisted so tests can assert
// which navigation (if any) the page issued.
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: mockPush, replace: mockReplace }),
  usePathname: () => "/login",
  useSearchParams: () => searchParamsState.params,
}));

// Mock auth store — shared LoginPage uses getState().sendCode/verifyCode,
// web wrapper uses useAuthStore((s) => s.user/isLoading). Keep the real
// sanitizeNextUrl so the redirect-sanitization rules are exercised rather
// than silently drifting behind a mock reimplementation.
vi.mock("@multica/core/auth", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/auth")>(
      "@multica/core/auth",
    );
  const useAuthStore = Object.assign(
    (selector: (s: typeof authStateRef.state) => unknown) =>
      selector(authStateRef.state),
    { getState: () => authStateRef.state },
  );
  return { ...actual, useAuthStore };
});

// Mock auth-cookie
vi.mock("@/features/auth/auth-cookie", () => ({
  setLoggedInCookie: vi.fn(),
}));

// Mock api
vi.mock("@multica/core/api", () => ({
  api: {
    listWorkspaces: mockListWorkspaces,
    listMyInvitations: mockListMyInvitations,
    verifyCode: vi.fn(),
    setToken: vi.fn(),
    getMe: vi.fn(),
    issueCliToken: mockIssueCliToken,
  },
}));

import LoginPage from "./page";

describe("LoginPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    searchParamsState.params = new URLSearchParams();
    authStateRef.state.user = null;
    authStateRef.state.isLoading = false;
    mockListWorkspaces.mockResolvedValue([]);
    mockListMyInvitations.mockResolvedValue([]);
  });

  // Shared LoginPage behavior is canonical in
  // packages/views/auth/login-page.test.tsx. This wrapper suite only owns web
  // platform handoff and redirect behavior.

  // Regression: MUL-1080 — if the user is already authenticated on the web
  // and the Desktop app redirects them to /login?platform=desktop, the web
  // must exchange the cookie session for a bearer token and hand it off via
  // the multica:// deep link, not silently redirect to the workspace page.
  it("mints a token and deep-links to Desktop when already logged in with platform=desktop", async () => {
    searchParamsState.params = new URLSearchParams({ platform: "desktop" });
    authStateRef.state.user = { id: "u1", email: "test@multica.ai" };
    mockIssueCliToken.mockImplementation(() =>
      Promise.resolve({ token: "handoff-jwt" }),
    );

    const hrefSetter = vi.fn();
    const originalLocation = window.location;
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { ...originalLocation, set href(value: string) { hrefSetter(value); } },
    });

    try {
      render(<LoginPage />, { wrapper: createWrapper() });

      await waitFor(() => {
        expect(mockIssueCliToken).toHaveBeenCalledTimes(1);
      });
      await waitFor(() => {
        expect(hrefSetter).toHaveBeenCalledWith(
          "multica://auth/callback?token=handoff-jwt",
        );
      });
      expect(
        await screen.findByRole("button", { name: "Open Multica Desktop" }),
      ).toBeInTheDocument();
    } finally {
      Object.defineProperty(window, "location", {
        configurable: true,
        value: originalLocation,
      });
    }
  });

  // Regression: #5009 — the "already authenticated on arrival" effect used to
  // fire for fresh form logins too. verifyCode writes `user` while handleVerify
  // is still fetching the workspace list, so the effect read an empty cache and
  // raced handleSuccess with replace("/workspaces/new"); depending on the
  // interleaving the user could end up stuck on the create-workspace page
  // despite having workspaces.
  describe("post-login redirect ownership (#5009)", () => {
    const onboardedUser = {
      id: "u1",
      email: "test@multica.ai",
      onboarded_at: "2026-01-01T00:00:00Z",
    };

    it("does not redirect from the arrival effect when the user logs in via the form", async () => {
      // Auth settles as logged-out first — the page latches "any user from
      // now on came from the form".
      const wrapper = createWrapper();
      const { rerender } = render(<LoginPage />, { wrapper });
      // verifyCode set the user; the workspace list fetch is still in flight
      // (cache cold). The arrival effect must stay silent — handleSuccess
      // owns this navigation.
      authStateRef.state.user = onboardedUser;
      rerender(<LoginPage />);

      await act(async () => {});
      expect(mockReplace).not.toHaveBeenCalled();
      expect(mockPush).not.toHaveBeenCalled();
      expect(mockListWorkspaces).not.toHaveBeenCalled();
    });

    it("fetches the workspace list before redirecting a visitor who arrived authenticated", async () => {
      // Cold Query cache on a fresh page load: reading it would say "no
      // workspaces" and misroute to /workspaces/new. The effect must fetch.
      authStateRef.state.user = onboardedUser;
      mockListWorkspaces.mockResolvedValue([{ id: "ws-1", slug: "acme" }]);

      render(<LoginPage />, { wrapper: createWrapper() });

      await waitFor(() => {
        expect(mockReplace).toHaveBeenCalledWith("/acme/issues");
      });
      expect(mockListWorkspaces).toHaveBeenCalledTimes(1);
    });

    it("still honors ?next= for a visitor who arrived authenticated", async () => {
      searchParamsState.params = new URLSearchParams({
        next: "/invite/abc",
      });
      authStateRef.state.user = onboardedUser;

      render(<LoginPage />, { wrapper: createWrapper() });

      await waitFor(() => {
        expect(mockReplace).toHaveBeenCalledWith("/invite/abc");
      });
      expect(mockListWorkspaces).not.toHaveBeenCalled();
    });
  });

  // RUYI-526: a `next` pointing at the backend authorize endpoint
  // (/auth/oauth/authorize?...) must be followed by the browser itself —
  // the endpoint answers with the OAuth redirect chain (302 to the client
  // callback or the consent screen). A client-side router transition
  // fetches it as an RSC payload, the fetch consumes the 302, and the
  // chain never happens: the user was left stranded on the verification
  // page with no error. Backend auth paths therefore need a full-page
  // navigation; in-app paths keep the soft one.
  describe("post-login resume of backend auth targets (RUYI-526)", () => {
    // Realistic ChatGPT-connector authorize shape (synthetic PKCE/state
    // values) — path + query, same-origin, sanitizeNextUrl-clean.
    const authorizeNext =
      "/auth/oauth/authorize?response_type=code&client_id=chatgpt&redirect_uri=https%3A%2F%2Fchatgpt.example%2Fconnector%2Foauth%2Ftest123&scope=mcp&code_challenge=WpErBVM92yLTRPFWrJz9LYfTmo-_ZdmSwKVsklZr6oQ&code_challenge_method=S256&resource=https%3A%2F%2Fapp.example.com%2Fapi%2Fmcp&state=chatgpt_scheme__oauth_s_synthetic";

    function mockFullPageNavigation() {
      const hrefSetter = vi.fn();
      const originalLocation = window.location;
      Object.defineProperty(window, "location", {
        configurable: true,
        writable: true,
        value: {
          ...originalLocation,
          set href(value: string) {
            hrefSetter(value);
          },
        },
      });
      return {
        hrefSetter,
        restore: () => {
          Object.defineProperty(window, "location", {
            configurable: true,
            value: originalLocation,
          });
        },
      };
    }

    const onboardedUser = {
      id: "u1",
      email: "test@multica.ai",
      onboarded_at: "2026-01-01T00:00:00Z",
    };

    it("continues the authorize chain with a full-page navigation after the verification code is accepted", async () => {
      searchParamsState.params = new URLSearchParams({ next: authorizeNext });
      authStateRef.state.sendCode.mockResolvedValue(undefined);
      authStateRef.state.verifyCode.mockResolvedValue(undefined);
      mockListWorkspaces.mockResolvedValue([{ id: "ws-1", slug: "acme" }]);

      const { hrefSetter, restore } = mockFullPageNavigation();
      try {
        render(<LoginPage />, { wrapper: createWrapper() });

        const user = userEvent.setup();
        await user.type(screen.getByLabelText(/email/i), "test@multica.ai");
        await user.click(screen.getByRole("button", { name: /continue/i }));
        await waitFor(() => {
          expect(
            screen.getByRole("textbox", { hidden: true }),
          ).toBeInTheDocument();
        });
        await user.type(screen.getByRole("textbox", { hidden: true }), "123456");

        await waitFor(() => {
          expect(hrefSetter).toHaveBeenCalledWith(authorizeNext);
        });
        // The soft router must stay out of the way — its RSC fetch would
        // swallow the authorize 302.
        expect(mockPush).not.toHaveBeenCalled();
        expect(mockReplace).not.toHaveBeenCalled();
      } finally {
        restore();
      }
    });

    it("full-page-navigates a visitor who arrived already authenticated at /login?next=<authorize>", async () => {
      searchParamsState.params = new URLSearchParams({ next: authorizeNext });
      authStateRef.state.user = onboardedUser;

      const { hrefSetter, restore } = mockFullPageNavigation();
      try {
        render(<LoginPage />, { wrapper: createWrapper() });

        await waitFor(() => {
          expect(hrefSetter).toHaveBeenCalledWith(authorizeNext);
        });
        expect(mockReplace).not.toHaveBeenCalled();
        expect(mockPush).not.toHaveBeenCalled();
      } finally {
        restore();
      }
    });
  });
});
