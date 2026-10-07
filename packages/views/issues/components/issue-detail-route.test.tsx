import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, render, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { setApiInstance } from "@multica/core/api";
import type { ApiClient } from "@multica/core/api/client";
import { NavigationProvider } from "../../navigation";
import type { NavigationAdapter } from "../../navigation";
import {
  IssueDetailRoute,
  parseCommentHighlightHash,
  parseDecisionHighlightHash,
  useCanonicalIssueUrl,
  useHighlightHash,
} from "./issue-detail-route";

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/paths", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/paths")>(
    "@multica/core/paths",
  );
  return {
    ...actual,
    useCurrentWorkspace: () => ({ id: "ws-1", name: "Acme", slug: "acme" }),
    useWorkspacePaths: () => actual.paths.workspace("acme"),
  };
});

const replace = vi.fn();
const push = vi.fn();

function wrapper({ children }: { children: ReactNode }) {
  const adapter: NavigationAdapter = {
    push,
    replace,
    back: vi.fn(),
    pathname: "/acme/issues/x",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (p: string) => `https://app.multica.com${p}`,
  };
  return <NavigationProvider value={adapter}>{children}</NavigationProvider>;
}

describe("useCanonicalIssueUrl", () => {
  beforeEach(() => {
    replace.mockClear();
    push.mockClear();
  });

  it("rewrites a UUID URL to the identifier once the issue resolves", () => {
    const { rerender } = renderHook(
      ({ identifier }: { identifier?: string }) =>
        useCanonicalIssueUrl("cb240efb-154c-42a8-ae92-42b02676feca", identifier),
      { wrapper, initialProps: {} },
    );

    // Nothing to rewrite to while the issue is still loading.
    expect(replace).not.toHaveBeenCalled();

    rerender({ identifier: "TRS-134" });
    expect(replace).toHaveBeenCalledWith("/acme/issues/TRS-134");
    expect(push).not.toHaveBeenCalled();
  });

  it("leaves an already-canonical URL alone", () => {
    renderHook(() => useCanonicalIssueUrl("TRS-134", "TRS-134"), { wrapper });
    expect(replace).not.toHaveBeenCalled();
  });

  // `useWorkspacePaths()` returns a fresh object per call, so the effect's
  // dependencies change identity on every commit. Without the ref guard this
  // re-fired the replace forever.
  it("rewrites once, not on every render", () => {
    const { rerender } = renderHook(
      () => useCanonicalIssueUrl("cb240efb-154c-42a8-ae92-42b02676feca", "TRS-134"),
      { wrapper },
    );

    rerender();
    rerender();
    expect(replace).toHaveBeenCalledTimes(1);
  });

  // A lowercase key resolves server-side, so the URL must still be normalized
  // to the issue's real identifier rather than left as typed.
  it("normalizes a differently-cased identifier segment", () => {
    renderHook(() => useCanonicalIssueUrl("trs-134", "TRS-134"), { wrapper });
    expect(replace).toHaveBeenCalledWith("/acme/issues/TRS-134");
  });

  it("preserves a source-comment deep link while canonicalizing a UUID", () => {
    renderHook(
      () => useCanonicalIssueUrl(
        "cb240efb-154c-42a8-ae92-42b02676feca",
        "TRS-134",
        "#comment-comment-7",
      ),
      { wrapper },
    );
    expect(replace).toHaveBeenCalledWith("/acme/issues/TRS-134#comment-comment-7");
  });
});

