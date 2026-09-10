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
const mockPreview = vi.hoisted(() => vi.fn());
const mockInstallPlugin = vi.hoisted(() => vi.fn());
const mockPublish = vi.hoisted(() => vi.fn());
const mockUpdate = vi.hoisted(() => vi.fn());
const mockWithdraw = vi.hoisted(() => vi.fn());

const data = vi.hoisted(() => ({
  items: [] as Array<Record<string, unknown>>,
  listings: [] as Array<Record<string, unknown>>,
  isLoading: false,
  role: "owner" as "owner" | "admin" | "member",
  publishEnabled: true,
  /** Records the filter the tab asked the catalog for. */
  lastFilter: undefined as unknown,
  /** Whether the catalog query was allowed to run this render. */
  catalogEnabled: undefined as unknown,
  directory: { packages: [] as Array<Record<string, unknown>> },
  installed: { plugins: [], plugins_enabled: true },
}));

// Three queries live on this tab — the build-time catalog, the instance plugin
// directory, and the workspace's own listings. They are told apart by the query
// key, so a test can populate one without the others.
vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey?: readonly unknown[]; enabled?: boolean }) => {
    const key = options?.queryKey ?? [];
    if (key[0] === "plugins") {
      return key[1] === "directory"
        ? { data: data.directory, isLoading: false, isError: false }
        : { data: data.installed, isLoading: false, isError: false };
    }
    if (key[2] === "marketplace-listings") {
      return { data: data.listings, isLoading: false };
    }
    data.catalogEnabled = options?.enabled;
    return { data: data.items, isLoading: data.isLoading };
  },
}));

