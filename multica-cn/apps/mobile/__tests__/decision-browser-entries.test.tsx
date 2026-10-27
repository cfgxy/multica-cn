/**
 * RUYI-863 mobile entry-point evidence. The three decision-card entries
 * (sidebar more-menu → decisions.tsx with no params, info-feed single card
 * → decisions.tsx ?cardId=…, batch → batch.tsx ?filter=open) plus the
 * legacy deep-link ([decisionId].tsx) all render the SAME shared browser
 * (MobileDecisionCardBrowser) with entry-specific initial filter + card.
 * This suite mounts the real screens and asserts each entry lands where its
 * rule says (first card, pinned target card that "must not bounce", first
 * pending card, and the batch done state when the pending set empties).
 *
 * Division of layers (repo test rules): the positioning/filter/advance
 * MATRIX lives in packages/core/issues/decision-card-browser.test.ts and is
 * not re-run here through a DOM mount; the card FACE (options, answer,
 * cancel/restore affordances, cache upsert) lives in decision-card.test.tsx.
 * This file is the wiring: screens pass the right params into the shared
 * browser, and the browser's screen-level main path (advance when the active
 * card leaves the filter bucket; done state when "open" empties) holds.
 */
import React from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react-native";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ComponentType } from "react";
import type { IssueDecision } from "@multica/core/types";

// --- router params, per-test mutable (mock-prefixed so jest hoisting allows)
let mockGlobalParams: { cardId?: string; filter?: string } = {};
const mockLocalParams = { id: "issue-1", workspace: "ws", decisionId: "c2" };

// The shared query layer resolves a card list per test; the screen's real
// useQuery drives the browser from it without a network.
let mockCardData: IssueDecision[] = [];

jest.mock("expo-router", () => ({
  Stack: { Screen: () => null },
  useLocalSearchParams: () => mockLocalParams,
  useGlobalSearchParams: () => mockGlobalParams,
}));

jest.mock("@/data/queries/decisions", () => ({
  issueDecisionsOptions: (issueId: string) => ({
    queryKey: ["issues", "decisions", issueId],
    queryFn: () => Promise.resolve(mockCardData),
    enabled: !!issueId,
  }),
}));

// The card face has its own suite (decision-card.test.tsx); stub it to a
// test-id marker so this file asserts the browser's position, not the card.
jest.mock("@/components/issue/decision-card", () => {
  const { View, Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    DecisionCard: ({ decision }: { decision: IssueDecision }) => (
      <View testID="browser-stub-card">
        <Text testID="stub-card-question">{decision.question}</Text>
        <Text testID="stub-card-status">{decision.status}</Text>
      </View>
    ),
  };
});

jest.mock("@/components/ui/text", () => {
  const { Text } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Text };
});
jest.mock("@/components/ui/button", () => {
  const { Pressable } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    Button: ({ variant: _v, size: _s, ...props }: React.ComponentProps<typeof Pressable> & { variant?: string; size?: string }) => (
      <Pressable {...props} />
    ),
  };
});
jest.mock("@/components/ui/dropdown-menu", () => {
  const { Pressable, View } = jest.requireActual<typeof import("react-native")>("react-native");
  return {
    DropdownMenu: ({ children }: { children: React.ReactNode }) => <View>{children}</View>,
    DropdownMenuTrigger: ({ children }: { children: React.ReactNode }) => <View>{children}</View>,
    DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <View>{children}</View>,
    DropdownMenuItem: ({ children, ...props }: React.ComponentProps<typeof Pressable>) => <Pressable {...props}>{children}</Pressable>,
  };
});
jest.mock("@expo/vector-icons", () => ({
  Ionicons: () => null,
}));
jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light" }),
}));
jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (_key: string, fallback?: string, options?: Record<string, string>) =>
      (fallback ?? _key).replace(/\{\{(\w+)\}\}/g, (_m: string, name: string) => options?.[name] ?? ""),
  }),
}));
jest.mock("@/lib/utils", () => ({
  cn: (...classes: unknown[]) => classes.filter(Boolean).join(" "),
}));

