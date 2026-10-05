// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";

import { agentListOptions } from "./agents";

// agentListOptions' queryFn delegates to the fetch client; mock it so the
// Node test never loads RN modules and can assert the exact request opts.
const { listAgents } = vi.hoisted(() => ({ listAgents: vi.fn() }));
vi.mock("@/data/api", () => ({ api: { listAgents } }));

describe("agentListOptions workspace pinning (RUYI-463 P1)", () => {
  beforeEach(() => {
    listAgents.mockReset();
    listAgents.mockResolvedValue([]);
  });

  // share-target fetches agents for the PICKED workspace; the slug must be
  // threaded into the request itself, not left to the fetch layer's mirror
  // injection (which is empty for a fresh user or points at another space).
  it("threads an explicit workspaceSlug into the list request", async () => {
    const options = agentListOptions("ws-1", { workspaceSlug: "picked-ws" });
    await options.queryFn?.({ signal: new AbortController().signal } as never);

    expect(listAgents).toHaveBeenCalledWith({
      signal: expect.any(AbortSignal),
      workspaceSlug: "picked-ws",
    });
  });

  it("keeps in-shell callers on the mirror-injected context", async () => {
    const options = agentListOptions("ws-1");
    await options.queryFn?.({ signal: new AbortController().signal } as never);

    expect(listAgents).toHaveBeenCalledWith({
      signal: expect.any(AbortSignal),
    });
    expect(listAgents.mock.calls[0]?.[0]?.workspaceSlug).toBeUndefined();
  });
});
