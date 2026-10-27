import React from "react";
import { Alert, Linking } from "react-native";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react-native";
import type { GitHubPullRequest } from "@multica/core/types";

const mockOpenURL = jest.fn();
const mockAlert = jest.fn();

jest.spyOn(Linking, "openURL").mockImplementation(((url: string) =>
  mockOpenURL(url)) as typeof Linking.openURL);
jest.spyOn(Alert, "alert").mockImplementation(
  ((...args: unknown[]) => mockAlert(...args)) as typeof Alert.alert,
);

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (key: string, options?: string | Record<string, unknown>) => {
      if (typeof options === "string") return options;
      const templates: Record<string, string> = {
        "detail.pull_request_checks_failed_count": "{{failed}}/{{total}} failed",
        "detail.pull_request_checks_failed_named": "{{failed}}/{{total}} failed · {{names}}",
        "detail.pull_request_checks_running": "{{passed}}/{{total}} · {{running}} running",
        "detail.pull_request_checks_all_passed": "All checks passed ({{total}}/{{total}})",
        "detail.pull_request_checks_unknown": "Checks status unknown",
      };
      return (templates[key] ?? key).replace(/\{\{(\w+)\}\}/g, (_match, name: string) =>
        String(options?.[name] ?? ""),
      );
    },
  }),
}));

jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light" }),
}));

jest.mock("@/lib/theme", () => ({
  THEME: {
    light: {
      success: "#16a34a",
      info: "#2563eb",
      destructive: "#dc2626",
      mutedForeground: "#737373",
      foreground: "#171717",
    },
  },
}));

jest.mock("@expo/vector-icons", () => ({
  Ionicons: () => null,
}));

// The ui primitives pull in @rn-primitives/slot, whose dist ships raw JSX the
// jest transform never sees — stand in plain RN equivalents (decision-card
// pattern).
jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});
jest.mock("@/components/ui/card", () => {
  const { View } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Card: ({ children }: { children: React.ReactNode }) => <View>{children}</View> };
});
jest.mock("@/lib/utils", () => ({
  cn: (...classes: unknown[]) => classes.filter(Boolean).join(" "),
}));

import { PullRequestCard } from "@/components/issue/pull-request-card";

// toJSON() hands back library JSON whose text leaves are plain strings, so
// the walker accepts both shapes (issue-agent-activity-badge pattern).
type JsonNode = string | { props?: Record<string, unknown>; children?: JsonNode[] } | null;

function collectClassNames(node: JsonNode, out: string[] = []): string[] {
  if (!node || typeof node === "string") return out;
  const cls = node.props?.className;
  if (typeof cls === "string") out.push(cls);
  for (const child of node.children ?? []) collectClassNames(child, out);
  return out;
}

function pr(overrides: Partial<GitHubPullRequest> = {}): GitHubPullRequest {
  return {
    id: "pr-1",
    workspace_id: "ws-1",
    repo_owner: "cfgxy",
    repo_name: "multica-cn",
    number: 313,
    title: "fix(mobile): surface pull requests in the timeline",
    state: "open",
    html_url: "https://github.com/cfgxy/multica-cn/pull/313",
    branch: "agent/x",
    base_branch: null,
    author_login: "gu-guyu",
    author_avatar_url: null,
    merged_at: null,
    closed_at: null,
    pr_created_at: "2026-10-02T04:00:00Z",
    pr_updated_at: "2026-10-02T04:00:00Z",
    ...overrides,
  };
}

// Serialised press: a bare fireEvent inside an async test can overlap
// React's act scope (RNTL v14 + React 19) and silently break the NEXT
// render's commit — decision-card's pressPressable pattern.
async function pressCard(
  element: Parameters<typeof fireEvent.press>[0],
) {
  await act(async () => {
    fireEvent.press(element);
  });
}