// eslint-disable-next-line @typescript-eslint/no-require-imports
const MoreMenuScreen = jest.requireActual<typeof import("@/app/(app)/[workspace]/issue/[id]/decisions")>(
  "@/app/(app)/[workspace]/issue/[id]/decisions",
).default;
// eslint-disable-next-line @typescript-eslint/no-require-imports
const SingleCardScreen = jest.requireActual<typeof import("@/app/(app)/[workspace]/issue/[id]/decisions/[decisionId]")>(
  "@/app/(app)/[workspace]/issue/[id]/decisions/[decisionId]",
).default;
// eslint-disable-next-line @typescript-eslint/no-require-imports
const BatchScreen = jest.requireActual<typeof import("@/app/(app)/[workspace]/issue/[id]/decisions/batch")>(
  "@/app/(app)/[workspace]/issue/[id]/decisions/batch",
).default;

function card(overrides: Partial<IssueDecision> & { id: string; question: string }): IssueDecision {
  return {
    issue_id: "issue-1",
    multi_select: false,
    recommended_indices: [],
    status: "open",
    selected_indices: [],
    answered_by_type: null,
    answered_by_id: null,
    answered_at: null,
    answer_comment_id: null,
    created_by_type: "agent",
    created_by_id: "agent-1",
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    source_comment_id: null,
    ...overrides,
  } as unknown as IssueDecision;
}

const threeCards: IssueDecision[] = [
  card({ id: "c1", question: "First card", status: "open", created_at: "2026-10-01T00:00:00Z" }),
  card({ id: "c2", question: "Second card", status: "answered", created_at: "2026-10-02T00:00:00Z" }),
  card({ id: "c3", question: "Third card", status: "cancelled", created_at: "2026-10-03T00:00:00Z" }),
];

async function renderEntry(Entry: ComponentType, cards: IssueDecision[]) {
  mockCardData = cards;
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await render(
    <QueryClientProvider client={qc}>
      <Entry />
    </QueryClientProvider>,
  );
  await screen.findByTestId("decision-card-browser");
  // Let the screen's initial fetch settle (its snapshot writes the cache):
  // until it lands, a setQueryData below would be clobbered by that write.
  await waitFor(() =>
    expect(qc.getQueryState(["issues", "decisions", "issue-1"])?.data).toEqual(cards),
  );
  return qc;
}

function question() {
  return screen.queryByTestId("stub-card-question")?.props.children;
}

beforeEach(() => {
  mockGlobalParams = {};
});

afterEach(() => {
  cleanup();
});

