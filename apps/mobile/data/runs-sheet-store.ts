/**
 * Shared open-state for the Agent Runs sheet on issue detail.
 *
 * The Runs sheet has two entry points: `<AgentActivityRow>` inside
 * `IssueHeaderCard` (in the page body) and `<AgentHeaderBadge>` in the
 * native Stack header. Those two trees can't share local React state
 * across the Stack-header / page-body boundary, so we lift to Zustand.
 *
 * Scoped to a single `openIssueId` (not a Set): only one issue detail
 * screen is mounted at a time on mobile (Expo Router Stack). When the
 * user navigates to a different issue, the new screen's
 * `<RunsSheet issueId={newId}>` predicate (`openIssueId === newId`)
 * evaluates false even if the store retains a stale id from the
 * previous screen — so the new sheet stays closed.
 *
 * Mobile-local, no persist (sheet open state must NOT survive relaunch).
 * Follows the workspace-store.ts shape: synchronous `create<T>(set => ...)`.
 */
import { create } from "zustand";

interface RunsSheetState {
  /** Issue id whose RunsSheet is currently open. null = no sheet open. */
  openIssueId: string | null;
  open: (issueId: string) => void;
  close: () => void;
}

export const useRunsSheetStore = create<RunsSheetState>((set) => ({
  openIssueId: null,
  open: (issueId) => set({ openIssueId: issueId }),
  close: () => set({ openIssueId: null }),
}));
