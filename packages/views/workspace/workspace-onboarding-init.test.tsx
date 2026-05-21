import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../locales/en/common.json";
import enOnboarding from "../locales/en/onboarding.json";

// Single decision point covering all 4 branches of WorkspaceOnboardingInit.
// Per branch we mock the user/workspace via hoisted refs so the same mount
// can flip state between tests; downstream API calls are mocked at the
// module boundary.

type FakeUser = {
  id: string;
  onboarded_at: string | null;
  onboarding_runtime_id: string | null;
  onboarding_runtime_skipped: boolean;
};

const userRef = vi.hoisted(() => ({ current: null as FakeUser | null }));
const workspaceRef = vi.hoisted(() => ({
  current: null as { id: string; slug: string } | null,
}));
const mockBootstrap = vi.hoisted(() => vi.fn());
const mockEnsureContent = vi.hoisted(() => vi.fn());
const mockNavigate = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/auth", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/auth")>(
      "@multica/core/auth",
    );
  const useAuthStore = Object.assign(
    (sel?: (s: { user: FakeUser | null }) => unknown) =>
      sel ? sel({ user: userRef.current }) : { user: userRef.current },
    { getState: () => ({ user: userRef.current }) },
  );
  return { ...actual, useAuthStore };
});

vi.mock("@multica/core/paths", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/paths")>(
      "@multica/core/paths",
    );
  return {
    ...actual,
    useCurrentWorkspace: () => workspaceRef.current,
  };
});

vi.mock("@multica/core/onboarding", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/onboarding")>(
      "@multica/core/onboarding",
    );
  return {
    ...actual,
    bootstrapNoRuntimeOnboarding: mockBootstrap,
  };
});

vi.mock("@multica/core/api", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/api")>(
      "@multica/core/api",
    );
  return {
    ...actual,
    api: { ...actual.api, ensureOnboardingContent: mockEnsureContent },
  };
});

vi.mock("../navigation", () => ({
  useNavigation: () => ({ push: mockNavigate, replace: mockNavigate }),
}));

// Import after mocks.
import { WorkspaceOnboardingInit } from "./workspace-onboarding-init";

const TEST_RESOURCES = {
  en: { common: enCommon, onboarding: enOnboarding },
};

