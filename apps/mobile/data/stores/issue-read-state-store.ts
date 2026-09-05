/**
 * Per-issue read state (RUYI-78 已读状态) — "this issue was opened at
 * least once before", persisted per install.
 *
 * The re-entry collapse policy (lib/comment-collapse-policy.ts, applied
 * in timeline-list.tsx) keys off this store: on re-entry only each
 * agent's last comment thread stays expanded. First entry (no record
 * here) keeps the RUYI-28 default — every root collapsed.
 *
 * Why persisted when sibling stores (last-viewed-store, viewed-issues-
 * store) are in-memory only: read state is not a session nicety but the
 * policy's input. Losing it on every cold start would silently degrade
 * re-entry back to first-entry behavior — exactly the "sometimes it
 * works, sometimes it doesn't" nondeterminism this issue fixes.
 * AsyncStorage is already a mobile dependency (data/server-store.ts).
 *
 * Keyed by bare issue UUID: ids are server-generated UUIDv4, unique
 * across servers for any practical purpose; a cross-server collision
 * would at worst flip one issue's collapse default. Flat shape keeps
 * eviction simple — beyond READ_STATE_MAX_ENTRIES the oldest timestamp
 * is dropped (this store is a bounded recency set, not an archive).
 */
import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import AsyncStorage from "@react-native-async-storage/async-storage";

export const READ_STATE_MAX_ENTRIES = 1000;

interface IssueReadStateStore {
  /** issueId → ISO timestamp of the most recent open. */
  readAt: Record<string, string>;
  isRead: (issueId: string) => boolean;
  markRead: (issueId: string, when?: string) => void;
}

function evictOldest(readAt: Record<string, string>) {
  const ids = Object.keys(readAt);
  const overflow = ids.length - READ_STATE_MAX_ENTRIES;
  if (overflow <= 0) return;
  ids.sort((a, b) => (readAt[a] < readAt[b] ? -1 : readAt[a] > readAt[b] ? 1 : 0));
  for (const id of ids.slice(0, overflow)) {
    delete readAt[id];
  }
}

export const useIssueReadStateStore = create<IssueReadStateStore>()(
  persist(
    (set, get) => ({
      readAt: {},
      isRead: (issueId) => get().readAt[issueId] !== undefined,
      markRead: (issueId, when) =>
        set((s) => {
          const readAt = {
            ...s.readAt,
            [issueId]: when ?? new Date().toISOString(),
          };
          evictOldest(readAt);
          return { readAt };
        }),
    }),
    {
      name: "multica_mobile_issue_read_state",
      storage: createJSONStorage(() => AsyncStorage),
    },
  ),
);

/** Test-only reset (matches comment-focus-store's test helper). */
export function resetIssueReadStateForTests(): void {
  useIssueReadStateStore.setState({ readAt: {} });
}
