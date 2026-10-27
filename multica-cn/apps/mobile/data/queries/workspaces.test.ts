// @vitest-environment node
import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";

vi.mock("@/data/api", () => ({
  api: { listWorkspaces: vi.fn() },
}));

import { freshWorkspaceListOptions } from "./workspaces";

describe("freshWorkspaceListOptions", () => {
  it("marks the workspace list stale so confirmation rechecks membership", async () => {
    const qc = new QueryClient();
    let calls = 0;
    const options = {
      ...freshWorkspaceListOptions(),
      queryFn: async () => {
        calls += 1;
        return [];
      },
    };

    await qc.fetchQuery(options);
    await qc.fetchQuery(options);

    expect(calls).toBe(2);
  });
});
