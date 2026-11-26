import { beforeEach, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import en from "../locales/en/settings.json";
import { RuntimeCachePanel } from "./runtime-cache-panel";
const api = vi.hoisted(() => ({ getRuntimeCache: vi.fn(), refreshRuntimeCache: vi.fn(), clearRuntimeCache: vi.fn() }));
vi.mock("@multica/core/api", () => ({ api }));
const data = { bytes: 100, limit_bytes: 1000, downloads: 0, entries: [{ id: "file", url: "https://registry.npmjs.org/tool/-/tool.tgz", size: 100, cached_at: "2026-09-28T00:00:00Z" }], catalog: [{ id: "codex", display_name: "Codex", latest_version: "1.2.3", latest_version_state: "ready" }] };
function mount() {
 const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
 render(<I18nProvider locale="en" resources={{ en: { settings: en } }}><QueryClientProvider client={client}><RuntimeCachePanel userId="admin" /></QueryClientProvider></I18nProvider>);
}
beforeEach(() => { vi.clearAllMocks(); api.getRuntimeCache.mockResolvedValue(data); api.refreshRuntimeCache.mockResolvedValue(undefined); api.clearRuntimeCache.mockResolvedValue(undefined); });
it("shows shared cached files and refreshes upstream versions", async () => {
 mount();
 expect(await screen.findByText("1.2.3")).toBeInTheDocument();
 expect(screen.getByText(data.entries[0]!.url)).toBeInTheDocument();
 await userEvent.setup().click(screen.getByRole("button", { name: "Refresh versions" }));
 await waitFor(() => expect(api.refreshRuntimeCache).toHaveBeenCalledOnce());
});
it("awaits cache deletion and keeps the confirmation open on failure", async () => {
 api.clearRuntimeCache.mockRejectedValue(new Error("Installations are active"));
 mount(); const user = userEvent.setup();
 await screen.findByText("1.2.3");
 await user.click(screen.getByRole("button", { name: "Clear cache" }));
 expect(api.clearRuntimeCache).not.toHaveBeenCalled();
 await user.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Clear cache" }));
 expect(await within(screen.getByRole("alertdialog")).findByRole("alert")).toHaveTextContent("Installations are active");
 api.clearRuntimeCache.mockResolvedValue(undefined);
 await user.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Clear cache" }));
 await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
});
it("disables clearing while downloads are running", async () => {
 api.getRuntimeCache.mockResolvedValue({ ...data, downloads: 1 }); mount();
 await screen.findByText("1.2.3");
 expect(screen.getByRole("button", { name: "Clear cache" })).toBeDisabled();
});
