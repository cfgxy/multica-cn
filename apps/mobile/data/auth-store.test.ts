// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  apiSetToken: vi.fn(),
  apiSendCode: vi.fn(),
  apiVerifyCode: vi.fn(),
  clearServerMemory: vi.fn(),
  clearToken: vi.fn(),
  clearQuickCreateActorMemory: vi.fn(),
  invalidateNewIssueSubmissionContext: vi.fn(),
}));

vi.mock("./api", () => ({
  api: {
    setToken: mocks.apiSetToken,
    sendCode: mocks.apiSendCode,
    verifyCode: mocks.apiVerifyCode,
  },
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

// RUYI-568：登录屏的取消语义依赖 signal 从 store 一路透传到 api 客户端——
// store 是薄透传层，这里锁定签名与转发形态，屏幕层的取消行为由 jest 组件
// lane（auth-login-screen.test.tsx）覆盖。
describe("login chain signal passthrough (RUYI-568)", () => {
  it("sendCode forwards the caller abort signal to the api client", async () => {
    mocks.apiSendCode.mockResolvedValue(undefined);
    const controller = new AbortController();

    await useAuthStore
      .getState()
      .sendCode("user@test.local", { signal: controller.signal });

    expect(mocks.apiSendCode).toHaveBeenCalledWith("user@test.local", {
      signal: controller.signal,
    });
  });

  it("verifyCode forwards the caller abort signal to the api client", async () => {
    mocks.apiVerifyCode.mockResolvedValue({
      token: "token-1",
      user: { id: "user-1" },
    });
    const controller = new AbortController();

    await useAuthStore
      .getState()
      .verifyCode("user@test.local", "123456", { signal: controller.signal });

    expect(mocks.apiVerifyCode).toHaveBeenCalledWith(
      "user@test.local",
      "123456",
      { signal: controller.signal },
    );
  });
});
