// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryObserver } from "@tanstack/react-query";

import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import { issuePullRequestsOptions } from "./queries";

describe("issuePullRequestsOptions", () => {
  afterEach(() => {
    setApiInstance(undefined as unknown as ApiClient);
  });

  it("keeps a failed PR list request idle across remounts until an explicit retry", async () => {
    const listIssuePullRequests = vi
      .fn<(issueId: string) => Promise<{ pull_requests: [] }>>()
      .mockRejectedValueOnce(new Error("server returned 500"))
      .mockResolvedValueOnce({ pull_requests: [] });
    setApiInstance({ listIssuePullRequests } as unknown as ApiClient);

    const qc = new QueryClient({
      defaultOptions: { queries: { staleTime: Infinity } },
    });
    const options = issuePullRequestsOptions("issue-1");
    const observer1 = new QueryObserver(qc, options);
    const unsubscribe1 = observer1.subscribe(() => {});

    await vi.waitFor(() => {
      expect(observer1.getCurrentResult().status).toBe("error");
    });
    expect(listIssuePullRequests).toHaveBeenCalledTimes(1);

    unsubscribe1();
    const observer2 = new QueryObserver(qc, options);
    const unsubscribe2 = observer2.subscribe(() => {});

    expect(observer2.getCurrentResult().status).toBe("error");
    expect(observer2.getCurrentResult().fetchStatus).toBe("idle");
    expect(listIssuePullRequests).toHaveBeenCalledTimes(1);

    await observer2.refetch();
    expect(listIssuePullRequests).toHaveBeenCalledTimes(2);
    expect(observer2.getCurrentResult().data).toEqual({ pull_requests: [] });

    unsubscribe2();
    qc.clear();
  });
});
