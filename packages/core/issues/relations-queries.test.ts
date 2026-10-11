// @vitest-environment node
import { describe, expect, it, vi } from "vitest";

import type { IssueRelationsResponse } from "../types";

const sample: IssueRelationsResponse = {
  issue_id: "11111111-1111-4111-8111-111111111111",
  identifier: "MUL-1",
  revision: 7,
  parent: null,
  blocks: [
    {
      id: "22222222-2222-4222-8222-222222222222",
      identifier: "MUL-2",
      title: "Blocked issue",
      status: "todo",
    },
  ],
  blocked_by: [],
  relates_to: [],
  supersedes: [],
  superseded_by: [],
};

vi.mock("../api", () => ({
  api: { getIssueRelations: vi.fn(() => Promise.resolve(sample)) },
  ApiError: class ApiError extends Error {},
}));

import { api } from "../api";
import {
  EMPTY_ISSUE_RELATIONS,
  IssueRelationsResponseSchema,
} from "../api/schemas";
import { issueKeys, issueRelationsOptions } from "./queries";

describe("issueRelationsOptions", () => {
  it("keys per workspace + issue under the shared issues prefix", () => {
    expect(issueKeys.relations("ws-1", "issue-1")).toEqual([
      "issues",
      "ws-1",
      "relations",
      "issue-1",
    ]);
  });

  it("always refetches on mount — relation writes emit no WS event, so a cached snapshot never heals on its own", () => {
    expect(issueRelationsOptions("ws-1", "issue-1").refetchOnMount).toBe(
      "always",
    );
  });

  it("queryFn pulls the relations payload through the API client", async () => {
    const opts = issueRelationsOptions("ws-1", "issue-1");
    await expect(opts.queryFn?.(undefined as never)).resolves.toBe(sample);
    expect(api.getIssueRelations).toHaveBeenCalledWith("issue-1");
  });
});

describe("IssueRelationsResponseSchema", () => {
  it("parses the server shape (parent + five edge views)", () => {
    const parsed = IssueRelationsResponseSchema.parse(sample);
    expect(parsed.blocks).toHaveLength(1);
    expect(parsed.blocks[0]?.identifier).toBe("MUL-2");
  });

  it("defaults missing groups so a drifted payload degrades to 'no relations' instead of tripping the fallback", () => {
    const parsed = IssueRelationsResponseSchema.parse({
      issue_id: "issue-1",
      identifier: "MUL-1",
      revision: 1,
    });
    expect(parsed.parent).toBeNull();
    expect(parsed.blocks).toEqual([]);
    expect(parsed.blocked_by).toEqual([]);
    expect(parsed.superseded_by).toEqual([]);
  });

  it("EMPTY_ISSUE_RELATIONS satisfies the response type as the parse fallback", () => {
    expect(IssueRelationsResponseSchema.parse(EMPTY_ISSUE_RELATIONS)).toEqual(
      EMPTY_ISSUE_RELATIONS,
    );
  });
});
