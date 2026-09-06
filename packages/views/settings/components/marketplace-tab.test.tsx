// @vitest-environment jsdom

import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";
import enAgents from "../../locales/en/agents.json";

const mockInstall = vi.hoisted(() => vi.fn());

const data = vi.hoisted(() => ({
  items: [] as Array<Record<string, unknown>>,
  isLoading: false,
  role: "owner" as "owner" | "admin" | "member",
  /** Records the filter the tab asked the catalog for. */
  lastFilter: undefined as unknown,
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: data.items, isLoading: data.isLoading }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  marketplaceItemsOptions: (wsId: string, filter: unknown) => {
    data.lastFilter = filter;
    return { queryKey: ["workspaces", wsId, "marketplace", filter] };
  },
}));

vi.mock("@multica/core/workspace/mutations", () => ({
  useInstallMarketplaceItem: () => ({ mutateAsync: mockInstall, isPending: false }),
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "workspace-1", name: "Acme", slug: "acme" }),
}));

vi.mock("@multica/core/permissions", () => ({
  useCurrentMember: () => ({ role: data.role, isLoading: false }),
}));

const toastError = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: toastError } }));

import { MarketplaceTab } from "./marketplace-tab";

const TEST_RESOURCES = {
  en: { common: enCommon, settings: enSettings, agents: enAgents },
};

function Wrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

const skillItem = (over: Record<string, unknown> = {}) => ({
  key: "skill:anthropics/skills/pdf",
  kind: "skill",
  name: "pdf",
  summary: "Read, fill, and generate PDF documents.",
  description: "",
  publisher: "Anthropic",
  homepage_url: "",
  categories: ["documents"],
  source_url: "https://github.com/anthropics/skills/tree/main/document-skills/pdf",
  installed: false,
  ...over,
});

const mcpItem = (over: Record<string, unknown> = {}) => ({
  key: "mcp:modelcontextprotocol/github",
  kind: "mcp",
  name: "github",
  summary: "Read and write GitHub issues, pull requests, and code.",
  description: "",
  publisher: "Model Context Protocol",
  homepage_url: "",
  categories: ["development"],
  placeholders: [
    {
      key: "github_token",
      label: "GitHub personal access token",
      description: "",
      secret: true,
      required: true,
    },
  ],
  installed: false,
  ...over,
});

