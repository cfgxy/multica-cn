// @vitest-environment node
import { expect, it } from "vitest";
import { ComputerBindingSchema } from "./schema";
import {
  bindingPrimaryAction,
  bindingSummary,
  bindingWorkspaceLabel,
} from "./summary";
const base = ComputerBindingSchema.parse({
  id: "b",
  computer_id: "c",
  workspace_id: "w",
  username: "alice",
  state: "ready",
  last_error: "",
  verified: true,
  account_state: "present",
});
it.each([
  [{ state: "pending" }, "pending"],
  [{ state: "removed" }, "removed"],
  [{ state: "detached" }, "detached"],
  [{ state: "failed", account_state: "missing" }, "missing"],
  [{ verified: false }, "unverified"],
  [{ state: "pending", operation_busy: true }, "running"],
  [{ state: "interrupted" }, "interrupted"],
  [{ state: "new" }, "unknown"],
])("summarizes account facts %j", (input, expected) =>
  expect(bindingSummary(ComputerBindingSchema.parse({ ...base, ...input }))).toBe(expected),
);
it("keeps runtime failure separate but prioritizes unconfirmed interruption", () => {
  const latest_operation = {
    id: "op",
    kind: "runtime_install",
    state: "failed" as const,
    error_code: "remote_failed",
    finished_at: null,
  };
  expect(bindingSummary({ ...base, latest_operation })).toBe("ready");
  expect(
    bindingSummary({
      ...base,
      latest_operation: { ...latest_operation, state: "interrupted" },
    }),
  ).toBe("interrupted");
  expect(
    bindingSummary({
      ...base,
      latest_operation: {
        ...latest_operation,
        state: "interrupted",
        finished_at: "now",
      },
    }),
  ).toBe("ready");
});
it("picks one next step from the account summary", () => {
  expect(bindingPrimaryAction({ ...base, state: "pending" })).toBe("credentials");
  expect(bindingPrimaryAction({ ...base, state: "ready" })).toBeNull();
  expect(bindingPrimaryAction({ ...base, state: "removed" })).toBe("create_account");
  expect(bindingPrimaryAction({ ...base, state: "running" })).toBe("operations");
  expect(bindingPrimaryAction({ ...base, account_state: "unavailable" })).toBe(
    "operations",
  );
});
it("never renders a workspace UUID or an inaccessible name", () => {
  expect(bindingWorkspaceLabel(base)).toEqual({ name: null, key: "unknown" });
  expect(
    bindingWorkspaceLabel({
      ...base,
      workspace_access: "unavailable",
      workspace_name: "Private name",
    }),
  ).toEqual({ name: null, key: "unavailable" });
  expect(
    bindingWorkspaceLabel({
      ...base,
      state: "detached",
      workspace_access: "none",
    }),
  ).toEqual({ name: null, key: "deleted" });
});
it("tolerates missing and malformed new summaries without dropping the binding", () => {
  expect(
    ComputerBindingSchema.parse({
      ...base,
      workspace_name: 42,
      workspace_access: "future",
      latest_operation: { id: 42 },
    }),
  ).toMatchObject({
    id: "b",
    workspace_name: "",
    workspace_access: "unknown",
    latest_operation: null,
  });
  expect(
    ComputerBindingSchema.parse({
      ...base,
      latest_operation: { id: "op", kind: "future", state: "future" },
    }).latest_operation?.state,
  ).toBe("unknown");
});
