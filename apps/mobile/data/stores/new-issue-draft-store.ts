/**
 * Draft state for the New Issue modal (`app/(app)/[workspace]/new-issue.tsx`).
 *
 * Why a store instead of local useState: the formSheet picker routes
 * (`new-issue-picker/status.tsx`, etc.) live in a separate Stack screen and
 * have no React parent-child relationship with the new-issue modal. They
 * need a way to read the current draft value and write the new selection
 * back without prop-drilling through the router. A small Zustand store is
 * the minimum-viable cross-screen channel.
 *
 * Lifecycle: `reset()` runs from `new-issue.tsx` when the user dismisses
 * the modal (either submit succeeds or they cancel) so the next open
 * starts clean. Seed-from-comment params still go through local useState
 * inside the screen (description text is a controlled input that doesn't
 * cross routes); only the attribute-chip values live here.
 *
 * Workspace lifecycle: this draft is workspace-scoped (e.g. an `assignee`
 * id only resolves in the workspace whose memberlist seeded it). When the
 * user switches workspaces, the draft is invalid. Reset is wired in
 * `app/(app)/[workspace]/_layout.tsx` via `useResetOnWorkspaceChange()` —
 * that's the only place that calls it on workspace-id transitions.
 *
 * Last-assignee memory (RUYI-79, web parity): `useNewIssueLastAssigneeStore`
 * below persists the assignee submitted with a SUCCESSFUL create, keyed by
 * server(account) × workspace slug, mirroring web's `lastAssigneeType/Id`
 * on the issue draft store (packages/core/issues/stores/draft-store.ts).
 * The in-memory draft store above deliberately stays non-persistent — only
 * the submitted assignee crosses a cold start, same as web (the draft
 * itself is a session concern; see spec §6 non-goals).
 */
import { useEffect, useRef } from "react";
import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import AsyncStorage from "@react-native-async-storage/async-storage";
import type {
  IssuePriority,
  IssueStatus,
  Project,
} from "@multica/core/types";
import type { AssigneeValue } from "@/components/issue/pickers/assignee-picker-body";

interface NewIssueDraftState {
  status: IssueStatus;
  priority: IssuePriority;
  assignee: AssigneeValue;
  assigneeVersion: number;
  dueDate: string | null;
  project: Project | null;
  setStatus: (next: IssueStatus) => void;
  setPriority: (next: IssuePriority) => void;
  setAssignee: (next: AssigneeValue) => void;
  setDueDate: (next: string | null) => void;
  setProject: (next: Project | null) => void;
  reset: () => void;
}

const INITIAL: Pick<
  NewIssueDraftState,
  "status" | "priority" | "assignee" | "dueDate" | "project"
> = {
  status: "todo",
  priority: "none",
  assignee: null,
  dueDate: null,
  project: null,
};

export const useNewIssueDraftStore = create<NewIssueDraftState>((set) => ({
  ...INITIAL,
  assigneeVersion: 0,
  setStatus: (next) => set({ status: next }),
  setPriority: (next) => set({ priority: next }),
  setAssignee: (next) =>
    set((state) => ({
      assignee: next,
      assigneeVersion: state.assigneeVersion + 1,
    })),
  setDueDate: (next) => set({ dueDate: next }),
  setProject: (next) => set({ project: next }),
  reset: () =>
    set((state) => ({
      ...INITIAL,
      assigneeVersion: state.assigneeVersion + 1,
    })),
}));

/**
 * Clears the new-issue draft store whenever the active workspace id
 * changes. Mounted once from the workspace `_layout.tsx`; relies on the
 * workspace store being the source of truth. The `useRef` gate ensures
 * the first mount is a no-op — we only fire `reset()` when the id
 * actually changes from one value to another, so a fresh app launch that
 * resolves the workspace into a non-null id doesn't pointlessly stomp
 * the already-INITIAL store on every cold start.
 */
export function useNewIssueDraftResetOnWorkspaceChange(wsId: string | null) {
  const prevRef = useRef(wsId);
  useEffect(() => {
    if (prevRef.current !== wsId) {
      useNewIssueDraftStore.getState().reset();
      prevRef.current = wsId;
    }
  }, [wsId]);
}

// ---------------------------------------------------------------------------
// Last-assignee memory (RUYI-79) — persisted, server × workspace scoped.
//
// Shape: serverId → workspace slug → the assignee submitted with the last
// successful create from that workspace. `null` is a real value meaning
// "last create was Unassigned" (web parity: Owner decision — unassigned is
// remembered like any other choice); `undefined` (no entry) means no
// history. Callers pass the active serverId/slug explicitly so this module
// stays free of server/workspace store imports (and unit-testable) — the
// same reason it never touches SecureStore: nothing here is a credential,
// AsyncStorage matches the issue-read-state-store pattern.
//
// Isolation & cleanup mirror web's draft cleanup registry:
//   - workspace switch → different key, different value (no rehydrate
//     dance needed since the whole map is in memory);
//   - logout (auth-store) and server removal (server-store) call
//     `clearServerMemory` so the next login on the same server entry never
//     inherits the previous account's pick (web resetInMemory + storage
//     removal semantics).
// A stale id is NOT validated against the workspace directory — web
// renders it through the actor-name fallback ("Unknown*") and so does the
// mobile form chip; removing the value would be new semantics (Owner:
// mirror web exactly).

