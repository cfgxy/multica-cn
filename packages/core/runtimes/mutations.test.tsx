/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import { runtimeKeys } from "./queries";
import { useRemoveRuntimeFromList } from "./mutations";

function createWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

describe("useRemoveRuntimeFromList", () => {
  let queryClient: QueryClient;
  let removeRuntimeFromList: ReturnType<typeof vi.fn<(runtimeId: string) => Promise<void>>>;

  beforeEach(() => {
    queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    removeRuntimeFromList = vi.fn().mockResolvedValue(undefined);
    setApiInstance({ removeRuntimeFromList } as unknown as ApiClient);
  });

  afterEach(() => {
    queryClient.clear();
    vi.restoreAllMocks();
  });

  it("refreshes only Runtime queries after the management entry is removed", async () => {
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    const { result } = renderHook(() => useRemoveRuntimeFromList("ws-1"), {
      wrapper: createWrapper(queryClient),
    });

    await act(async () => {
      await result.current.mutateAsync("runtime-1");
    });

    expect(removeRuntimeFromList).toHaveBeenCalledWith("runtime-1");
    expect(invalidate).toHaveBeenCalledTimes(1);
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: runtimeKeys.all("ws-1"),
    });
  });
});