vi.mock("@multica/core/plugins", () => ({
  pluginDirectoryOptions: () => ({ queryKey: ["plugins", "directory"] }),
  pluginInstallationsOptions: () => ({ queryKey: ["plugins", "installed"] }),
  usePreviewPlugin: () => ({ mutateAsync: mockPreview, isPending: false }),
  useInstallPlugin: () => ({ mutateAsync: mockInstallPlugin, isPending: false }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  marketplaceItemsOptions: (wsId: string, filter: unknown) => {
    data.lastFilter = filter;
    return { queryKey: ["workspaces", wsId, "marketplace", filter] };
  },
  marketplaceListingsOptions: (wsId: string) => ({
    queryKey: ["workspaces", wsId, "marketplace-listings"],
  }),
}));

vi.mock("@multica/core/workspace/mutations", () => ({
  useInstallMarketplaceItem: () => ({ mutateAsync: mockInstall, isPending: false }),
  usePublishMarketplaceListing: () => ({ mutateAsync: mockPublish, isPending: false }),
  useUpdateMarketplaceListing: () => ({ mutateAsync: mockUpdate, isPending: false }),
  useWithdrawMarketplaceListing: () => ({ mutateAsync: mockWithdraw, isPending: false }),
}));

vi.mock("@multica/core/config", () => ({
  useFeatureEnabled: () => data.publishEnabled,
}));

// Hoisted so the vi.mock factory below (which vitest lifts above this file's
// statements) can close over it without hitting a temporal dead zone.
const TestApiError = vi.hoisted(
  () =>
    class TestApiError extends Error {
      constructor(
        message: string,
        readonly status: number,
        readonly body?: unknown,
      ) {
        super(message);
      }
    },
);
vi.mock("@multica/core/api", () => ({ ApiError: TestApiError }));

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

const directoryPackage = (over: Record<string, unknown> = {}) => ({
  id: "package-1",
  plugin_key: "com.example.hello",
  name: "Hello Panel",
  created_at: "2026-01-01T00:00:00Z",
  visibility: "public",
  publisher_workspace_id: "workspace-2",
  versions: [
    {
      id: "version-2",
      version: "1.1.0",
      digest: "b".repeat(64),
      size_bytes: 2048,
      published_at: "2026-01-02T00:00:00Z",
      installed: false,
    },
  ],
  ...over,
});

describe("MarketplaceTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    data.role = "owner";
    data.isLoading = false;
    data.lastFilter = undefined;
    data.catalogEnabled = undefined;
    data.items = [skillItem(), mcpItem()];
    data.directory = { packages: [] };
    data.installed = { plugins: [], plugins_enabled: true };
    data.listings = [];
    data.publishEnabled = true;
    mockInstall.mockResolvedValue({});
    mockPublish.mockResolvedValue({});
    mockUpdate.mockResolvedValue({});
    mockWithdraw.mockResolvedValue({});
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

  // The plugin directory is a second shelf on the same tab, not a second query
  // against the catalog: it lists what workspaces on this instance published.
  it("lists instance plugins alongside the catalog", () => {
    data.directory = { packages: [directoryPackage()] };
    render(<MarketplaceTab />, { wrapper: Wrapper });

    expect(screen.getByText("pdf")).toBeInTheDocument();
    expect(screen.getByText("Hello Panel")).toBeInTheDocument();
    expect(screen.getByText("Plugin")).toBeInTheDocument();
  });

  it("stands the catalog query down when the reader filters to plugins", async () => {
    const user = userEvent.setup();
    data.directory = { packages: [directoryPackage()] };
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("tab", { name: "Plugins" }));

    await waitFor(() => expect(data.catalogEnabled).toBe(false));
    expect(screen.getByText("Hello Panel")).toBeInTheDocument();
    expect(screen.queryByText("pdf")).toBeNull();
  });

  it("hides the plugin shelf behind a catalog-only filter", async () => {
    const user = userEvent.setup();
    data.directory = { packages: [directoryPackage()] };
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("tab", { name: "Skills" }));

    await waitFor(() => expect(screen.queryByText("Hello Panel")).toBeNull());
  });

  // Installing from the directory must go through preview-then-consent, exactly
  // as installing from the workspace's own published list does. The scope list
  // the administrator reads IS the grant.
  it("previews before installing a plugin from the directory", async () => {
    const user = userEvent.setup();
    data.directory = { packages: [directoryPackage()] };
    mockPreview.mockResolvedValue({
      manifest: { key: "com.example.hello", name: "Hello Panel", version: "1.1.0", author: { name: "Acme" } },
      scopes: ["issues:read", "net:example.com"],
      config_schema: [],
      version_id: "version-2",
      version: "1.1.0",
      digest: "b".repeat(64),
      installed: false,
    });
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Review and install" }));

    await waitFor(() => expect(mockPreview).toHaveBeenCalledWith({ version_id: "version-2" }));
    expect(await screen.findByText("issues:read")).toBeInTheDocument();
    expect(screen.getByText("net:example.com")).toBeInTheDocument();
    expect(mockInstallPlugin).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Grant and install" }));
    await waitFor(() =>
      expect(mockInstallPlugin).toHaveBeenCalledWith({
        version_id: "version-2",
        granted_scopes: ["issues:read", "net:example.com"],
        config: {},
      }),
    );
  });

  // A directory row that says only "Hello Panel 1.1.0" makes the reader open the
  // consent flow just to learn what the plugin would be granted. The manifest
  // facts that decide "is this worth reviewing" belong on the row.
  it("shows what a listed version would be granted and would ask for", () => {
    data.directory = {
      packages: [
        directoryPackage({
          versions: [
            {
              id: "version-2",
              version: "1.1.0",
              digest: "b".repeat(64),
              size_bytes: 2048,
              published_at: "2026-01-02T00:00:00Z",
              installed: false,
              description: "Greets an issue.",
              scopes: ["issues:read", "net:example.com"],
              config_keys: ["repo", "token"],
            },
          ],
        }),
      ],
    };
    render(<MarketplaceTab />, { wrapper: Wrapper });

    expect(screen.getByText("Greets an issue.")).toBeInTheDocument();
    expect(screen.getByText("issues:read")).toBeInTheDocument();
    expect(screen.getByText("net:example.com")).toBeInTheDocument();
    expect(screen.getByText(/repo、token/)).toBeInTheDocument();
  });

  // A withdrawn version is the publisher saying "do not start on this one".
  it("does not offer a withdrawn version", () => {
    data.directory = {
      packages: [
        directoryPackage({
          versions: [
            {
              id: "version-2",
              version: "1.1.0",
              digest: "b".repeat(64),
              size_bytes: 2048,
              published_at: "2026-01-02T00:00:00Z",
              installed: false,
              withdrawn_at: "2026-01-03T00:00:00Z",
            },
          ],
        }),
      ],
    };
    render(<MarketplaceTab />, { wrapper: Wrapper });

    expect(screen.getByText("Hello Panel")).toBeInTheDocument();
    expect(screen.getByText("Every version has been withdrawn")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Review and install" })).toBeNull();
  });

  // plugins_v1 off is a server-side refusal; offering the button would only
  // produce a 503 the reader cannot act on.
  // With the flag off there is nothing behind the filter and nothing the
  // directory could offer, so the shelf and its filter both stay out rather
  // than leading the reader to an install that cannot happen.
  it("hides the plugin shelf and its filter when plugins are disabled", () => {
    data.directory = { packages: [directoryPackage()] };
    data.installed = { plugins: [], plugins_enabled: false };
    render(<MarketplaceTab />, { wrapper: Wrapper });

    expect(screen.queryByRole("tab", { name: "Plugins" })).toBeNull();
    expect(screen.queryByText("Hello Panel")).toBeNull();
    expect(screen.queryByRole("button", { name: "Review and install" })).toBeNull();
  });

  it("hides plugin installs from a plain member", () => {
    data.role = "member";
    data.directory = { packages: [directoryPackage()] };
    render(<MarketplaceTab />, { wrapper: Wrapper });

    expect(screen.getByText("Hello Panel")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Review and install" })).toBeNull();
  });
});

