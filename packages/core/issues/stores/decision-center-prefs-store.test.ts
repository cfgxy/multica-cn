// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";
import {
  decisionCenterPrefsStore,
  type DecisionCenterViewMode,
} from "./decision-center-prefs-store";

// RUYI-547: the Decision Center's display prefs (view mode + which decision
// statuses are filtered out) persist like the issues surfaces' view state —
// same workspace-aware storage pattern, own storage key.

function reset() {
  decisionCenterPrefsStore.setState({
    viewMode: "list",
    hiddenStatuses: [],
  });
}

beforeEach(reset);

describe("decisionCenterPrefsStore", () => {
  it("starts on the list view with every status visible", () => {
    const s = decisionCenterPrefsStore.getState();
    expect(s.viewMode).toBe<DecisionCenterViewMode>("list");
    expect(s.hiddenStatuses).toEqual([]);
  });

  it("switches the view mode between list and board", () => {
    decisionCenterPrefsStore.getState().setViewMode("board");
    expect(decisionCenterPrefsStore.getState().viewMode).toBe("board");

    decisionCenterPrefsStore.getState().setViewMode("list");
    expect(decisionCenterPrefsStore.getState().viewMode).toBe("list");
  });

  it("toggles one status hidden without touching the others", () => {
    const { toggleStatusHidden } = decisionCenterPrefsStore.getState();

    toggleStatusHidden("answered");
    expect(decisionCenterPrefsStore.getState().hiddenStatuses).toEqual(["answered"]);

    toggleStatusHidden("cancelled");
    expect(decisionCenterPrefsStore.getState().hiddenStatuses).toEqual([
      "answered",
      "cancelled",
    ]);

    toggleStatusHidden("answered");
    expect(decisionCenterPrefsStore.getState().hiddenStatuses).toEqual(["cancelled"]);
  });

  it("toggling the same status twice returns it to visible", () => {
    const { toggleStatusHidden } = decisionCenterPrefsStore.getState();

    toggleStatusHidden("open");
    expect(decisionCenterPrefsStore.getState().hiddenStatuses).toEqual(["open"]);

    toggleStatusHidden("open");
    expect(decisionCenterPrefsStore.getState().hiddenStatuses).toEqual([]);
  });

  it("restores every status at once", () => {
    const s = decisionCenterPrefsStore.getState();
    s.toggleStatusHidden("open");
    s.toggleStatusHidden("answered");

    s.showAllStatuses();
    expect(decisionCenterPrefsStore.getState().hiddenStatuses).toEqual([]);
  });
});
