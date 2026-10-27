import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const mockPreview = vi.hoisted(() => vi.fn());
const mockInstall = vi.hoisted(() => vi.fn());
const mockConfigure = vi.hoisted(() => vi.fn());
const mockSetEnabled = vi.hoisted(() => vi.fn());
const mockUninstall = vi.hoisted(() => vi.fn());
const mockPublish = vi.hoisted(() => vi.fn());
const mockDeletePackage = vi.hoisted(() => vi.fn());
const mockSetVisibility = vi.hoisted(() => vi.fn());
const mockSetWithdrawn = vi.hoisted(() => vi.fn());
const mockClearSecret = vi.hoisted(() => vi.fn());

const data = vi.hoisted(() => ({
  installed: {
    plugins: [] as Array<Record<string, unknown>>,
    // The server answers with the mode it served in, and the tab reads that
    // rather than the flag — see the read-and-remove case below.
    plugins_enabled: true,
  },
  packages: { packages: [] as Array<Record<string, unknown>> },
  role: "owner" as "owner" | "admin" | "member",
}));

vi.mock("@tanstack/react-query", () => ({
  // Two queries reach this tab: what is installed, and what has been published.
  // They are told apart by the query key so a test can have one without the
  // other — which is the normal state right after a publish.
  useQuery: (options: { queryKey?: readonly unknown[] }) =>
    options?.queryKey?.[1] === "packages"
      ? { data: data.packages, isLoading: false, isError: false }
      : { data: data.installed, isLoading: false, isError: false },
}));

vi.mock("@multica/core/plugins", () => ({
  pluginInstallationsOptions: () => ({ queryKey: ["plugins", "installed"] }),
  pluginPackagesOptions: () => ({ queryKey: ["plugins", "packages"] }),
  usePreviewPlugin: () => ({ mutateAsync: mockPreview, isPending: false }),
  useInstallPlugin: () => ({ mutateAsync: mockInstall, isPending: false }),
  useConfigurePlugin: () => ({ mutateAsync: mockConfigure, isPending: false }),
  useSetPluginEnabled: () => ({ mutateAsync: mockSetEnabled, isPending: false }),
  useUninstallPlugin: () => ({ mutateAsync: mockUninstall, isPending: false }),
  usePublishPluginPackage: () => ({ mutateAsync: mockPublish, isPending: false }),
  useDeletePluginPackage: () => ({ mutateAsync: mockDeletePackage, isPending: false }),
  useSetPluginPackageVisibility: () => ({ mutateAsync: mockSetVisibility, isPending: false }),
  useSetPluginVersionWithdrawn: () => ({ mutateAsync: mockSetWithdrawn, isPending: false }),
  useClearPluginSecret: () => ({ mutateAsync: mockClearSecret, isPending: false }),
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "workspace-1", name: "Acme", slug: "acme" }),
}));

vi.mock("@multica/core/permissions", () => ({
  useCurrentMember: () => ({ role: data.role, isLoading: false }),
}));

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { PluginsTab } from "./plugins-tab";

const TEST_RESOURCES = { en: { common: enCommon, settings: enSettings } };

function Wrapper({ children }: { children: ReactNode }) {
  return <I18nProvider locale="en" resources={TEST_RESOURCES}>{children}</I18nProvider>;
}

const INSTALLATION = {
  id: "installation-1",
  plugin_key: "com.example.hello",
  name: "Hello Panel",
  description: "A greeting panel.",
  version: "1.0.0",
  package_version_id: "version-1",
  enabled: true,
  granted_scopes: ["issues:read", "comments:write", "net:example.com"],
  config_schema: [
    { key: "repo", type: "string", label: "Repo", required: true, options: [] },
    { key: "token", type: "secret", label: "Token", required: true, options: [] },
  ],
  config: { repo: "multica-ai/multica" },
  configured_secrets: ["token"],
  surfaces: [{ key: "hello", type: "issue_panel", name: "Hello", entry: "ui/main.js", platforms: [] }],
  hooks: [],
  resources: [],
  created_at: "2026-08-18T00:00:00Z",
  updated_at: "2026-08-18T00:00:00Z",
};

