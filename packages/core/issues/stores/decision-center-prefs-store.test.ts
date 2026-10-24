// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";
import { decisionCenterPrefsStore } from "./decision-center-prefs-store";

// RUYI-864: the Decision Center owns one single-value status window —
// "all" or exactly one card status, mapping 1:1 onto the server's
// `status` query param. The v1 `viewMode`/`hiddenStatuses` payload is
// forgotten at the storage boundary (version 2 reopens on "all").

function reset() {
  decisionCenterPrefsStore.setState({ statusFilter: "all" });
}

beforeEach(reset);

describe("decisionCenterPrefsStore", () => {
  it("starts on the all window", () => {
    expect(decisionCenterPrefsStore.getState().statusFilter).toBe("all");
  });

  it("sets a single status window", () => {
    decisionCenterPrefsStore.getState().setStatusFilter("open");
    expect(decisionCenterPrefsStore.getState().statusFilter).toBe("open");

    decisionCenterPrefsStore.getState().setStatusFilter("answered");
    expect(decisionCenterPrefsStore.getState().statusFilter).toBe("answered");
  });

  it("returns to the all window", () => {
    decisionCenterPrefsStore.getState().setStatusFilter("cancelled");
    decisionCenterPrefsStore.getState().setStatusFilter("all");
    expect(decisionCenterPrefsStore.getState().statusFilter).toBe("all");
  });
});