describe("MarketplaceTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    data.role = "owner";
    data.isLoading = false;
    data.lastFilter = undefined;
    data.items = [skillItem(), mcpItem()];
    mockInstall.mockResolvedValue({});
  });

  it("lists skills and MCP servers together in one marketplace", () => {
    render(<MarketplaceTab />, { wrapper: Wrapper });

    expect(screen.getByText("pdf")).toBeInTheDocument();
    expect(screen.getByText("Skill")).toBeInTheDocument();
    expect(screen.getByText("github")).toBeInTheDocument();
    expect(screen.getByText("MCP")).toBeInTheDocument();
  });

  it("sends the kind filter and the search term to the catalog query", async () => {
    const user = userEvent.setup();
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("tab", { name: "MCP servers" }));
    await waitFor(() =>
      expect(data.lastFilter).toEqual(expect.objectContaining({ kind: "mcp" })),
    );

    await user.type(screen.getByPlaceholderText("Search the marketplace"), "git");
    await waitFor(() =>
      expect(data.lastFilter).toEqual({ kind: "mcp", q: "git" }),
    );
  });

  it("marks an already installed entry and still offers to install again", () => {
    data.items = [skillItem({ installed: true, installed_id: "skill-1" })];
    render(<MarketplaceTab />, { wrapper: Wrapper });

    expect(screen.getByText("Installed")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Install again" })).toBeInTheDocument();
  });

  // The install gate lives on the server (owner/admin only), so a member seeing
  // a button would only ever produce a 403 — the catalog itself is harmless to
  // browse and stays visible.
  it("hides the install button from a plain member but keeps the catalog", () => {
    data.role = "member";
    render(<MarketplaceTab />, { wrapper: Wrapper });

    expect(screen.getByText("pdf")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Install" })).toBeNull();
    expect(
      screen.getByText(/Only workspace owners and admins can install/),
    ).toBeInTheDocument();
  });

  it("installs a skill by key, without a name or values to fill", async () => {
    const user = userEvent.setup();
    data.items = [skillItem()];
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Install" }));
    // A skill has no configuration, so the dialog only confirms the source.
    expect(screen.getByText(/will be imported from/)).toBeInTheDocument();
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Install" }));

    await waitFor(() =>
      expect(mockInstall).toHaveBeenCalledWith({
        key: "skill:anthropics/skills/pdf",
        name: "pdf",
        values: {},
      }),
    );
  });

  it("collects an MCP credential in a password field and sends it with the install", async () => {
    const user = userEvent.setup();
    data.items = [mcpItem()];
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Install" }));

    const secret = screen.getByLabelText(/GitHub personal access token/);
    // A credential must never be a plain text input, and must not be offered
    // back by the browser's autofill on some unrelated form.
    expect(secret).toHaveAttribute("type", "password");
    expect(secret).toHaveAttribute("autocomplete", "new-password");

    await user.type(secret, "ghp-not-a-real-token");
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Install" }));

    await waitFor(() =>
      expect(mockInstall).toHaveBeenCalledWith({
        key: "mcp:modelcontextprotocol/github",
        name: "github",
        values: { github_token: "ghp-not-a-real-token" },
      }),
    );
  });

  // Submitting an empty required credential would store a broken entry that
  // only fails later, inside an agent's run.
  it("blocks the install until every required value is supplied", async () => {
    const user = userEvent.setup();
    data.items = [mcpItem()];
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Install" }));

    const submit = within(screen.getByRole("dialog")).getByRole("button", { name: "Install" });
    expect(submit).toBeDisabled();

    await user.type(screen.getByLabelText(/GitHub personal access token/), "t");
    expect(submit).toBeEnabled();
  });

  // Names are unique per workspace, so an install under a taken name can only
  // come back 409 — the dialog says so before the round trip.
  it("refuses a server name the workspace already uses", async () => {
    const user = userEvent.setup();
    data.items = [
      mcpItem({ key: "mcp:a", name: "github", installed: true }),
      mcpItem({ key: "mcp:b", name: "fetch", placeholders: [] }),
    ];
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getAllByRole("button", { name: "Install" })[0]!);
    const nameInput = screen.getByLabelText("Server name");
    await user.clear(nameInput);
    await user.type(nameInput, "github");

    expect(screen.getByText(/already has a server with that name/)).toBeInTheDocument();
    expect(within(screen.getByRole("dialog")).getByRole("button", { name: "Install" })).toBeDisabled();
  });

  // Reopening on another entry must not carry the previous entry's credential
  // across — that would submit one server's token to a different server.
  it("clears collected values when the dialog reopens on another entry", async () => {
    const user = userEvent.setup();
    data.items = [
      mcpItem(),
      mcpItem({
        key: "mcp:modelcontextprotocol/other",
        name: "other",
        placeholders: [
          {
            key: "github_token",
            label: "GitHub personal access token",
            description: "",
            secret: true,
            required: true,
          },
        ],
      }),
    ];
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getAllByRole("button", { name: "Install" })[0]!);
    await user.type(
      screen.getByLabelText(/GitHub personal access token/),
      "ghp-not-a-real-token",
    );
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    // Open the OTHER entry's row button, not the dialog's submit.
    await user.click(screen.getAllByRole("button", { name: "Install" })[1]!);
    await waitFor(() =>
      expect(screen.getByLabelText(/GitHub personal access token/)).toHaveValue(""),
    );
  });

  it("surfaces the server's reason when an install fails", async () => {
    const user = userEvent.setup();
    data.items = [skillItem()];
    mockInstall.mockRejectedValue(new Error("skill already exists"));
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Install" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Install" }));

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith("skill already exists"),
    );
  });

  // Forward compatibility: a kind this client cannot install must not vanish
  // from the listing, and must not offer a button that would fail.
  it("lists an unknown kind without offering to install it", () => {
    data.items = [{ ...skillItem(), key: "future:1", kind: "workflow", name: "future" }];
    render(<MarketplaceTab />, { wrapper: Wrapper });

    expect(screen.getByText("future")).toBeInTheDocument();
    expect(screen.getByText("workflow")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Install" })).toBeNull();
  });

  it("renders an empty state when nothing matches", () => {
    data.items = [];
    render(<MarketplaceTab />, { wrapper: Wrapper });

    expect(screen.getByText("Nothing matches")).toBeInTheDocument();
  });

  it("survives a payload that is not an array", () => {
    data.items = undefined as unknown as typeof data.items;
    render(<MarketplaceTab />, { wrapper: Wrapper });

    expect(screen.getByText("Nothing matches")).toBeInTheDocument();
  });
});
