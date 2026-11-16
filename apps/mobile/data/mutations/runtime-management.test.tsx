import React from "react";
import { act, renderHook } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const mockRemoveRuntimeFromList = jest.fn();

jest.mock("@/data/api", () => ({
  api: {
    removeRuntimeFromList: (...args: unknown[]) => mockRemoveRuntimeFromList(...args),
  },
}));

jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (selector: (state: { currentWorkspaceId: string }) => unknown) =>
    selector({ currentWorkspaceId: "workspace-1" }),
}));

import { useRemoveRuntimeFromList } from "@/data/mutations/runtime-management";
import { runtimeKeys } from "@/data/queries/runtimes";

function wrapperFor(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
  };
}

describe("useRemoveRuntimeFromList", () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockRemoveRuntimeFromList.mockResolvedValue(undefined);
  });

  it("refreshes only the runtime projection after removal", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const invalidateSpy = jest.spyOn(queryClient, "invalidateQueries");
    const { result, unmount } = await renderHook(() => useRemoveRuntimeFromList(), {
      wrapper: wrapperFor(queryClient),
    });

    await act(async () => {
      await result.current.mutateAsync("runtime-1");
    });

    expect(mockRemoveRuntimeFromList).toHaveBeenCalledWith("runtime-1");
    expect(invalidateSpy).toHaveBeenCalledTimes(1);
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: runtimeKeys.all("workspace-1"),
    });

    await unmount();
    queryClient.clear();
    queryClient.unmount();
  });
});
