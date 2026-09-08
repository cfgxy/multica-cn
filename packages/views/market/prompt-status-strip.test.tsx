// @vitest-environment jsdom

import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../locales/en/common.json";
import enPromptMarket from "../locales/en/prompt-market.json";

const mockUseQuery = vi.hoisted(() => vi.fn());
const mockRestore = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());

const data = vi.hoisted(() => ({
  flagOn: true,
  state: null as Record<string, unknown> | null,
  versions: [] as Array<Record<string, unknown>>,
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey: readonly unknown[] }) => {
    mockUseQuery(options.queryKey);
    const kind = options.queryKey[options.queryKey.length - 1];
    if (kind === "source-versions") return { data: data.versions, isLoading: false };
    return { data: data.state, isLoading: false };
  },
}));

vi.mock("@multica/core/config", () => ({
  useFeatureEnabled: () => data.flagOn,
}));

vi.mock("@multica/core/workspace/queries", () => ({
  promptTargetStateOptions: (wsId: string, type: string, id: string) => ({
    queryKey: ["workspaces", wsId, type, id, "prompt-state"],
  }),
  promptSourceVersionsOptions: (wsId: string, type: string, id: string) => ({
    queryKey: ["workspaces", wsId, type, id, "source-versions"],
  }),
}));

vi.mock("@multica/core/workspace/mutations", () => ({
  useRestorePrompt: () => ({ mutateAsync: mockRestore, isPending: false }),
  // The publish dialog is a child; its own suite owns these.
  useCreatePromptVersion: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useUpdatePromptVersion: () => ({ mutateAsync: vi.fn(), isPending: false }),
  usePublishPromptVersion: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));

vi.mock("sonner", () => ({
  toast: { success: mockToastSuccess, error: mockToastError },
}));

vi.mock("@multica/core/utils", () => ({ createSafeId: () => "operation-id" }));

import { PromptMarketStatusStrip } from "./prompt-status-strip";

const TEST_RESOURCES = {
  en: { common: enCommon, "prompt-market": enPromptMarket },
};

function Wrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

function renderStrip(over: Record<string, unknown> = {}) {
  return render(
    <PromptMarketStatusStrip
      wsId="ws-1"
      targetType="agent"
      targetId="agent-1"
      targetName="Triage bot"
      canManage
      {...over}
    />,
    { wrapper: Wrapper },
  );
}

beforeEach(() => {
  data.flagOn = true;
  data.state = {
    applied_version: 2,
    applied_content_intact: true,
    can_restore: true,
    current_sha256: "current-hash",
  };
  data.versions = [
    { id: "version-1", series_id: "series-1", state: "published", version: 2 },
  ];
  mockUseQuery.mockReset();
  mockRestore.mockReset().mockResolvedValue({ restored: true });
  mockToastError.mockReset();
  mockToastSuccess.mockReset();
});

describe("PromptMarketStatusStrip gating", () => {
  it("renders nothing and issues no request with the flag off", () => {
    data.flagOn = false;
    const { container } = renderStrip();

    expect(container).toBeEmptyDOMElement();
    // The regression this guards is a prompt tab that starts failing two
    // network calls on every server where the market is off.
    expect(mockUseQuery).not.toHaveBeenCalled();
  });

  it("renders nothing for a viewer who cannot manage the target", () => {
    const { container } = renderStrip({ canManage: false });

    expect(container).toBeEmptyDOMElement();
    expect(mockUseQuery).not.toHaveBeenCalled();
  });
});

describe("PromptMarketStatusStrip state", () => {
  it("reports the applied version", () => {
    renderStrip();

    expect(screen.getByText(/Using “Triage bot” v2 from the market/)).toBeInTheDocument();
  });

  it("distinguishes an applied prompt that was edited afterwards", () => {
    data.state = {
      applied_version: 2,
      applied_content_intact: false,
      can_restore: false,
      current_sha256: "current-hash",
    };
    renderStrip();

    expect(screen.getByText(/was applied, then edited here/)).toBeInTheDocument();
  });

  it("hides restore rather than offering a button that only errors", () => {
    data.state = {
      applied_version: 2,
      applied_content_intact: false,
      can_restore: false,
      current_sha256: "current-hash",
    };
    renderStrip();

    expect(
      screen.queryByRole("button", { name: "Restore previous version" }),
    ).not.toBeInTheDocument();
  });

  it("offers to publish a new version once one is published", () => {
    renderStrip();

    expect(
      screen.getByRole("button", { name: /Publish a new version/ }),
    ).toBeInTheDocument();
  });

  it("offers a first publish when nothing is published yet", () => {
    data.state = null;
    data.versions = [];
    renderStrip();

    expect(screen.getByRole("button", { name: /Publish to market/ })).toBeInTheDocument();
  });
});

describe("PromptMarketStatusStrip unsaved edits", () => {
  it("blocks publishing and says why", () => {
    renderStrip({ hasUnsavedEdits: true });

    expect(screen.getByRole("button", { name: /Publish a new version/ })).toBeDisabled();
    expect(screen.getByText(/snapshots the saved text/)).toBeInTheDocument();
  });
});

describe("PromptMarketStatusStrip restore", () => {
  it("sends the hash the strip was rendered from", async () => {
    const user = userEvent.setup();
    renderStrip();
    await user.click(screen.getByRole("button", { name: "Restore previous version" }));
    await user.click(await screen.findByRole("button", { name: "Restore" }));

    // Without the hash a concurrent edit would be discarded silently instead
    // of turning the race into a refusal.
    await waitFor(() =>
      expect(mockRestore).toHaveBeenCalledWith({
        targetType: "agent",
        targetId: "agent-1",
        operation_id: "operation-id",
        expected_sha256: "current-hash",
      }),
    );
    expect(mockToastSuccess).toHaveBeenCalled();
  });

  it("reports a refusal caused by a hand edit in its own words", async () => {
    data.state = {
      applied_version: 2,
      applied_content_intact: false,
      can_restore: true,
      current_sha256: "current-hash",
    };
    mockRestore.mockRejectedValue(new Error("refused"));
    const user = userEvent.setup();
    renderStrip();
    await user.click(screen.getByRole("button", { name: "Restore previous version" }));
    await user.click(await screen.findByRole("button", { name: "Restore" }));

    await waitFor(() =>
      expect(mockToastError).toHaveBeenCalledWith(
        expect.stringContaining("edited after it was applied"),
      ),
    );
  });

  it("reports an ordinary failure as a plain failure", async () => {
    mockRestore.mockRejectedValue(new Error("boom"));
    const user = userEvent.setup();
    renderStrip();
    await user.click(screen.getByRole("button", { name: "Restore previous version" }));
    await user.click(await screen.findByRole("button", { name: "Restore" }));

    await waitFor(() =>
      expect(mockToastError).toHaveBeenCalledWith("Restore failed"),
    );
  });
});
