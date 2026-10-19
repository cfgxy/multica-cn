import { fireEvent, render, screen, within } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import { RESOURCES } from "@multica/views/locales";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { EnvironmentManagerTab } from "./environment-manager-tab";

const inventory = vi.fn();
const previewCache = vi.fn();
const cleanCache = vi.fn();
const archives = vi.fn();
const previewArchive = vi.fn();
const archive = vi.fn();
const restoreArchive = vi.fn();

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

function mount() {
  return render(
    <I18nProvider locale="zh-Hans" resources={RESOURCES}>
      <EnvironmentManagerTab />
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  inventory.mockResolvedValue([
    {
      environmentId: "a".repeat(64), taskId: "task-1", issueId: "RUYI-1",
      workspaceShort: "dev", taskShort: "RUYI-1", parentStatus: "done",
      sizeBytes: 2048, ageSeconds: 100, active: false,
      protectionReason: "", retentionReason: "",
    },
    {
      environmentId: "b".repeat(64), taskId: "task-2", issueId: "RUYI-2",
      workspaceShort: "dev", taskShort: "RUYI-2", parentStatus: "running",
      sizeBytes: 4096, ageSeconds: 10, active: true,
      protectionReason: "active", retentionReason: "",
    },
  ]);
  archives.mockResolvedValue([]);
  previewCache.mockResolvedValue([{
    environmentId: "a".repeat(64), revision: "c".repeat(64),
    sizeBytes: 1024, candidates: 2, reason: "",
  }]);
  cleanCache.mockResolvedValue([{
    environmentId: "a".repeat(64), revision: "c".repeat(64), sizeBytes: 1024,
    candidates: 2, reason: "", removedBytes: 1024, removedCount: 2,
  }]);
  previewArchive.mockResolvedValue([]);
  archive.mockResolvedValue([]);
  restoreArchive.mockResolvedValue([]);
  Object.defineProperty(window, "daemonAPI", {
    configurable: true,
    value: { environments: { inventory, previewCache, cleanCache, archives, previewArchive, archive, restoreArchive } },
  });
});

describe("EnvironmentManagerTab", () => {
  it("shows managed environments and keeps active rows protected", async () => {
    mount();
    const row = await screen.findByText("RUYI-2");
    const container = row.closest("div.grid");
    expect(container).not.toBeNull();
    expect(within(container as HTMLElement).getByRole("checkbox")).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByText("受保护")).toBeInTheDocument();
  });

  it("previews selected caches and requires confirmation before cleaning", async () => {
    mount();
    await screen.findByText("RUYI-1");
    const row = screen.getByText("RUYI-1").closest("div.grid");
    fireEvent.click(within(row as HTMLElement).getByRole("checkbox"));
    fireEvent.click(screen.getByRole("button", { name: "预览缓存" }));

    expect(await screen.findByText(/缓存预览：1 个环境/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "清理所选缓存" }));
    expect(cleanCache).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "确认" }));
    expect(cleanCache).toHaveBeenCalledWith([{ environmentId: "a".repeat(64), revision: "c".repeat(64) }]);
  });
});
