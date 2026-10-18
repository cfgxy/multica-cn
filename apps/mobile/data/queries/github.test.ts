// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";

import { pullRequestLookupOptions } from "./github";

// queryFn delegates to the fetch client; mock it so the Node test never
// loads RN modules and can assert the exact workspace-scoped call shape.
// RUYI-635 返工：wsId 必须作为首参进入端点路径 —— 漏传时请求落在不存在的
// 顶层路由上，被「404→null」降级契约吞掉，PR 引用静默降级为纯文本。
const { lookupPullRequest } = vi.hoisted(() => ({ lookupPullRequest: vi.fn() }));
vi.mock("@/data/api", () => ({ api: { lookupPullRequest } }));

describe("pullRequestLookupOptions workspace threading (RUYI-635 返工)", () => {
  beforeEach(() => {
    lookupPullRequest.mockReset();
    lookupPullRequest.mockResolvedValue(null);
  });

  it("threads wsId as the workspaceId into the lookup request", async () => {
    const options = pullRequestLookupOptions(
      "ws-1",
      "cfgxy",
      "multica-cn",
      350,
    );
    await options.queryFn?.({ signal: new AbortController().signal } as never);

    expect(lookupPullRequest).toHaveBeenCalledWith(
      "ws-1",
      "cfgxy",
      "multica-cn",
      350,
      { signal: expect.any(AbortSignal) },
    );
  });
});
