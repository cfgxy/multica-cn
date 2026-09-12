/**
 * Preferences for the smart-mode (agent quick-create) flow, mirroring two
 * web stores: create-mode-store (`lastMode`) and quick-create-store
 * (`lastActor`). Mobile keeps them in one store because they share a single
 * consumer (the new-issue screen).
 *
 * `lastMode` stays session-scoped and in-memory (mobile store convention,
 * see my-issues-view-store.ts) — a documented divergence from web's
 * localStorage-backed preference, semantics identical within a session.
 *
 * `lastActor` is NOT session-scoped (RUYI-130). It is the "remember who I
 * filed the last issue as" memory, and a memory that dies with the process
 * silently re-seeds the seed chain's tail (first visible agent) on the next
 * cold start — which in a workspace whose squad leader sorts first reads as
 * the squad being downgraded to its leader. It is therefore persisted and
 * scoped to server(account) × workspace slug, exactly like the manual
 * form's last-assignee memory (new-issue-draft-store) and web's
 * workspace-aware quick-create storage. AsyncStorage, not SecureStore:
 * nothing here is a credential.
 *
 * Cleanup mirrors the last-assignee memory: logout (auth-store) and server
 * removal (server-store) drop the whole server subtree so the next login on
 * the same entry never inherits the previous account's pick.
 */
import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import AsyncStorage from "@react-native-async-storage/async-storage";
import type { NewIssueSubmissionContext } from "@/data/stores/new-issue-draft-store";
import type { QuickCreateActorRef } from "@/lib/quick-create";

export type QuickCreateMode = "smart" | "manual";

interface QuickCreatePrefsState {
  lastMode: QuickCreateMode;
  setLastMode: (mode: QuickCreateMode) => void;
}

export const useQuickCreatePrefsStore = create<QuickCreatePrefsState>(
  (set) => ({
    // Web's create-mode-store also defaults to "agent" (smart) — a fresh
    // user lands on the one-line flow first.
    lastMode: "smart",
    setLastMode: (mode) => set({ lastMode: mode }),
  }),
);

// ---------------------------------------------------------------------------
// Last quick-create actor memory — persisted, server × workspace scoped.
//
// Shape: serverId → workspace slug → the actor submitted with the last
// successful quick-create from that workspace. No entry means no history,
// and the seed chain falls through to the first visible agent as before.
// Callers pass serverId/slug explicitly so this module stays free of
// server/workspace store imports (and unit-testable) — same rationale as
// the last-assignee memory in new-issue-draft-store.ts.

type LastActorMemory = Record<
  string,
  Partial<Record<string, QuickCreateActorRef>>
>;

interface QuickCreateActorMemoryState {
  byServer: LastActorMemory;
  setLastActor: (
    serverId: string,
    slug: string,
    actor: QuickCreateActorRef,
  ) => void;
  clearServer: (serverId: string) => void;
}

export const useQuickCreateActorMemoryStore =
  create<QuickCreateActorMemoryState>()(
    persist(
      (set) => ({
        byServer: {},
        setLastActor: (serverId, slug, actor) =>
          set((s) => ({
            byServer: {
              ...s.byServer,
              [serverId]: { ...s.byServer[serverId], [slug]: actor },
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
        name: "multica_mobile_quick_create_last_actor",
        storage: createJSONStorage(() => AsyncStorage),
      },
    ),
  );

/** Last actor submitted from `slug` on `serverId`, or null = no history. */
export function getLastQuickCreateActor(
  serverId: string,
  slug: string,
): QuickCreateActorRef | null {
  return (
    useQuickCreateActorMemoryStore.getState().byServer[serverId]?.[slug] ?? null
  );
}

/** Record the actor submitted with a successful quick-create. */
export function setLastQuickCreateActor(
  serverId: string,
  slug: string,
  actor: QuickCreateActorRef,
): void {
  useQuickCreateActorMemoryStore
    .getState()
    .setLastActor(serverId, slug, actor);
}

/** Drop the whole server subtree — logout / server removal. */
export function clearQuickCreateActorMemory(serverId: string): void {
  useQuickCreateActorMemoryStore.getState().clearServer(serverId);
}

/**
 * Persist a successful quick-create only when its original account,
 * workspace and generation are still active — same late-response guard the
 * manual form's last-assignee memory runs
 * (`rememberLastAssigneeAfterSuccessfulCreate`), sharing its generation
 * counter so one `invalidateNewIssueSubmissionContext()` expires both.
 */
export function rememberQuickCreateActorAfterSuccess(
  submitted: NewIssueSubmissionContext,
  current: NewIssueSubmissionContext | null,
  actor: QuickCreateActorRef,
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
  setLastQuickCreateActor(submitted.serverId, submitted.workspaceSlug, actor);
  return true;
}

/**
 * Await AsyncStorage hydration before the memory is read. The seed chain
 * must not run against a pre-hydration empty map: that is the same
 * "looks like no history" fall-through the loading gate in
 * `resolveQuickCreateActor` guards against (RUYI-130).
 */
export async function ensureQuickCreateActorMemoryHydrated(): Promise<void> {
  const store = useQuickCreateActorMemoryStore;
  if (store.persist.hasHydrated()) return;
  await store.persist.rehydrate();
}