const listing = (over: Record<string, unknown> = {}) => ({
  id: "listing-1",
  key: "listing:listing-1",
  kind: "mcp",
  name: "acme-search",
  publisher_display_name: "Acme",
  summary: "Search Acme.",
  description: "",
  homepage_url: "",
  categories: ["development"],
  config_template: {
    type: "http",
    url: "https://mcp.example.com",
    headers: { Authorization: "Bearer ${api_key}" },
  },
  transport: "http",
  placeholders: [
    {
      key: "api_key",
      label: "API key",
      description: "",
      secret: true,
      required: true,
    },
  ],
  state: "published",
  revision: 3,
  created_at: "2026-09-08T00:00:00Z",
  updated_at: "2026-09-08T00:00:00Z",
  ...over,
});

// Summary and at least one category are required of every listing, matching
// what the server enforces. The canonical matrix for those rules lives in
// `marketplace-publish-dialog.test.tsx`; here they are just filled in so the
// flow under test can reach submit.
async function fillPublicMetadata(
  user: ReturnType<typeof userEvent.setup>,
  summary = "Search Acme.",
) {
  await user.type(screen.getByLabelText("Summary"), summary);
  await user.click(screen.getByRole("button", { name: "Development" }));
}

describe("MarketplaceTab publishing (RUYI-99)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    data.role = "owner";
    data.isLoading = false;
    data.items = [];
    data.listings = [];
    data.publishEnabled = true;
    mockPublish.mockResolvedValue({});
    mockUpdate.mockResolvedValue({});
    mockWithdraw.mockResolvedValue({});
  });

  // The write path has its own flag. Off means no publish surface at all —
  // not a button that fails at the server.
  it("hides the publish surface when the flag is off", () => {
    data.publishEnabled = false;
    data.listings = [listing()];
    render(<MarketplaceTab />, { wrapper: Wrapper });

    expect(screen.queryByRole("button", { name: "Publish a skill" })).toBeNull();
    expect(screen.queryByText("acme-search")).toBeNull();
    expect(
      screen.getByText(/Publishing to the marketplace is not enabled/),
    ).toBeInTheDocument();
  });

  it("hides the publish surface from a plain member", () => {
    data.role = "member";
    render(<MarketplaceTab />, { wrapper: Wrapper });

    expect(screen.queryByRole("button", { name: "Publish a skill" })).toBeNull();
    expect(
      screen.queryByText(/Publishing to the marketplace is not enabled/),
    ).toBeNull();
  });

  it("publishes a skill by its public source URL", async () => {
    const user = userEvent.setup();
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Publish a skill" }));
    await user.type(screen.getByLabelText("Name"), "acme-pdf");
    await user.type(screen.getByLabelText("Summary"), "Fill PDFs.");
    await user.click(screen.getByRole("button", { name: "Development" }));
    await user.type(
      screen.getByLabelText("Source URL"),
      "https://github.com/acme/skills/pdf",
    );
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "Publish" }),
    );

    await waitFor(() =>
      expect(mockPublish).toHaveBeenCalledWith(
        expect.objectContaining({
          kind: "skill",
          name: "acme-pdf",
          summary: "Fill PDFs.",
          source_url: "https://github.com/acme/skills/pdf",
        }),
      ),
    );
    // A skill listing carries no MCP template at all.
    expect(mockPublish.mock.calls[0]![0]).not.toHaveProperty("config_template");
  });

  // Transport fidelity is an acceptance criterion: sse must survive as sse
  // rather than collapsing into http, since they are different wire protocols.
  it.each([
    ["stdio", "stdio"],
    ["HTTP", "http"],
    ["SSE", "sse"],
  ])("publishes an MCP template on the %s transport", async (label, expected) => {
    const user = userEvent.setup();
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Publish an MCP server" }));
    await user.type(screen.getByLabelText("Name"), "acme-search");
    await fillPublicMetadata(user);
    await user.click(screen.getByRole("button", { name: label }));

    if (expected === "stdio") {
      await user.type(screen.getByLabelText("Command"), "npx");
      await user.type(screen.getByLabelText("Arguments"), "-y\n@acme/mcp");
    } else {
      await user.type(screen.getByLabelText("URL"), "https://mcp.example.com");
    }

    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "Publish" }),
    );

    await waitFor(() => expect(mockPublish).toHaveBeenCalled());
    const sent = mockPublish.mock.calls[0]![0] as { config_template: Record<string, unknown> };
    expect(sent.config_template.type).toBe(expected);
    if (expected === "stdio") {
      expect(sent.config_template.command).toBe("npx");
      expect(sent.config_template.args).toEqual(["-y", "@acme/mcp"]);
    } else {
      expect(sent.config_template.url).toBe("https://mcp.example.com");
    }
  });

  // A declared placeholder the template never references would render an input
  // on the install dialog that goes nowhere.
  it("refuses to publish a placeholder the template never uses", async () => {
    const user = userEvent.setup();
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Publish an MCP server" }));
    await user.type(screen.getByLabelText("Name"), "acme-search");
    await fillPublicMetadata(user);
    // The form opens on stdio; headers only exist on a remote transport.
    await user.click(screen.getByRole("button", { name: "HTTP" }));
    await user.type(screen.getByLabelText("URL"), "https://mcp.example.com");
    await user.click(screen.getByRole("button", { name: "Add placeholder" }));
    await user.type(screen.getByLabelText("Key"), "api_key");

    expect(screen.getByText(/never uses: api_key/)).toBeInTheDocument();
    expect(
      within(screen.getByRole("dialog")).getByRole("button", { name: "Publish" }),
    ).toBeDisabled();

    // Referencing it in a header clears the objection.
    await user.click(screen.getByRole("button", { name: "Add header" }));
    await user.type(screen.getByLabelText("Headers key"), "Authorization");
    // `{{` is userEvent's escape for a literal brace; the field receives
    // `Bearer ${api_key}`.
    await user.type(screen.getByLabelText("Headers value"), "Bearer ${{api_key}");

    await waitFor(() =>
      expect(
        within(screen.getByRole("dialog")).getByRole("button", { name: "Publish" }),
      ).toBeEnabled(),
    );
  });

  it("sends the revision it read when updating, so a stale edit is refused", async () => {
    const user = userEvent.setup();
    data.listings = [listing()];
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Edit" }));
    await user.clear(screen.getByLabelText("Summary"));
    await user.type(screen.getByLabelText("Summary"), "Search Acme faster.");
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "Save" }),
    );

    await waitFor(() =>
      expect(mockUpdate).toHaveBeenCalledWith(
        expect.objectContaining({
          id: "listing-1",
          revision: 3,
          summary: "Search Acme faster.",
        }),
      ),
    );
    // kind is fixed at publish time and the PATCH route does not accept it.
    expect(mockUpdate.mock.calls[0]![0]).not.toHaveProperty("kind");
  });

  // Reopening an existing MCP listing must land on the transport it was
  // published under, or a save would silently rewrite it.
  it("reopens an existing listing on its own transport", async () => {
    const user = userEvent.setup();
    data.listings = [
      listing({ config_template: { type: "sse", url: "https://sse.example.com" }, transport: "sse" }),
    ];
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.getByRole("button", { name: "SSE" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByLabelText("URL")).toHaveValue("https://sse.example.com");
  });

  it("withdraws only after a confirmation, sending the revision", async () => {
    const user = userEvent.setup();
    data.listings = [listing()];
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Withdraw" }));
    expect(screen.getByText(/keep their copy/)).toBeInTheDocument();
    expect(mockWithdraw).not.toHaveBeenCalled();

    await user.click(
      within(screen.getByRole("alertdialog")).getByRole("button", { name: "Withdraw" }),
    );
    await waitFor(() =>
      expect(mockWithdraw).toHaveBeenCalledWith({ id: "listing-1", revision: 3 }),
    );
  });

  // A withdrawn row is a tombstone: the name stays reserved for this
  // workspace, so the only action is publishing it again — the server refuses
  // a PATCH on a tombstone.
  it("offers republish rather than edit on a withdrawn listing", async () => {
    const user = userEvent.setup();
    data.listings = [listing({ state: "withdrawn", revision: 4 })];
    render(<MarketplaceTab />, { wrapper: Wrapper });

    expect(screen.getByText("Withdrawn")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Withdraw" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "Publish again" }));
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "Publish" }),
    );

    await waitFor(() => expect(mockPublish).toHaveBeenCalled());
    expect(mockUpdate).not.toHaveBeenCalled();
  });

  // The 422 scan report is location-only by construction. The dialog must
  // render it without echoing whatever was pasted.
  it("shows secret-scan findings by location and never the matched value", async () => {
    const user = userEvent.setup();
    mockPublish.mockRejectedValue(
      new TestApiError("content blocked", 422, {
        error: "content blocked",
        scanner_revision: "2026-09-01",
        findings: [
          { category: "token", rule: "generic-api-key", field: "headers.Authorization", line: 1, mask: "***" },
        ],
        truncated: false,
      }),
    );
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Publish an MCP server" }));
    await user.type(screen.getByLabelText("Name"), "acme-search");
    await fillPublicMetadata(user);
    // The form opens on stdio; headers only exist on a remote transport.
    await user.click(screen.getByRole("button", { name: "HTTP" }));
    await user.type(screen.getByLabelText("URL"), "https://mcp.example.com");
    await user.click(screen.getByRole("button", { name: "Add header" }));
    await user.type(screen.getByLabelText("Headers key"), "Authorization");
    await user.type(screen.getByLabelText("Headers value"), "Bearer sk-live-not-real");
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "Publish" }),
    );

    const dialog = await screen.findByRole("dialog");
    await waitFor(() =>
      expect(within(dialog).getByText(/looks like it contains a credential/)).toBeInTheDocument(),
    );
    expect(
      within(dialog).getByText(/headers\.Authorization, line 1 — generic-api-key/),
    ).toBeInTheDocument();
    expect(within(dialog).getByText(/Scanner 2026-09-01/)).toBeInTheDocument();
    // The pasted value must not be echoed back anywhere in the report.
    expect(within(dialog).queryByText(/sk-live-not-real/)).toBeNull();
    // The dialog stays open so the publisher can fix the field.
    expect(within(dialog).getByLabelText("Name")).toHaveValue("acme-search");
  });

  // A conflict is not a scan report; it must surface as its own message.
  it("shows a revision conflict inline instead of a scan report", async () => {
    const user = userEvent.setup();
    data.listings = [listing()];
    mockUpdate.mockRejectedValue(
      new TestApiError("this listing was changed by someone else", 409, {
        error: "this listing was changed by someone else",
      }),
    );
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Edit" }));
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", { name: "Save" }),
    );

    await waitFor(() =>
      expect(screen.getByText(/changed by someone else/)).toBeInTheDocument(),
    );
  });

  // Reopening on a different listing must not carry the previous one's
  // template across — that publishes one server's config under another name.
  it("clears the form when reopening on another listing", async () => {
    const user = userEvent.setup();
    data.listings = [
      listing(),
      listing({ id: "listing-2", name: "other", summary: "Other.", config_template: { type: "stdio", command: "npx" }, transport: "stdio" }),
    ];
    render(<MarketplaceTab />, { wrapper: Wrapper });

    await user.click(screen.getAllByRole("button", { name: "Edit" })[0]!);
    expect(screen.getByLabelText("Name")).toHaveValue("acme-search");
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    await user.click(screen.getAllByRole("button", { name: "Edit" })[1]!);
    await waitFor(() => expect(screen.getByLabelText("Name")).toHaveValue("other"));
    expect(screen.getByLabelText("Command")).toHaveValue("npx");
  });

  // Forward compatibility: a state a newer backend introduces must not be
  // mislabelled as published or withdrawn.
  it("renders an unknown listing state as itself", () => {
    data.listings = [listing({ state: "under_review" })];
    render(<MarketplaceTab />, { wrapper: Wrapper });

    expect(screen.getByText("under_review")).toBeInTheDocument();
  });

  it("renders an empty state when nothing has been published", () => {
    render(<MarketplaceTab />, { wrapper: Wrapper });

    expect(
      screen.getByText("This workspace has not published anything yet."),
    ).toBeInTheDocument();
  });
});