describe("RUYI-863 mobile decision-card entries (shared browser)", () => {
  it("more-menu entry (no params) lands on the first card under all-filter", async () => {
    await renderEntry(MoreMenuScreen, threeCards);

    // No pin, no filter → "all" and the FIRST card in created_at order.
    expect(question()).toBe("First card");
    expect(screen.getByText("1 / 3")).toBeTruthy();
    expect(screen.getByTestId("decision-browser-filter")).toBeTruthy();
    expect(screen.getByTestId("decision-browser-filter-all").props.accessibilityState).toMatchObject({
      selected: true,
    });
    // The dropdown exposes the three statuses plus all.
    expect(screen.queryByTestId("decision-browser-filter-open")).toBeTruthy();
    expect(screen.queryByTestId("decision-browser-filter-cancelled")).toBeTruthy();
  });

  it("keeps the full card and navigation controls in a naturally growing scroll container", async () => {
    await renderEntry(MoreMenuScreen, threeCards);

    const scroll = screen.getByTestId("decision-browser-scroll");
    expect(scroll.props.contentContainerStyle).toEqual({ flexGrow: 1 });
    expect(scroll.props.contentContainerClassName).not.toContain("flex-1");
    expect(scroll.props.keyboardShouldPersistTaps).toBe("handled");
    expect(screen.getByTestId("browser-stub-card")).toBeTruthy();
    expect(screen.getByTestId("decision-browser-next")).toBeTruthy();
  });

  it("info-feed single-card entry pins the target card even when it is cancelled (no bounce)", async () => {
    mockGlobalParams = { cardId: "c3" };
    await renderEntry(MoreMenuScreen, threeCards);

    // Pinned card under "all": a cancelled/answered target stays put — the
    // rule RUYI-863 requires of the feed's per-card entry.
    expect(question()).toBe("Third card");
    expect(screen.getByTestId("stub-card-status").props.children).toBe("cancelled");
    expect(screen.getByText("3 / 3")).toBeTruthy();
  });

  it("batch entry opens the browser pre-filtered to open on the first pending card", async () => {
    mockGlobalParams = { filter: "open" };
    await renderEntry(BatchScreen, threeCards);

    // Only c1 is open; the pointer lands on it, not on the first card.
    expect(question()).toBe("First card");
    expect(screen.getByText("1 / 1")).toBeTruthy();
    expect(screen.getByTestId("decision-browser-filter-open").props.accessibilityState).toMatchObject({
      selected: true,
    });
  });

  it("batch entry shows the done state when the pending set is empty", async () => {
    const noPending: IssueDecision[] = [
      card({ id: "c1", question: "a", status: "answered" }),
      card({ id: "c2", question: "b", status: "cancelled" }),
    ];
    mockGlobalParams = { filter: "open" };
    await renderEntry(BatchScreen, noPending);

    // No open card → the retired batch bar is replaced by the browser's
    // done state, not an empty list and not a bounce back to the feed.
    expect(screen.queryByTestId("browser-stub-card")).toBeNull();
    expect(screen.queryByTestId("decision-browser-done")).toBeTruthy();
    expect(screen.queryByTestId("decision-browser-empty")).toBeNull();
  });

  it("legacy deep-link entry pins its cardId under the all filter", async () => {
    mockLocalParams.decisionId = "c2";
    await renderEntry(SingleCardScreen, threeCards);

    expect(question()).toBe("Second card");
    expect(screen.getByText("2 / 3")).toBeTruthy();
    mockLocalParams.decisionId = "c2";
  });

  it("advances to the next pending card when the active one leaves the open bucket, then reaches the done state", async () => {
    const pending: IssueDecision[] = [
      card({ id: "c1", question: "a" }),
      card({ id: "c2", question: "b" }),
    ];
    mockGlobalParams = { filter: "open" };
    const qc = await renderEntry(BatchScreen, pending);

    expect(question()).toBe("a");
    expect(screen.getByText("1 / 2")).toBeTruthy();

    // Answering c1 (the cache upsert the screen performs on success) drops
    // it from the open bucket; the pointer advances to the next open card.
    // The cache write lands inside act, but TanStack Query notifies its
    // observers through a batched macrotask (notifyManager.setTimeout), so
    // the re-render commits AFTER act returns — waitFor for the new position
    // instead of asserting immediately.
    await act(async () => {
      qc.setQueryData<IssueDecision[]>(["issues", "decisions", "issue-1"], [
        card({ id: "c1", question: "a", status: "answered" }),
        card({ id: "c2", question: "b" }),
      ]);
    });
    await waitFor(() => expect(question()).toBe("b"));
    expect(screen.getByText("1 / 1")).toBeTruthy();

    // Answer the last one → the filtered set empties → the done state, no
    // navigation out of the browser.
    await act(async () => {
      qc.setQueryData<IssueDecision[]>(["issues", "decisions", "issue-1"], [
        card({ id: "c1", question: "a", status: "answered" }),
        card({ id: "c2", question: "b", status: "answered" }),
      ]);
    });
    await waitFor(() => expect(screen.queryByTestId("decision-browser-done")).toBeTruthy());
    expect(screen.queryByTestId("browser-stub-card")).toBeNull();
  });

  it("switching the filter re-positions the pointer to the matching bucket", async () => {
    await renderEntry(MoreMenuScreen, threeCards);
    expect(question()).toBe("First card");

    await act(async () => {
      fireEvent.press(screen.getByTestId("decision-browser-filter-cancelled"));
    });
    // The cancelled card becomes the current position under its bucket.
    expect(question()).toBe("Third card");
    expect(screen.getByText("1 / 1")).toBeTruthy();

    await act(async () => {
      fireEvent.press(screen.getByTestId("decision-browser-filter-open"));
    });
    expect(question()).toBe("First card");
    expect(screen.getByText("1 / 1")).toBeTruthy();
  });
});
