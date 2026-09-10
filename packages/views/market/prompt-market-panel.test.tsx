// @vitest-environment jsdom

import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../locales/en/common.json";
import enPromptMarket from "../locales/en/prompt-market.json";

const mockInstall = vi.hoisted(() => vi.fn());
const mockWithdraw = vi.hoisted(() => vi.fn());

const data = vi.hoisted(() => ({
  catalog: [] as Array<Record<string, unknown>>,
  installs: [] as Array<Record<string, unknown>>,
  detail: null as Record<string, unknown> | null,
  role: "owner" as "owner" | "admin" | "member",
  /** Every filter the panel asked the catalog query for. */
  filters: [] as unknown[],
}));

// Routed by query key so the catalog, the install library and the detail read
// can disagree — which is the whole point of the two-segment layout.
vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey: readonly unknown[]; enabled?: boolean }) => {
    const kind = options.queryKey[3];
    if (kind === "installs") return { data: data.installs, isLoading: false };
    if (kind === "version")
      return { data: data.detail, isLoading: false, isError: false, refetch: vi.fn() };
    return { data: data.catalog, isLoading: false };
  },
}));

vi.mock("@multica/core/workspace/queries", () => ({
  promptMarketOptions: (wsId: string, filter: unknown) => {
    data.filters.push(filter);
    return { queryKey: ["workspaces", wsId, "prompt-market", "catalog"] };
  },
  promptInstallsOptions: (wsId: string) => ({
    queryKey: ["workspaces", wsId, "prompt-market", "installs"],
  }),
  promptVersionOptions: (wsId: string, versionId: string) => ({
    queryKey: ["workspaces", wsId, "prompt-market", "version", versionId],
  }),
}));

vi.mock("@multica/core/workspace/mutations", () => ({
  useInstallPrompt: () => ({ mutateAsync: mockInstall, isPending: false }),
  useWithdrawPromptVersion: () => ({ mutateAsync: mockWithdraw, isPending: false }),
}));

