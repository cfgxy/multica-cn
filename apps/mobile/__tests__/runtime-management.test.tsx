import type { PropsWithChildren } from "react";
import { act, renderHook } from "@testing-library/react-native";
import {
  QueryClient,
  QueryClientProvider,
  onlineManager,
} from "@tanstack/react-query";

const mockDeleteRuntime = jest.fn();
const mockUnbindAgentsAndDeleteRuntime = jest.fn();

jest.mock("@/data/api", () => ({
  api: {
    deleteRuntime: mockDeleteRuntime,
    unbindAgentsAndDeleteRuntime: mockUnbindAgentsAndDeleteRuntime,
    updateRuntime: jest.fn(),
  },
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (state: { currentWorkspaceId: string }) => unknown) =>
    selector({ currentWorkspaceId: "workspace-1" }),
}));

const {
  useDeleteRuntime,
  useUnbindAgentsAndDeleteRuntime,
} = jest.requireActual<typeof import("@/data/mutations/runtime-management")>(
  "@/data/mutations/runtime-management",
);

function createWrapper() {
  const client = new QueryClient({
    defaultOptions: { mutations: { gcTime: 0, retry: false } },
  });

  function QueryWrapper({ children }: PropsWithChildren) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  }

  return { client, wrapper: QueryWrapper };
}

describe("runtime removal mutations", () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockDeleteRuntime.mockResolvedValue(undefined);
    mockUnbindAgentsAndDeleteRuntime.mockResolvedValue(undefined);
    onlineManager.setOnline(false);
  });

  afterEach(() => {
    onlineManager.setOnline(true);
  });

  it("executes strict runtime removal while the device is offline", async () => {
    const harness = createWrapper();
    const { result, unmount } = await renderHook(() => useDeleteRuntime(), {
      wrapper: harness.wrapper,
    });

    await act(async () => {
      await result.current.mutateAsync("runtime-1");
    });

    expect(mockDeleteRuntime).toHaveBeenCalledWith("runtime-1");
    await unmount();
    harness.client.clear();
  });

  it("executes bound-agent runtime removal while the device is offline", async () => {
    const harness = createWrapper();
    const { result, unmount } = await renderHook(() => useUnbindAgentsAndDeleteRuntime(), {
      wrapper: harness.wrapper,
    });

    const input = { runtimeId: "runtime-1", expectedActiveAgentIds: ["agent-1"] };
    await act(async () => {
      await result.current.mutateAsync(input);
    });

    expect(mockUnbindAgentsAndDeleteRuntime).toHaveBeenCalledWith(
      "runtime-1",
      ["agent-1"],
    );
    await unmount();
    harness.client.clear();
  });
});
