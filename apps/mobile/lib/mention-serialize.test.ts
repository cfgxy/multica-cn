// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  removeMentionChip,
  serializeMentionChips,
} from "@/lib/mention-serialize";

describe("serializeMentionChips", () => {
  it("prepends selected mention links in selection order", () => {
    expect(
      serializeMentionChips("Fix the upload flow", [
        { type: "member", id: "user-1", name: "Bohan" },
        { type: "issue", id: "issue-1", name: "RUYI-133" },
      ]),
    ).toBe(
      "[@Bohan](mention://member/user-1) [RUYI-133](mention://issue/issue-1) Fix the upload flow",
    );
  });

  it("leaves plain text unchanged when no mention chips exist", () => {
    expect(serializeMentionChips("  Keep spacing  ", [])).toBe(
      "  Keep spacing  ",
    );
  });

  it("removes exactly one chip before serializing the remaining mentions", () => {
    const markers = [
      { type: "member" as const, id: "user-1", name: "Bohan" },
      { type: "issue" as const, id: "issue-1", name: "RUYI-133" },
    ];

    const remaining = removeMentionChip(markers, "member", "user-1");

    expect(serializeMentionChips("Fix the upload flow", remaining)).toBe(
      "[RUYI-133](mention://issue/issue-1) Fix the upload flow",
    );
  });
});