function renderInit() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <WorkspaceOnboardingInit />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("WorkspaceOnboardingInit", () => {
  beforeEach(() => {
    userRef.current = null;
    workspaceRef.current = null;
    mockBootstrap.mockReset();
    mockEnsureContent.mockReset();
    mockNavigate.mockReset();
  });

  it("renders nothing when user not yet loaded (auth still resolving)", () => {
    userRef.current = null;
    workspaceRef.current = { id: "w1", slug: "acme" };
    const { container } = renderInit();
    expect(container.firstChild).toBeNull();
    expect(mockBootstrap).not.toHaveBeenCalled();
    expect(mockEnsureContent).not.toHaveBeenCalled();
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  it("renders nothing when workspace not yet resolved", () => {
    userRef.current = {
      id: "u1",
      onboarded_at: null,
      onboarding_runtime_id: null,
      onboarding_runtime_skipped: false,
    };
    workspaceRef.current = null;
    const { container } = renderInit();
    expect(container.firstChild).toBeNull();
  });

  describe("branch 0 — already onboarded", () => {
    it("fires ensureOnboardingContent once per workspace id and renders nothing", async () => {
      mockEnsureContent.mockResolvedValue({ created: false });
      userRef.current = {
        id: "u1",
        onboarded_at: "2026-05-01T00:00:00Z",
        onboarding_runtime_id: null,
        onboarding_runtime_skipped: false,
      };
      workspaceRef.current = { id: "w1", slug: "acme" };
      const { container } = renderInit();

      await waitFor(() => {
        expect(mockEnsureContent).toHaveBeenCalledWith("w1");
      });
      // Branch 0 itself renders no visible UI (the EnsureWorkspaceContent
      // child returns null) — the workspace shell underneath shows.
      expect(container.querySelector("[role='dialog']")).toBeNull();
      expect(mockBootstrap).not.toHaveBeenCalled();
      expect(mockNavigate).not.toHaveBeenCalled();
    });
  });

  describe("branch 1 — explicit Skip", () => {
    beforeEach(() => {
      userRef.current = {
        id: "u1",
        onboarded_at: null,
        onboarding_runtime_id: null,
        onboarding_runtime_skipped: true,
      };
      workspaceRef.current = { id: "w1", slug: "acme" };
    });

    it("shows loading veil while bootstrap is in flight", async () => {
      // Resolve a pending promise the test can await later.
      let resolveBootstrap: (v: { issue_id: string; workspace_id: string }) => void;
      mockBootstrap.mockReturnValue(
        new Promise((res) => {
          resolveBootstrap = res;
        }),
      );

      renderInit();
      expect(
        await screen.findByText(/Setting up your workspace/i),
      ).toBeInTheDocument();

      // Unblock the promise so the test cleans up without leaking handlers.
      resolveBootstrap!({ issue_id: "i1", workspace_id: "w1" });
      await waitFor(() => {
        expect(mockBootstrap).toHaveBeenCalledWith("w1");
      });
    });

    it("navigates to seeded issue on success", async () => {
      mockBootstrap.mockResolvedValue({
        issue_id: "seeded-id",
        workspace_id: "w1",
      });
      renderInit();

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith(
          expect.stringContaining("seeded-id"),
        );
      });
    });

    it("on failure: shows retry UI (NOT a frozen loading veil)", async () => {
      mockBootstrap.mockRejectedValueOnce(new Error("network blip"));
      renderInit();

      // The retry surface replaces the loading veil so the user isn't
      // stuck behind it. The bug fix this test guards against was: on
      // failure the loading veil stayed up indefinitely until reload.
      expect(
        await screen.findByText(/Couldn't finish setup/i),
      ).toBeInTheDocument();
      expect(screen.getByText(/network blip/i)).toBeInTheDocument();
      expect(screen.getByRole("button", { name: /try again/i })).toBeEnabled();
    });

    it("Retry: clicking re-runs bootstrap and succeeds on the second try", async () => {
      // Stateful mock — first call rejects, subsequent calls resolve. Doesn't
      // assume the effect fires exactly once per attempt (React StrictMode in
      // dev / vitest defaults can double-invoke effects), so the assertion
      // tracks "did a successful retry run after a failed run", not call count.
      let rejected = false;
      mockBootstrap.mockImplementation(() => {
        if (!rejected) {
          rejected = true;
          return Promise.reject(new Error("transient"));
        }
        return Promise.resolve({ issue_id: "i2", workspace_id: "w1" });
      });
      renderInit();

      const retryButton = await screen.findByRole("button", { name: /try again/i });
      await userEvent.setup().click(retryButton);

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith(
          expect.stringContaining("i2"),
        );
      });
    });
  });

  describe("branch 2 — runtime picked", () => {
    it("renders the Helper Modal with the chosen runtime id", async () => {
      userRef.current = {
        id: "u1",
        onboarded_at: null,
        onboarding_runtime_id: "chosen-runtime",
        onboarding_runtime_skipped: false,
      };
      workspaceRef.current = { id: "w1", slug: "acme" };
      renderInit();

      // Modal renders its title from the locale file.
      expect(
        await screen.findByText(/Meet Multica Helper/i),
      ).toBeInTheDocument();
      // Branch 2 does NOT run the no-runtime bootstrap.
      expect(mockBootstrap).not.toHaveBeenCalled();
      // Branch 2 does NOT touch ensureOnboardingContent (branch 0 only).
      expect(mockEnsureContent).not.toHaveBeenCalled();
    });
  });

  describe("branch 3 — bailed out of Step 3", () => {
    it("redirects to /onboarding via navigation.push (in effect, not render)", async () => {
      userRef.current = {
        id: "u1",
        onboarded_at: null,
        onboarding_runtime_id: null,
        onboarding_runtime_skipped: false,
      };
      workspaceRef.current = { id: "w1", slug: "acme" };
      renderInit();

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith("/onboarding");
      });
      // No side-effects from the other branches.
      expect(mockBootstrap).not.toHaveBeenCalled();
      expect(mockEnsureContent).not.toHaveBeenCalled();
    });
  });
});
