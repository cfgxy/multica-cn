// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  apiSetToken: vi.fn(),
  clearServerMemory: vi.fn(),
  clearToken: vi.fn(),
  clearQuickCreateActorMemory: vi.fn(),
  invalidateNewIssueSubmissionContext: vi.fn(),
}));

vi.mock("./api", () => ({
  api: { setToken: mocks.apiSetToken },
  ApiError: class ApiError extends Error {},
}));

vi.mock("./secure-storage", () => ({
  clearToken: mocks.clearToken,
  getToken: vi.fn(),
  migrateLegacySession: vi.fn(),
  setToken: vi.fn(),
}));

vi.mock("./workspace-store", () => ({
  useWorkspaceStore: { getState: () => ({ restoreSlug: vi.fn() }) },
}));

vi.mock("./server-store", () => ({
  useServerStore: { getState: () => ({ activeServerId: "srv-1" }) },
}));

vi.mock("./stores/new-issue-draft-store", () => ({
  clearServerMemory: mocks.clearServerMemory,
  invalidateNewIssueSubmissionContext: mocks.invalidateNewIssueSubmissionContext,
}));

vi.mock("./stores/quick-create-prefs-store", () => ({
  clearQuickCreateActorMemory: mocks.clearQuickCreateActorMemory,
}));

import { useAuthStore } from "./auth-store";

beforeEach(() => {
  vi.clearAllMocks();
  mocks.clearToken.mockResolvedValue(undefined);
  mocks.clearQuickCreateActorMemory.mockResolvedValue(undefined);
  useAuthStore.setState({ user: null, isLoading: false });
});

describe("logout", () => {
  it("clears the credential and API token when quick-create preference cleanup fails", async () => {
    mocks.clearQuickCreateActorMemory.mockRejectedValueOnce(
      new Error("AsyncStorage write failed"),
    );

    await expect(useAuthStore.getState().logout()).resolves.toBeUndefined();

    expect(mocks.clearToken).toHaveBeenCalledWith("srv-1");
    expect(mocks.apiSetToken).toHaveBeenCalledWith(null);
  });
});