vi.mock("@multica/core/permissions", () => ({
  useCurrentMember: () => ({ role: data.role, isLoading: false }),
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

// The apply wizard has its own query surface and its own suite; this file is
// about the catalog, the install gate and the detail pane.
vi.mock("./prompt-apply-dialog", () => ({
  PromptApplyDialog: ({ open }: { open: boolean }) =>
    open ? <div data-testid="apply-dialog" /> : null,
}));

import { PromptMarketPanel } from "./prompt-market-panel";

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

const item = (over: Record<string, unknown> = {}) => ({
  id: "version-1",
  series_id: "series-1",
  kind: "agent_prompt",
  version: 2,
  name: "Support triage",
  summary: "Sorts inbound tickets.",
  audience: "",
  categories: [],
  license_code: "cc-by-4.0",
  usage_notes: "",
  companions: "",
  publisher_display_name: "Dana Wu",
  content: "",
  content_sha256: "",
  state: "published",
  visibility: "public",
  scanner_revision: "r1",
  published_at: "2026-09-01T00:00:00Z",
  withdrawn_at: null,
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-01T00:00:00Z",
  installed: false,
  update_available: false,
  ...over,
});

const installRow = (over: Record<string, unknown> = {}) => ({
  id: "install-1",
  series_id: "series-1",
  kind: "agent_prompt",
  installed_version_id: "version-1",
  installed_version: 2,
  installed_content_sha256: "abc",
  name: "Support triage",
  summary: "Sorts inbound tickets.",
  publisher_display_name: "Dana Wu",
  license_code: "cc-by-4.0",
  installed_at: "2026-09-02T00:00:00Z",
  updated_at: "2026-09-02T00:00:00Z",
  ...over,
});

beforeEach(() => {
  data.catalog = [item()];
  data.installs = [];
  data.detail = null;
  data.role = "owner";
  data.filters = [];
  mockInstall.mockReset().mockResolvedValue({});
  mockWithdraw.mockReset().mockResolvedValue({});
});

describe("PromptMarketPanel catalog", () => {
  it("lists a published version with its publisher and licence", () => {
    render(<PromptMarketPanel wsId="ws-1" />, { wrapper: Wrapper });

    expect(screen.getByText("Support triage")).toBeInTheDocument();
    expect(screen.getByText(/Dana Wu/)).toBeInTheDocument();
    expect(screen.getByText(/CC BY 4.0/)).toBeInTheDocument();
  });

  // Owner decision D4: the catalog identifies a publisher, never their
  // workspace. A row that leaked it would expose one tenant's org chart to
  // every other tenant.
  it("renders no source workspace anywhere in a row", () => {
    render(<PromptMarketPanel wsId="ws-1" />, { wrapper: Wrapper });

    expect(screen.queryByText(/workspace/i)).not.toBeInTheDocument();
  });

  it("passes the kind filter through to the query", async () => {
    const user = userEvent.setup();
    render(<PromptMarketPanel wsId="ws-1" />, { wrapper: Wrapper });

    await user.click(screen.getByRole("tab", { name: "Squad prompts" }));

    await waitFor(() => {
      expect(data.filters.at(-1)).toEqual({ kind: "squad_prompt", q: "" });
    });
  });

  it("marks a series with a newer published version", () => {
    data.catalog = [item({ installed: true, update_available: true })];
    render(<PromptMarketPanel wsId="ws-1" />, { wrapper: Wrapper });

    expect(screen.getByText("v2 available")).toBeInTheDocument();
  });
});

describe("PromptMarketPanel install gate", () => {
  it("offers no install to a plain member and says why", async () => {
    data.role = "member";
    const user = userEvent.setup();
    render(<PromptMarketPanel wsId="ws-1" />, { wrapper: Wrapper });

    // The catalog still lists — a member may browse; only installing is gated.
    expect(screen.getByText("Support triage")).toBeInTheDocument();
    expect(
      screen.getByText(/Only workspace owners and admins can install/),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Details" }));
    expect(
      screen.queryByRole("button", { name: "Install" }),
    ).not.toBeInTheDocument();
  });

  it("installs only after the confirmation step", async () => {
    data.detail = { ...item(), content: "You are a triage agent." };
    const user = userEvent.setup();
    render(<PromptMarketPanel wsId="ws-1" />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Details" }));
    await user.click(screen.getByRole("button", { name: "Install" }));

    // Opening the confirm dialog must not have installed anything yet.
    expect(mockInstall).not.toHaveBeenCalled();
    expect(
      screen.getByText(/It writes to no agent and no squad/),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Install" }));
    await waitFor(() => expect(mockInstall).toHaveBeenCalledWith("version-1"));
  });
});

describe("PromptMarketPanel withdrawn versions", () => {
  it("keeps a withdrawn version readable but not installable", async () => {
    data.catalog = [item({ state: "withdrawn" })];
    data.detail = { ...item({ state: "withdrawn" }), content: "text" };
    const user = userEvent.setup();
    render(<PromptMarketPanel wsId="ws-1" />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Details" }));

    expect(screen.getByText(/This version has been withdrawn/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Install" })).not.toBeInTheDocument();
  });

  it("offers withdrawal only to the publisher of the version", async () => {
    // `source_id` comes back only for a reader who manages the source object.
    data.detail = { ...item(), content: "text", source_type: "agent", source_id: "a1" };
    const user = userEvent.setup();
    render(<PromptMarketPanel wsId="ws-1" />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Details" }));
    expect(screen.getByRole("button", { name: "Withdraw" })).toBeInTheDocument();
  });

  it("hides withdrawal from a reader who does not manage the source", async () => {
    data.detail = { ...item(), content: "text" };
    const user = userEvent.setup();
    render(<PromptMarketPanel wsId="ws-1" />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Details" }));
    expect(screen.queryByRole("button", { name: "Withdraw" })).not.toBeInTheDocument();
  });
});

describe("PromptMarketPanel installed segment", () => {
  it("holds an install without applying it", async () => {
    data.installs = [installRow()];
    const user = userEvent.setup();
    render(<PromptMarketPanel wsId="ws-1" />, { wrapper: Wrapper });

    await user.click(screen.getByRole("tab", { name: "Installed prompts" }));

    expect(screen.getByText("Support triage")).toBeInTheDocument();
    // Applying is a separate, explicitly opened flow.
    expect(screen.queryByTestId("apply-dialog")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Apply to target" }));
    expect(screen.getByTestId("apply-dialog")).toBeInTheDocument();
  });

  it("explains an empty library rather than showing a bare list", async () => {
    const user = userEvent.setup();
    render(<PromptMarketPanel wsId="ws-1" />, { wrapper: Wrapper });

    await user.click(screen.getByRole("tab", { name: "Installed prompts" }));
    expect(screen.getByText("No installed prompts")).toBeInTheDocument();
  });
});
