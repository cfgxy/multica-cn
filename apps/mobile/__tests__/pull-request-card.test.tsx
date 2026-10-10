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
  useT: () => ({ t: (_key: string, fallback?: string) => fallback ?? _key }),
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
});
