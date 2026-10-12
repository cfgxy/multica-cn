// @vitest-environment node
import { describe, expect, it } from "vitest";
import { bindingEligibility } from "./eligibility";
import type { Computer, ComputerBinding } from "./schema";

const machine: Computer = { id: "m", name: "Host", host: "example.com", port: 22, ssh_user: "operator", enabled: true };
const binding: ComputerBinding = {
  id: "b", computer_id: "m", workspace_id: "old", username: "alice",
  state: "removed", last_error: "", verified: true,
  account_state: "present", operation_busy: false, workspace_name: "", workspace_access: "unknown", latest_operation: null,
};

describe("Linux User workspace eligibility", () => {
  it("allows only detached or removed verified accounts to move between workspaces", () => {
    expect(bindingEligibility(binding, "new", machine)).toBe("available");
    expect(bindingEligibility({ ...binding, state: "detached", account_state: "missing" }, "new", machine)).toBe("available");
    expect(bindingEligibility({ ...binding, state: "failed" }, "new", machine)).toBe("other_workspace");
    expect(bindingEligibility({ ...binding, state: "ready" }, "new", machine)).toBe("other_workspace");
  });

  it("keeps unverified, busy, unavailable and already connected accounts out of the chooser", () => {
    expect(bindingEligibility({ ...binding, verified: false }, "new", machine)).toBe("unverified");
    expect(bindingEligibility({ ...binding, operation_busy: true }, "new", machine)).toBe("busy");
    expect(bindingEligibility({ ...binding, state: "interrupted" }, "new", machine)).toBe("recovery_required");
    expect(bindingEligibility(binding, "new", { ...machine, enabled: false })).toBe("computer_unavailable");
    expect(bindingEligibility({ ...binding, account_state: "unavailable" }, "new", machine)).toBe("account_unavailable");
    expect(bindingEligibility({ ...binding, state: "ready", workspace_id: "new" }, "new", machine)).toBe("current_workspace");
  });
});
