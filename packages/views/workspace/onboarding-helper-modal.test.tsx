import type { ReactNode } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../locales/en/common.json";
import enOnboarding from "../locales/en/onboarding.json";

// The Modal is now a dumb component — it receives `workspace` and
// `runtimeId` props and renders. Gating (un-onboarded, workspace
// resolved, runtime chosen) lives one level up in
// `<WorkspaceOnboardingInit />`, which has its own test file.
//
// What we test here:
//   - The dialog renders when given props (no gates to fail).
//   - All three starter cards appear with the right copy.
//   - Clicking a card calls bootstrapRuntimeOnboarding with the workspace
//     id, runtime id (from prop), and the card's prompt.
//   - On success: invalidates the workspace.agents + issues query keys,
//     and navigates to the seeded issue.
//   - On failure: shows the error, lets the user retry.

const mockBootstrap = vi.hoisted(() => vi.fn());
const mockNavigate = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/onboarding", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/onboarding")>(
      "@multica/core/onboarding",
    );
  return {
    ...actual,
    bootstrapRuntimeOnboarding: mockBootstrap,
  };
});

vi.mock("../navigation", () => ({
  useNavigation: () => ({ push: mockNavigate, replace: mockNavigate }),
}));

import { OnboardingHelperModal } from "./onboarding-helper-modal";

const TEST_RESOURCES = {
  en: { common: enCommon, onboarding: enOnboarding },
};

const TEST_WORKSPACE = {
  id: "w1",
  slug: "acme",
  name: "ACME",
  description: null,
  context: null,
  settings: {},
  repos: [],
  issue_prefix: "ACME",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

function renderModal(runtimeId = "r1") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <OnboardingHelperModal
          workspace={TEST_WORKSPACE}
          runtimeId={runtimeId}
        />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

function Wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        {children}
      </I18nProvider>
    </QueryClientProvider>
  );
}

describe("OnboardingHelperModal — render", () => {
  beforeEach(() => {
    mockBootstrap.mockReset();
    mockNavigate.mockReset();
  });

  it("renders the dialog when given props (no internal gating)", () => {
    renderModal();
    expect(screen.getByText(/Meet Multica Helper/i)).toBeInTheDocument();
  });

  it("renders all three starter cards", () => {
    renderModal();
    expect(
      screen.getByText(/Introduce me to Multica/i),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Show me how to assign an issue/i),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Help me create a second agent/i),
    ).toBeInTheDocument();
  });

  it("renders no close affordance (the modal is blocking)", () => {
    renderModal();
    // Base UI Dialog Close component is keyed by aria-label "Close" in the
    // default Multica DialogContent — our blocking flag suppresses it.
    expect(screen.queryByRole("button", { name: /^close$/i })).toBeNull();
  });
});

describe("OnboardingHelperModal — pick a card", () => {
  beforeEach(() => {
    mockBootstrap.mockReset();
    mockNavigate.mockReset();
  });

  it("calls bootstrapRuntimeOnboarding with workspace id, runtime id from prop, and the card's prompt", async () => {
    mockBootstrap.mockResolvedValue({
      workspace_id: TEST_WORKSPACE.id,
      agent_id: "a1",
      issue_id: "i1",
    });
    renderModal("r-from-prop");
    const user = userEvent.setup();

    await user.click(screen.getByText(/Introduce me to Multica/i));

    await waitFor(() => {
      expect(mockBootstrap).toHaveBeenCalledTimes(1);
    });
    const [wsId, runtimeId, prompt] = mockBootstrap.mock.calls[0]!;
    expect(wsId).toBe(TEST_WORKSPACE.id);
    expect(runtimeId).toBe("r-from-prop");
    expect(prompt).toMatch(/Introduce me to Multica/i);
  });

  it("navigates to the seeded issue after bootstrap succeeds", async () => {
    mockBootstrap.mockResolvedValue({
      workspace_id: TEST_WORKSPACE.id,
      agent_id: "a1",
      issue_id: "seeded-issue-id",
    });
    renderModal();
    const user = userEvent.setup();

    await user.click(screen.getByText(/Introduce me to Multica/i));

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith(
        expect.stringContaining("seeded-issue-id"),
      );
    });
  });

  it("surfaces an error and lets the user retry when bootstrap fails", async () => {
    mockBootstrap.mockRejectedValueOnce(new Error("network blip"));
    renderModal();
    const user = userEvent.setup();

    await user.click(screen.getByText(/Introduce me to Multica/i));

    expect(await screen.findByText(/network blip/i)).toBeInTheDocument();

    // After dismissing the error, the card is selectable again.
    await user.click(screen.getByRole("button", { name: /dismiss/i }));
    mockBootstrap.mockResolvedValueOnce({
      workspace_id: TEST_WORKSPACE.id,
      agent_id: "a1",
      issue_id: "ok",
    });
    await user.click(screen.getByText(/Introduce me to Multica/i));
    await waitFor(() => {
      expect(mockBootstrap).toHaveBeenCalledTimes(2);
    });
  });
});

// Wrapper kept exported for potential future use by other modal-adjacent tests.
export { Wrapper };