describe("PullRequestCard (RUYI-634)", () => {
  beforeEach(() => {
    mockOpenURL.mockReset();
    mockAlert.mockReset();
    mockOpenURL.mockResolvedValue(undefined);
  });

  it("renders title, repo/number subtitle, and the state from the shared display helpers", async () => {
    await render(<PullRequestCard pr={pr()} />);

    expect(screen.getByTestId("pull-request-card")).toBeTruthy();
    expect(
      screen.getByText("fix(mobile): surface pull requests in the timeline"),
    ).toBeTruthy();
    // formatPullRequestSubtitle: `owner/repo#N · <state>` + ` · @author`;
    // the mocked t() falls back to the raw server state for known keys.
    expect(
      screen.getByText("cfgxy/multica-cn#313 · open · @gu-guyu"),
    ).toBeTruthy();
  });

  it("opens the PR's canonical html_url with one tap", async () => {
    await render(<PullRequestCard pr={pr()} />);

    await pressCard(screen.getByTestId("pull-request-card"));

    expect(mockOpenURL).toHaveBeenCalledTimes(1);
    expect(mockOpenURL).toHaveBeenCalledWith(
      "https://github.com/cfgxy/multica-cn/pull/313",
    );
    expect(mockAlert).not.toHaveBeenCalled();
  });

  it("keeps the user-visible failure Alert — never a silent no-op", async () => {
    mockOpenURL.mockRejectedValueOnce(new Error("no handler for https URLs"));

    await render(<PullRequestCard pr={pr()} />);

    await pressCard(screen.getByTestId("pull-request-card"));

    // The rejection → typed result → Alert chain crosses several
    // microtask turns; waitFor polls inside act until it lands.
    await waitFor(() => expect(mockAlert).toHaveBeenCalledTimes(1));
    expect(mockAlert).toHaveBeenCalledWith(
      "Couldn't open this pull request.",
      "no handler for https URLs",
    );
  });

  it("renders every linked PR reachable: a second card opens its own URL", async () => {
    await render(
      <React.Fragment>
        <PullRequestCard pr={pr()} />
        <PullRequestCard
          pr={pr({ id: "pr-2", number: 325, html_url: "https://github.com/cfgxy/multica-cn/pull/325" })}
        />
      </React.Fragment>,
    );

    await pressCard(screen.getAllByTestId("pull-request-card")[1]!);

    expect(mockOpenURL).toHaveBeenCalledTimes(1);
    expect(mockOpenURL).toHaveBeenCalledWith(
      "https://github.com/cfgxy/multica-cn/pull/325",
    );
  });

  it("marks draft PRs dimmed, mirroring the modal row (RUYI-43 parity)", async () => {
    const { toJSON } = await render(<PullRequestCard pr={pr({ state: "draft" })} />);

    expect(
      collectClassNames(toJSON() as JsonNode).some((c) => c.includes("opacity-80")),
    ).toBe(true);
  });

  it("shows the target branch for the bottom summary and never substitutes the source branch", async () => {
    await render(<PullRequestCard pr={pr({ branch: "agent/source" })} showTargetBranch />);

    expect(screen.getByText("Target branch: Unknown")).toBeTruthy();
    expect(screen.queryByText("agent/source")).toBeNull();
  });

  it("shows a known target branch for the bottom summary", async () => {
    await render(<PullRequestCard pr={pr({ base_branch: "main" })} showTargetBranch />);

    expect(screen.getByText("Target branch: main")).toBeTruthy();
  });

  it("shows the failed check count on the pending-PR summary when enabled", async () => {
    await render(
      <PullRequestCard
        pr={pr({
          checks_conclusion: "failed",
          checks_failed: 1,
          checks_total: 1,
          snapshot_available: true,
        })}
        showTargetBranch
        showMergeRisk
      />,
    );

    expect(screen.getByTestId("pull-request-checks-status").props.children).toBe("1/1 failed");
  });

  it("does not change the existing timeline card unless risk details are enabled", async () => {
    await render(
      <PullRequestCard
        pr={pr({
          checks_conclusion: "failed",
          checks_failed: 1,
          checks_total: 1,
          snapshot_available: true,
        })}
      />,
    );

    expect(screen.queryByTestId("pull-request-checks-status")).toBeNull();
  });

  it("does not claim a checks result when the snapshot is unavailable", async () => {
    await render(
      <PullRequestCard
        pr={pr({
          checks_conclusion: "failed",
          checks_failed: 1,
          checks_total: 1,
          snapshot_available: false,
        })}
        showMergeRisk
      />,
    );

    expect(screen.queryByTestId("pull-request-checks-status")).toBeNull();
  });

  it("keeps missing checks data unknown instead of reporting no checks", async () => {
    await render(<PullRequestCard pr={pr()} showMergeRisk />);

    expect(screen.queryByTestId("pull-request-checks-status")).toBeNull();
  });

  it("does not report no checks or passed for an unrecognized rollup", async () => {
    await render(
      <PullRequestCard
        pr={pr({
          checks_rollup: "mystery",
          checks_conclusion: null,
          snapshot_available: true,
          checks_total: 0,
          checks_failed: 0,
        })}
        showMergeRisk
      />,
    );

    expect(screen.getByTestId("pull-request-checks-status").props.children).toBe(
      "Checks status unknown",
    );
    expect(screen.queryByText("No checks yet")).toBeNull();
  });

  it("reports a failed conclusion without inventing missing counts", async () => {
    await render(
      <PullRequestCard
        pr={pr({ checks_conclusion: "failed", snapshot_available: true })}
        showMergeRisk
      />,
    );

    expect(screen.getByTestId("pull-request-checks-status").props.children).toBe(
      "Checks failed",
    );
  });

  it("shows the current review decision in the pending-PR card", async () => {
    await render(
      <PullRequestCard
        pr={pr({ review_decision: "CHANGES_REQUESTED", snapshot_available: true })}
        showMergeRisk
      />,
    );

    expect(screen.getByTestId("pull-request-review-decision").props.children).toBe(
      "Changes requested",
    );
  });

  it.each([null, "NEW_VALUE"])("keeps review state unknown for %s", async (reviewDecision) => {
    await render(
      <PullRequestCard pr={pr({ review_decision: reviewDecision, snapshot_available: true })} showMergeRisk />,
    );

    expect(screen.getByTestId("pull-request-review-decision").props.children).toBe("Review status unknown");
  });
});
