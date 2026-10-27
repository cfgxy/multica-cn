// @vitest-environment node
import { describe, expect, it } from "vitest";

/**
 * Pure-function tests for the related-PR list (RUYI-43).
 *
 * Mobile's PR row mirrors the web sidebar's `PullRequestRow`
 * (packages/views/issues/components/pull-request-list.tsx): state icon +
 * title + `owner/repo#N · <state> · @author` subtitle. The three pieces
 * pinned here:
 *
 *   - `pullRequestStateLabelKey` — maps the four known PR states onto the
 *     shared `issues:detail.pull_request_state_*` i18n keys web already
 *     uses (data identity: same state vocabulary). Unknown states → null
 *     so the caller renders the raw server value instead of guessing
 *     ("API Response Compatibility": never silently drop a category).
 *   - `pullRequestStateVisual` — mobile's icon + theme token per state,
 *     derived from `derivePullRequestStateVisual` in core (RUYI-635 增量:
 *     ONE state→semantics derivation shared with web and the reference
 *     tail; this module only maps the spec onto mobile glyphs/tokens).
 *     Hue mapping is token-based (success / muted / violet / destructive),
 *     not web's literal hex — mobile theming diverges by design, semantics
 *     must not (open=growing, merged=integrated, closed=terminated).
 *     Merged rides the new `violet` token (was info blue): the design
 *     review unified both ends on violet, GitHub's merged identity.
 *     `dimmed` mirrors web's `opacity-80` draft row.
 *   - `formatPullRequestSubtitle` — exact subtitle composition parity
 *     with the web row, including the conditional `@author` segment.
 */

import {
  formatPullRequestSubtitle,
  pullRequestStateLabelKey,
  pullRequestStateVisual,
} from "./pull-request-display";

describe("pullRequestStateLabelKey", () => {
  it("maps the four known states to the shared i18n keys", () => {
    expect(pullRequestStateLabelKey("open")).toBe("detail.pull_request_state_open");
    expect(pullRequestStateLabelKey("draft")).toBe("detail.pull_request_state_draft");
    expect(pullRequestStateLabelKey("merged")).toBe("detail.pull_request_state_merged");
    expect(pullRequestStateLabelKey("closed")).toBe("detail.pull_request_state_closed");
  });

  it("returns null for unknown states so the caller falls back to the raw value", () => {
    expect(pullRequestStateLabelKey("reopened")).toBeNull();
    expect(pullRequestStateLabelKey("")).toBeNull();
  });
});

describe("pullRequestStateVisual", () => {
  it("renders open as the PR icon in the success token", () => {
    expect(pullRequestStateVisual("open")).toEqual({
      icon: "git-pull-request-outline",
      themeKey: "success",
      dimmed: false,
    });
  });

  it("renders draft as the PR icon in the muted token, row dimmed", () => {
    expect(pullRequestStateVisual("draft")).toEqual({
      icon: "git-pull-request-outline",
      themeKey: "mutedForeground",
      dimmed: true,
    });
  });

  it("renders merged as the merge icon in the violet token (both ends unified)", () => {
    expect(pullRequestStateVisual("merged")).toEqual({
      icon: "git-merge-outline",
      themeKey: "violet",
      dimmed: false,
    });
  });

  it("renders closed as terminated (destructive token)", () => {
    expect(pullRequestStateVisual("closed")).toEqual({
      icon: "git-pull-request-outline",
      themeKey: "destructive",
      dimmed: false,
    });
  });

  it("falls back to a neutral visual for unknown states", () => {
    expect(pullRequestStateVisual("reopened")).toEqual({
      icon: "git-pull-request-outline",
      themeKey: "foreground",
      dimmed: false,
    });
  });
});

describe("formatPullRequestSubtitle", () => {
  const base = {
    repo_owner: "cfgxy",
    repo_name: "multica",
    number: 13,
    author_login: "cfgxy" as string | null,
    state: "open",
  };

  it("composes repo#number · state · author like the web row", () => {
    expect(formatPullRequestSubtitle(base, "Merged")).toBe(
      "cfgxy/multica#13 · Merged · @cfgxy",
    );
  });

  it("omits the author segment when author_login is null", () => {
    expect(formatPullRequestSubtitle({ ...base, author_login: null }, "Open")).toBe(
      "cfgxy/multica#13 · Open",
    );
  });

  it("omits the author segment when author_login is an empty string", () => {
    expect(formatPullRequestSubtitle({ ...base, author_login: "" }, "Open")).toBe(
      "cfgxy/multica#13 · Open",
    );
  });

  it("falls back to the raw state string when no label resolved", () => {
    expect(formatPullRequestSubtitle(base, null)).toBe(
      "cfgxy/multica#13 · open · @cfgxy",
    );
  });
});
