/**
 * Related-PR row display helpers (RUYI-43) — mirrors the web sidebar's
 * `PullRequestRow` (packages/views/issues/components/pull-request-list.tsx).
 *
 * Behavioral parity points:
 *   - State vocabulary is identical to web: the four `GitHubPullRequestState`
 *     values map onto the same shared `issues:detail.pull_request_state_*`
 *     i18n keys the web row renders. Unknown states return null so the
 *     caller falls back to the raw server string ("API Response
 *     Compatibility" — never silently drop a category; web's
 *     `getStateLabel` ends in the same raw-value fallback).
 *   - Subtitle composition matches the web row exactly:
 *     `owner/repo#N · <state>` + ` · @author` only when an author exists.
 *   - Colors diverge from web's literal emerald/violet hex on purpose:
 *     mobile theming is token-based (NativeWind semantic tokens), so each
 *     state maps to the token carrying the same semantics — open=success,
 *     draft=muted, merged=info, closed=destructive.
 */

/** Shape the row actually renders; a lenient subset of `GitHubPullRequest`
 *  (imported as `import type` in the screen) keeps this module node-testable. */
export interface PullRequestDisplayItem {
  repo_owner: string;
  repo_name: string;
  number: number;
  author_login: string | null;
  state: string;
}

/** Shared i18n key for a known state, or null to render the raw value. */
export function pullRequestStateLabelKey(state: string): string | null {
  switch (state) {
    case "open":
      return "detail.pull_request_state_open";
    case "draft":
      return "detail.pull_request_state_draft";
    case "merged":
      return "detail.pull_request_state_merged";
    case "closed":
      return "detail.pull_request_state_closed";
    default:
      return null;
  }
}

export interface PullRequestStateVisual {
  icon: "git-pull-request-outline" | "git-merge-outline";
  /** Key into mobile's `THEME[colorScheme]` (lib/theme.ts) for the icon
   *  color — vector icons take a raw `color` prop, not NativeWind classes. */
  themeKey: "success" | "mutedForeground" | "info" | "destructive" | "foreground";
  /** Draft rows render at reduced opacity, mirroring web's
   *  `isDraft ? "opacity-80"` on PullRequestRow. */
  dimmed: boolean;
}

/** Icon + semantic theme token per state; neutral fallback for unknown. */
export function pullRequestStateVisual(state: string): PullRequestStateVisual {
  switch (state) {
    case "open":
      return { icon: "git-pull-request-outline", themeKey: "success", dimmed: false };
    case "draft":
      return {
        icon: "git-pull-request-outline",
        themeKey: "mutedForeground",
        dimmed: true,
      };
    case "merged":
      return { icon: "git-merge-outline", themeKey: "info", dimmed: false };
    case "closed":
      return {
        icon: "git-pull-request-outline",
        themeKey: "destructive",
        dimmed: false,
      };
    default:
      return {
        icon: "git-pull-request-outline",
        themeKey: "foreground",
        dimmed: false,
      };
  }
}

/**
 * `owner/repo#N · <state>` (+ ` · @author` when present) — web row parity.
 * `stateLabel` null renders the raw server state instead.
 */
export function formatPullRequestSubtitle(
  pr: Pick<
    PullRequestDisplayItem,
    "repo_owner" | "repo_name" | "number" | "author_login" | "state"
  >,
  stateLabel: string | null,
): string {
  const parts = [
    `${pr.repo_owner}/${pr.repo_name}#${pr.number}`,
    ` · ${stateLabel ?? pr.state}`,
  ];
  if (pr.author_login) {
    parts.push(` · @${pr.author_login}`);
  }
  return parts.join("");
}