type LastAssigneeMemory = Record<string, Partial<Record<string, AssigneeValue>>>;

export interface NewIssueSubmissionContext {
  serverId: string;
  workspaceSlug: string;
  userId: string;
  generation: number;
}

interface NewIssueLastAssigneeState {
  byServer: LastAssigneeMemory;
  setLastAssignee: (serverId: string, slug: string, value: AssigneeValue) => void;
  clearServer: (serverId: string) => void;
}

export const useNewIssueLastAssigneeStore = create<NewIssueLastAssigneeState>()(
  persist(
    (set) => ({
      byServer: {},
      setLastAssignee: (serverId, slug, value) =>
        set((s) => ({
          byServer: {
            ...s.byServer,
            [serverId]: { ...s.byServer[serverId], [slug]: value },
          },
        })),
      clearServer: (serverId) =>
        set((s) => {
          if (!(serverId in s.byServer)) return s;
          const { [serverId]: _removed, ...rest } = s.byServer;
          return { byServer: rest };
        }),
    }),
    {
      name: "multica_mobile_new_issue_last_assignee",
      storage: createJSONStorage(() => AsyncStorage),
    },
  ),
);

/** Last assignee submitted from `slug` on `serverId`, or undefined = no history. */
export function getLastAssigneeFor(
  serverId: string,
  slug: string,
): AssigneeValue | undefined {
  return useNewIssueLastAssigneeStore.getState().byServer[serverId]?.[slug];
}

/** Record the assignee submitted with a successful create (web `setLastAssignee`). */
export function setLastAssigneeFor(
  serverId: string,
  slug: string,
  value: AssigneeValue,
): void {
  useNewIssueLastAssigneeStore.getState().setLastAssignee(serverId, slug, value);
}

let submissionContextGeneration = 0;

/** Invalidate creates that were sent from a context that is no longer active. */
export function invalidateNewIssueSubmissionContext(): void {
  submissionContextGeneration += 1;
}

/** Return the generation to capture immediately before sending a create request. */
export function getNewIssueSubmissionContextGeneration(): number {
  return submissionContextGeneration;
}

/**
 * Persist a successful create only when its original account and workspace
 * and generation are still active. A late mutation result must never update
 * another context, including one that changed away and back before it arrived.
 */
export function rememberLastAssigneeAfterSuccessfulCreate(
  submitted: NewIssueSubmissionContext,
  current: NewIssueSubmissionContext | null,
  value: AssigneeValue,
): boolean {
  if (
    !current ||
    submitted.serverId !== current.serverId ||
    submitted.workspaceSlug !== current.workspaceSlug ||
    submitted.userId !== current.userId ||
    submitted.generation !== current.generation
  ) {
    return false;
  }
  setLastAssigneeFor(submitted.serverId, submitted.workspaceSlug, value);
  return true;
}

/** Drop the whole server subtree — logout / server removal. */
export function clearServerMemory(serverId: string): void {
  useNewIssueLastAssigneeStore.getState().clearServer(serverId);
}

/**
 * Seed the draft's assignee from the remembered choice for this server ×
 * workspace (web `clearDraft` re-seed parity). Returns whether a memory
 * existed; a stale/invalid remembered id is seeded as-is on purpose — the
 * form chip renders it through the actor-name "Unknown*" fallback, exactly
 * like web. Absent memory leaves the fresh draft's unassigned default.
 */
export async function seedDraftAssigneeFromMemory(
  serverId: string,
  slug: string,
  expectedAssigneeVersion?: number,
): Promise<boolean> {
  const memoryStore = useNewIssueLastAssigneeStore;
  // AsyncStorage hydration is async; opening the form before it lands (deep
  // link straight into new-issue on a cold start) would read a pre-hydration
  // empty map and skip the seed.
  if (!memoryStore.persist.hasHydrated()) {
    await memoryStore.persist.rehydrate();
  }
  const remembered = getLastAssigneeFor(serverId, slug);
  if (remembered === undefined) return false;
  // Hydration may finish after the user has changed the picker. Only seed the
  // untouched draft instance captured when this asynchronous read began.
  if (
    expectedAssigneeVersion !== undefined &&
    useNewIssueDraftStore.getState().assigneeVersion !== expectedAssigneeVersion
  ) {
    return false;
  }
  useNewIssueDraftStore.getState().setAssignee(remembered);
  return true;
}
