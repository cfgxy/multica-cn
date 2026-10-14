// @vitest-environment node
import { expect, it } from "vitest";
import { AgentRuntimeSchema } from "../api/schemas";
import { defaultAgentRuntime } from "./default-selection";
const runtime = (id: string) =>
  AgentRuntimeSchema.parse({
    id,
    workspace_id: "ws",
    name: "Claude (tensor)",
    provider: "claude",
    status: "online",
    owner_id: "me",
  });
const source = {
  binding_id: "binding",
  linux_user: "mas_glite",
  host: "tensor",
  preferred: true,
};
it("prefers the current workspace's Linux user over an earlier same-host runtime", () => {
  const old = runtime("tiger");
  const managed = { ...runtime("mas_glite"), execution_source: source };
  expect(defaultAgentRuntime([old, managed], "me")?.id).toBe("mas_glite");
  expect(
    defaultAgentRuntime(
      [old, { ...managed, execution_source: { ...source, preferred: false } }],
      "me",
    )?.id,
  ).toBe("tiger");
});
it("requires an explicit choice when the associated user is offline or there are multiple accounts", () => {
  const managed = { ...runtime("managed"), execution_source: source };
  expect(
    defaultAgentRuntime(
      [runtime("tiger"), { ...managed, status: "offline" }],
      "me",
    ),
  ).toBeNull();
  expect(
    defaultAgentRuntime(
      [
        managed,
        {
          ...managed,
          id: "other",
          execution_source: { ...source, binding_id: "other" },
        },
      ],
      "me",
    ),
  ).toBeNull();
});
it("waits for viewer identity and respects owner and visibility", () => {
  expect(defaultAgentRuntime([runtime("old")], null)).toBeNull();
  expect(
    defaultAgentRuntime(
      [
        { ...runtime("other"), owner_id: "another", execution_source: source },
        runtime("mine"),
      ],
      "me",
    )?.id,
  ).toBe("mine");
  expect(
    defaultAgentRuntime([{ ...runtime("other"), owner_id: null }], "me"),
  ).toBeNull();
});
