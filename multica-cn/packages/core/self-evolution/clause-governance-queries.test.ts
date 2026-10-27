// @vitest-environment node

import { describe, expect, it } from "vitest";
import {
  clauseGovernanceDetailOptions,
  clauseGovernanceOverviewOptions,
  clauseGovernanceReconciliationOptions,
} from "./clause-governance-queries";

describe("clause governance query options", () => {
  it("scopes every query key by workspace and the selected days", () => {
    expect(clauseGovernanceOverviewOptions("ws-1", 90).queryKey).toEqual([
      "clause-governance", "overview", "ws-1", 90,
    ]);
    expect(clauseGovernanceDetailOptions("ws-1", "clause-1", 7).queryKey).toEqual([
      "clause-governance", "detail", "ws-1", "clause-1", 7,
    ]);
    expect(clauseGovernanceReconciliationOptions("ws-1", 365).queryKey).toEqual([
      "clause-governance", "reconciliation", "ws-1", 365,
    ]);
  });

  it("does not enable workspace or clause reads without their identifiers", () => {
    expect(clauseGovernanceOverviewOptions("", 30).enabled).toBe(false);
    expect(clauseGovernanceDetailOptions("ws-1", "", 30).enabled).toBe(false);
    expect(clauseGovernanceReconciliationOptions("", 30).enabled).toBe(false);
  });
});