// The Decision Center / inbox deep links (RUYI-494) arrive on the web as an
// App Router navigation whose URL — fragment included — is applied by
// history.pushState during the commit phase, i.e. AFTER this route's first
// render, and pushState never fires `hashchange`. A fragment that lands that
// way must still reach the highlight machinery on a later commit.
describe("useHighlightHash", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/acme/issues/x");
  });

  it("reads the fragment present at first render (full-page load)", () => {
    window.history.replaceState({}, "", "/acme/issues/TRS-134#decision-card-9");
    const { result } = renderHook(() => useHighlightHash(), { wrapper });
    expect(result.current.hash).toBe("#decision-card-9");
    expect(result.current.decisionId).toBe("card-9");
    expect(result.current.commentId).toBeUndefined();
  });

  it("tracks a later hashchange", async () => {
    const { result } = renderHook(() => useHighlightHash(), { wrapper });
    expect(result.current.hash).toBe("");
    act(() => {
      window.location.hash = "#comment-comment-7";
    });
    // jsdom dispatches hashchange as a task, not synchronously.
    await waitFor(() => {
      expect(result.current.hash).toBe("#comment-comment-7");
    });
    expect(result.current.commentId).toBe("comment-7");
  });

  it("picks up a fragment that commits after mount via pushState (SPA navigation)", () => {
    const { result, rerender } = renderHook(() => useHighlightHash(), { wrapper });
    expect(result.current.hash).toBe("");
    act(() => {
      // pushState updates the URL without any hashchange event — the web SPA
      // shape. The hook can only converge by re-reading on a later commit.
      window.history.pushState({}, "", "/acme/issues/cb240efb#decision-card-9");
    });
    expect(result.current.hash).toBe("");
    rerender();
    expect(result.current.hash).toBe("#decision-card-9");
    expect(result.current.decisionId).toBe("card-9");
  });
});

describe("parseCommentHighlightHash", () => {
  it.each([
    ["#comment-01a02814-f098-7309-8286-0b249c66884d", "01a02814-f098-7309-8286-0b249c66884d"],
    ["#comment-comment_7", "comment_7"],
    ["#activity", undefined],
    ["#comment-", undefined],
    ["#comment-unsafe/value", undefined],
  ])("maps %s to %s", (hash, expected) => {
    expect(parseCommentHighlightHash(hash)).toBe(expected);
  });
});

// Decision Center rows deep-link `#decision-<cardId>` (RUYI-494); the parse
// must stay disjoint from the comment prefix so one hash can never land both.
describe("parseDecisionHighlightHash", () => {
  it.each([
    ["#decision-01a113d4-0000-0000-0000-000000000000", "01a113d4-0000-0000-0000-000000000000"],
    ["#decision-card_1", "card_1"],
    ["#comment-01a02814", undefined],
    ["#decision-", undefined],
    ["#decision-unsafe/value", undefined],
  ])("maps %s to %s", (hash, expected) => {
    expect(parseDecisionHighlightHash(hash)).toBe(expected);
  });
});

describe("IssueDetailRoute with an identifier that names no issue", () => {
  // Regression: the route used to fall through to IssueDetail with the raw
  // identifier. IssueDetail mounted a second observer on the query that had
  // just failed, `retryOnMount` refetched it, the route flipped back to its
  // skeleton and unmounted IssueDetail, then remounted it when the refetch
  // failed — an unbounded request loop that never reached "not found".
  // Retry is off so any count above 1 can only be a remount refetch.
  it("settles on not-found without looping requests", async () => {
    replace.mockClear();
    push.mockClear();
    const getIssue = vi.fn().mockRejectedValue(new Error("issue not found"));
    setApiInstance({ getIssue } as unknown as ApiClient);
    const qc = new QueryClient({
      defaultOptions: { queries: { staleTime: Infinity, retry: false } },
    });

    const { rerender } = render(
      <QueryClientProvider client={qc}>
        <NavigationProvider
          value={{
            push,
            replace,
            back: vi.fn(),
            pathname: "/acme/issues/ZZZ-134",
            searchParams: new URLSearchParams(),
            hash: "",
            getShareableUrl: (p: string) => `https://app.multica.com${p}`,
          }}
        >
          <IssueDetailRoute routeId="ZZZ-134" />
        </NavigationProvider>
      </QueryClientProvider>,
    );

    await waitFor(() => expect(getIssue).toHaveBeenCalled());
    await new Promise((resolve) => setTimeout(resolve, 250));
    expect(getIssue).toHaveBeenCalledTimes(1);

    rerender(
      <QueryClientProvider client={qc}>
        <NavigationProvider
          value={{
            push,
            replace,
            back: vi.fn(),
            pathname: "/acme/issues/ZZZ-134",
            searchParams: new URLSearchParams(),
            hash: "",
            getShareableUrl: (p: string) => `https://app.multica.com${p}`,
          }}
        >
          <IssueDetailRoute routeId="ZZZ-134" />
        </NavigationProvider>
      </QueryClientProvider>,
    );
    await new Promise((resolve) => setTimeout(resolve, 250));
    expect(getIssue).toHaveBeenCalledTimes(1);

    // A failed resolve must never rewrite the URL.
    expect(replace).not.toHaveBeenCalled();
    qc.clear();
  });
});