const PREVIEW = {
  manifest: {
    key: "com.example.hello",
    name: "Hello Panel",
    description: "A greeting panel.",
    version: "1.0.0",
    author: { name: "example" },
  },
  scopes: ["issues:read", "comments:write", "net:example.com"],
  config_schema: [],
  version_id: "version-1",
  version: "1.0.0",
  digest: "0123456789abcdef",
  installed: false,
  added_scopes: [],
};

// One published plugin with two versions: the installed one and a newer one an
// administrator has not adopted. That gap is the normal state after a publish,
// and it is what the version list has to make legible.
const PACKAGE = {
  id: "package-1",
  plugin_key: "com.example.hello",
  name: "Hello Panel",
  created_at: "2026-08-18T00:00:00Z",
  versions: [
    {
      id: "version-2",
      version: "2.0.0",
      digest: "fedcba9876543210",
      size_bytes: 2048,
      published_at: "2026-08-20T00:00:00Z",
      installed: false,
    },
    {
      id: "version-1",
      version: "1.0.0",
      digest: "0123456789abcdef",
      size_bytes: 1024,
      published_at: "2026-08-18T00:00:00Z",
      installed: true,
    },
  ],
};

describe("PluginsTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    data.role = "owner";
    data.installed.plugins = [];
    data.installed.plugins_enabled = true;
    data.packages.packages = [PACKAGE];
    mockPreview.mockResolvedValue(PREVIEW);
    mockInstall.mockResolvedValue(INSTALLATION);
    mockConfigure.mockResolvedValue(INSTALLATION);
    mockSetEnabled.mockResolvedValue(INSTALLATION);
    mockUninstall.mockResolvedValue(undefined);
    mockClearSecret.mockResolvedValue(undefined);
  });

  it("shows the scope consent screen before anything is installed", async () => {
    data.packages.packages = [{ ...PACKAGE, versions: [{ ...PACKAGE.versions[0] }] }];
    const user = userEvent.setup();
    render(<PluginsTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Review and install" }));

    // The raw scope strings are the trust model, so they must be on screen
    // verbatim alongside their plain-language meaning.
    await screen.findByText("This Plugin is asking for the following access");
    expect(screen.getByText("issues:read")).toBeInTheDocument();
    expect(screen.getByText("comments:write")).toBeInTheDocument();
    expect(screen.getByText("net:example.com")).toBeInTheDocument();
    expect(screen.getByText("Send data to example.com")).toBeInTheDocument();
    expect(mockInstall).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Grant and install" }));
    // The version reviewed is the version installed. Anything else would put
    // the consent screen back to describing one artifact while another runs.
    await waitFor(() => expect(mockInstall).toHaveBeenCalledWith({
      version_id: "version-1",
      granted_scopes: ["issues:read", "comments:write", "net:example.com"],
      config: {},
    }));
  });

  it("marks the scopes that write workspace data or leave the instance", async () => {
    data.packages.packages = [{ ...PACKAGE, versions: [{ ...PACKAGE.versions[0] }] }];
    const user = userEvent.setup();
    render(<PluginsTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Review and install" }));
    await screen.findByText("This Plugin is asking for the following access");

    // A read scope carries no marker, so the two that do are the ones that
    // stand out rather than being one line among three identical-looking ones.
    expect(screen.getByText("Changes workspace data")).toBeInTheDocument();
    expect(screen.getByText("Sends data off this instance")).toBeInTheDocument();
  });

  it("asks for required configuration on the consent screen and blocks install until it is filled", async () => {
    data.packages.packages = [{ ...PACKAGE, versions: [{ ...PACKAGE.versions[0] }] }];
    mockPreview.mockResolvedValue({ ...PREVIEW, config_schema: INSTALLATION.config_schema });
    const user = userEvent.setup();
    render(<PluginsTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "Review and install" }));
    await screen.findByText("Configuration this Plugin needs");

    // Mounting a plugin whose required credential is empty produces something
    // that fails on first use with nothing on screen explaining why.
    const install = screen.getByRole("button", { name: "Grant and install" });
    expect(install).toBeDisabled();

    await user.type(screen.getByRole("textbox"), "multica-ai/multica");
    // A secret field has no textbox role: it renders masked here for the same
    // reason it does in the installed form, and this is the assertion that the
    // consent copy of the generated form did not lose that.
    const secret = document.querySelector<HTMLInputElement>('input[type="password"]');
    expect(secret).not.toBeNull();
    expect(screen.getAllByText(/cannot be read back after saving/)).not.toHaveLength(0);
    await user.type(secret!, "sk-consent");

    await waitFor(() => expect(install).not.toBeDisabled());
    await user.click(install);

    // The secret travels with the install rather than in a second request, so
    // there is no window in which the plugin is mounted without it.
    await waitFor(() => expect(mockInstall).toHaveBeenCalledWith({
      version_id: "version-1",
      granted_scopes: ["issues:read", "comments:write", "net:example.com"],
      config: { repo: "multica-ai/multica", token: "sk-consent" },
    }));
  });

  it("explains what an upgrade does to stored data before it is approved", async () => {
    const user = userEvent.setup();
    mockPreview.mockResolvedValue({
      ...PREVIEW,
      version_id: "version-2",
      version: "2.0.0",
      installed: true,
      installed_version: "1.0.0",
    });
    render(<PluginsTab />, { wrapper: Wrapper });

    await user.click(screen.getAllByRole("button", { name: "Review and upgrade" })[0]!);

    await screen.findByText("What the upgrade does to your data");
    expect(
      screen.getByText(/Fields the new version no longer declares are removed/),
    ).toBeInTheDocument();
    expect(screen.getByText(/v1\.0\.0 and its existing grant are restored/)).toBeInTheDocument();
  });

  it("marks the running version and offers an upgrade only for the others", async () => {
    // A publish does not move an installed workspace, so "which one am I on"
    // has to be answerable on this screen or the guarantee is invisible.
    const user = userEvent.setup();
    render(<PluginsTab />, { wrapper: Wrapper });

    expect(screen.getByText("1.0.0")).toBeInTheDocument();
    expect(screen.getByText("2.0.0")).toBeInTheDocument();
    expect(screen.getByText("In use")).toBeInTheDocument();

    const upgrade = screen.getAllByRole("button", { name: "Review and upgrade" });
    expect(upgrade).toHaveLength(1);

    mockPreview.mockResolvedValue({ ...PREVIEW, version_id: "version-2", version: "2.0.0", installed: true, installed_version: "1.0.0" });
    await user.click(upgrade[0]!);
    await waitFor(() => expect(mockPreview).toHaveBeenCalledWith({ version_id: "version-2" }));
  });

  it("publishes an uploaded package", async () => {
    const user = userEvent.setup();
    mockPublish.mockResolvedValue({ ...PACKAGE, versions: [PACKAGE.versions[0]] });
    render(<PluginsTab />, { wrapper: Wrapper });

    const bundle = new File(["zip bytes"], "plugin.zip", { type: "application/zip" });
    await user.upload(screen.getByLabelText("Upload package"), bundle);
    await waitFor(() => expect(mockPublish).toHaveBeenCalledWith(bundle));
  });

  it("renders the configuration form from the manifest and never shows a stored secret", async () => {
    data.installed.plugins = [INSTALLATION];
    const user = userEvent.setup();
    render(<PluginsTab />, { wrapper: Wrapper });

    const repo = screen.getByDisplayValue("multica-ai/multica");
    expect(repo).toBeInTheDocument();

    const secret = screen.getByPlaceholderText("Saved — enter a new value to replace it");
    expect(secret).toHaveAttribute("type", "password");
    expect(secret).toHaveValue("");

    // Saving without retyping the secret must not send an empty value: that
    // would clear a stored secret as a side effect of an unrelated edit.
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(mockConfigure).toHaveBeenCalledWith({
      installationId: "installation-1",
      values: { repo: "multica-ai/multica" },
    }));

    mockConfigure.mockClear();
    await user.type(secret, "new-token");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(mockConfigure).toHaveBeenCalledWith({
      installationId: "installation-1",
      values: { repo: "multica-ai/multica", token: "new-token" },
    }));
  });

  it("disables and uninstalls an installed Plugin", async () => {
    data.installed.plugins = [INSTALLATION];
    const user = userEvent.setup();
    render(<PluginsTab />, { wrapper: Wrapper });

    await user.click(screen.getByRole("switch", { name: "Enable Plugin" }));
    await waitFor(() => expect(mockSetEnabled).toHaveBeenCalledWith({
      installationId: "installation-1",
      enabled: false,
    }));

    await user.click(screen.getByRole("button", { name: "Uninstall" }));

    // The icon opens a confirmation; it does not delete. Uninstall drops
    // storage, secrets and contributed skills in one irreversible step, so the
    // list is spelled out and the button waits for an explicit acknowledgement.
    await screen.findByText("Uninstall Hello Panel?");
    expect(screen.getByText(/Stored secrets: token/)).toBeInTheDocument();
    expect(screen.getByText(/The Skills it contributed/)).toBeInTheDocument();
    expect(mockUninstall).not.toHaveBeenCalled();

    const confirmButton = screen.getAllByRole("button", { name: "Uninstall" }).at(-1)!;
    expect(confirmButton).toBeDisabled();

    await user.click(screen.getByRole("checkbox"));
    await waitFor(() => expect(confirmButton).not.toBeDisabled());
    await user.click(confirmButton);
    await waitFor(() => expect(mockUninstall).toHaveBeenCalledWith("installation-1"));
  });

  it("lets an operator clear one stored secret while the feature is off", async () => {
    // With plugins_v1 off the configuration form is gone, so without this the
    // only way to get a leaked credential out would be removing the whole
    // installation. Names only — a stored value is never returned by any read.
    data.installed.plugins = [INSTALLATION];
    data.installed.plugins_enabled = false;
    const user = userEvent.setup();
    render(<PluginsTab />, { wrapper: Wrapper });

    expect(screen.getByText("Stored secrets")).toBeInTheDocument();
    expect(screen.getByText("token")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();

    // Two clicks, because a mis-click here deletes a credential the workspace
    // may still be running on.
    await user.click(screen.getByRole("button", { name: "Clear this secret" }));
    expect(mockClearSecret).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Confirm deletion" }));
    await waitFor(() => expect(mockClearSecret).toHaveBeenCalledWith({
      installationId: "installation-1",
      key: "token",
    }));
  });

  it("stays usable for removal after the plugins feature is turned off", () => {
    // Off stops plugin code from running, but the operator who turned it off
    // still has installations to clean up. If this screen went away with the
    // flag, their only remaining move would be editing the database by hand.
    data.installed.plugins = [INSTALLATION];
    data.installed.plugins_enabled = false;
    render(<PluginsTab />, { wrapper: Wrapper });

    expect(screen.getByText("Plugin management is turned off")).toBeInTheDocument();
    // Uninstall survives; everything that would start new plugin work does not.
    expect(screen.getByRole("button", { name: "Uninstall" })).not.toBeDisabled();
    expect(screen.queryByRole("button", { name: "Upload package" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Review and install" })).not.toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "Enable Plugin" })).toHaveAttribute("aria-disabled", "true");
  });

  it("blocks management for a non-admin member", () => {
    data.role = "member";
    data.installed.plugins = [INSTALLATION];
    render(<PluginsTab />, { wrapper: Wrapper });

    expect(screen.getByText("Read-only access")).toBeInTheDocument();
    // The whole publish-and-install section is admin-only.
    expect(screen.queryByRole("button", { name: "Upload package" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Review and install" })).not.toBeInTheDocument();
    // Base UI's Switch marks the disabled state with aria-disabled rather than
    // the native attribute, so assert what a screen reader actually sees.
    expect(screen.getByRole("switch", { name: "Enable Plugin" })).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("button", { name: "Uninstall" })).toBeDisabled();
  });

  // Listing is what puts a package on the instance directory. It is a decision
  // about discovery only, which is why the copy on screen has to say so — an
  // author who reads it as "revocable distribution" will unlist a bad release
  // and be surprised that installed workspaces keep running.
  it("lists a private package on the instance directory", async () => {
    const user = userEvent.setup();
    mockSetVisibility.mockResolvedValue({});
    render(<PluginsTab />, { wrapper: Wrapper });

    expect(screen.getByText("Private")).toBeInTheDocument();
    expect(
      screen.getByText(/Only this workspace can install this plugin/),
    ).toBeInTheDocument();
    // Nothing to withdraw while the package is private: a version can only come
    // off a directory it was never on.
    expect(screen.queryByRole("button", { name: "Withdraw" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "List on directory" }));

    await waitFor(() =>
      expect(mockSetVisibility).toHaveBeenCalledWith({
        packageId: "package-1",
        isPublic: true,
      }),
    );
  });

  it("takes a listed package back off the directory", async () => {
    const user = userEvent.setup();
    data.packages.packages = [{ ...PACKAGE, visibility: "public" }];
    mockSetVisibility.mockResolvedValue({});
    render(<PluginsTab />, { wrapper: Wrapper });

    expect(screen.getByText("Listed")).toBeInTheDocument();
    expect(
      screen.getByText(/workspaces already running a version keep running it/i),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Unlist" }));

    await waitFor(() =>
      expect(mockSetVisibility).toHaveBeenCalledWith({
        packageId: "package-1",
        isPublic: false,
      }),
    );
  });

  it("withdraws one bad version without unlisting the plugin", async () => {
    const user = userEvent.setup();
    data.packages.packages = [{ ...PACKAGE, visibility: "public" }];
    mockSetWithdrawn.mockResolvedValue({});
    render(<PluginsTab />, { wrapper: Wrapper });

    // One control per version, newest first.
    const withdrawButtons = screen.getAllByRole("button", { name: "Withdraw" });
    expect(withdrawButtons).toHaveLength(2);

    await user.click(withdrawButtons[0]!);

    await waitFor(() =>
      expect(mockSetWithdrawn).toHaveBeenCalledWith({
        versionId: "version-2",
        withdrawn: true,
      }),
    );
    expect(mockSetVisibility).not.toHaveBeenCalled();
  });

  it("marks a withdrawn version and offers to restore it", async () => {
    const user = userEvent.setup();
    data.packages.packages = [
      {
        ...PACKAGE,
        visibility: "public",
        versions: [
          { ...PACKAGE.versions[0], withdrawn_at: "2026-08-21T00:00:00Z" },
          PACKAGE.versions[1],
        ],
      },
    ];
    mockSetWithdrawn.mockResolvedValue({});
    render(<PluginsTab />, { wrapper: Wrapper });

    expect(screen.getByText("Withdrawn")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Restore" }));

    await waitFor(() =>
      expect(mockSetWithdrawn).toHaveBeenCalledWith({
        versionId: "version-2",
        withdrawn: false,
      }),
    );
  });

  it("does not let a plain member change what is listed", () => {
    data.role = "member";
    data.packages.packages = [{ ...PACKAGE, visibility: "public" }];
    render(<PluginsTab />, { wrapper: Wrapper });

    expect(screen.queryByRole("button", { name: "Unlist" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Withdraw" })).toBeNull();
  });
});
