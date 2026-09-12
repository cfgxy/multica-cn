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
 *
 * Every mutation and the hydration helper are awaitable on purpose: an
 * unawaited AsyncStorage write is indistinguishable from a flushed one until
 * the process dies, which is the exact failure this memory exists to
 * prevent. See `flushed` for why TypeScript hides that promise.
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
  ) => Promise<void>;
  clearServer: (serverId: string) => Promise<void>;
}

/**
 * zustand's persist middleware wraps `set` so it returns the storage write's
 * promise, but `StoreApi.setState` is declared `void`, so TypeScript hides
 * that promise and an unawaited write looks exactly like a flushed one
 * (zustand 5.0.12, `esm/middleware.mjs` — `set(...args); return setItem()`).
 * Recover it so every mutation here is awaitable: a memory that is still
 * only in RAM when the OS kills the app is precisely the defect this store
 * exists to fix (RUYI-130).
 */
function flushed(setResult: unknown): Promise<void> {
  return Promise.resolve(setResult as Promise<unknown> | undefined).then(
    () => undefined,
  );
}

export const useQuickCreateActorMemoryStore =
  create<QuickCreateActorMemoryState>()(
    persist(
      (set) => ({
        byServer: {},
        setLastActor: (serverId, slug, actor) =>
          flushed(
            set((s) => ({
              byServer: {
                ...s.byServer,
                [serverId]: { ...s.byServer[serverId], [slug]: actor },
              },
            })),
          ),
        clearServer: (serverId) =>
          flushed(
            set((s) => {
              if (!(serverId in s.byServer)) return s;
              const { [serverId]: _removed, ...rest } = s.byServer;
              return { byServer: rest };
            }),
          ),
      }),
      {
        name: "multica_mobile_quick_create_last_actor",
        storage: createJSONStorage(() => AsyncStorage),
      },
    ),
  );

/**
 * Hydration outcome of the persisted memory, as reactive state.
 *
 * `persist.hasHydrated()` is a plain getter — it never notifies React — and
 * zustand 5.0.12 swallows a storage read error inside `hydrate()`: the
 * promise `rehydrate()` returns resolves either way and only `hasHydrated()`
 * separates success from failure. A failed read that is mistaken for
 * "hydrated, no history" re-seeds the first visible agent, which is the
 * original RUYI-130 symptom, so the failure is an explicit state that both
 * the panel and the picker can refuse to seed from.
 */
export type QuickCreateActorMemoryHydration = "pending" | "ready" | "failed";

interface QuickCreateActorMemoryHydrationState {
  status: QuickCreateActorMemoryHydration;
  setStatus: (next: QuickCreateActorMemoryHydration) => void;
}

export const useQuickCreateActorMemoryHydrationStore =
  create<QuickCreateActorMemoryHydrationState>((set) => ({
    status: useQuickCreateActorMemoryStore.persist.hasHydrated()
      ? "ready"
      : "pending",
    setStatus: (next) => set({ status: next }),
  }));

/** Last actor submitted from `slug` on `serverId`, or null = no history. */
export function getLastQuickCreateActor(
  serverId: string,
  slug: string,
): QuickCreateActorRef | null {
  return (
    useQuickCreateActorMemoryStore.getState().byServer[serverId]?.[slug] ?? null
  );
}

/**
 * Record the actor submitted with a successful quick-create. Resolves only
 * after AsyncStorage accepted the write, so callers can hold the flow open
 * until the memory would survive the process dying.
 */
export function setLastQuickCreateActor(
  serverId: string,
  slug: string,
  actor: QuickCreateActorRef,
): Promise<void> {
  return useQuickCreateActorMemoryStore
    .getState()
    .setLastActor(serverId, slug, actor);
}

/**
 * Drop the whole server subtree — logout / server removal. Resolves after
 * the removal is on disk; an unawaited clear can be outlived by the session
 * it was meant to erase.
 *
 * Hydration is awaited first: zustand's default merge is
 * persisted-state-wins, so a rehydrate landing after the clear would write
 * the old subtree straight back over it.
 */
export async function clearQuickCreateActorMemory(
  serverId: string,
): Promise<void> {
  await ensureQuickCreateActorMemoryHydrated();
  await useQuickCreateActorMemoryStore.getState().clearServer(serverId);
}

/**
 * Persist a successful quick-create only when its original account,
 * workspace and generation are still active — same late-response guard the
 * manual form's last-assignee memory runs
 * (`rememberLastAssigneeAfterSuccessfulCreate`), sharing its generation
 * counter so one `invalidateNewIssueSubmissionContext()` expires both.
 */
export async function rememberQuickCreateActorAfterSuccess(
  submitted: NewIssueSubmissionContext,
  current: NewIssueSubmissionContext | null,
  actor: QuickCreateActorRef,
): Promise<boolean> {
  if (
    !current ||
    submitted.serverId !== current.serverId ||
    submitted.workspaceSlug !== current.workspaceSlug ||
    submitted.userId !== current.userId ||
    submitted.generation !== current.generation
  ) {
    return false;
  }
  await setLastQuickCreateActor(
    submitted.serverId,
    submitted.workspaceSlug,
    actor,
  );
  return true;
}

/**
 * Await AsyncStorage hydration before the memory is read. The seed chain
 * must not run against a pre-hydration empty map: that is the same
 * "looks like no history" fall-through the loading gate in
 * `resolveQuickCreateActor` guards against (RUYI-130).
 *
 * Returns the resolved status instead of `void`. zustand 5.0.12 catches a
 * storage read error inside `hydrate()` and leaves `hasHydrated` false
 * without rejecting, so awaiting `rehydrate()` alone cannot tell a read
 * failure from an empty map — and treating the failure as "no history"
 * re-seeds the first visible agent.
 */
export async function ensureQuickCreateActorMemoryHydrated(): Promise<
  Exclude<QuickCreateActorMemoryHydration, "pending">
> {
  const store = useQuickCreateActorMemoryStore;
  const setStatus = useQuickCreateActorMemoryHydrationStore.getState().setStatus;
  if (!store.persist.hasHydrated()) {
    await store.persist.rehydrate();
  }
  const status = store.persist.hasHydrated() ? "ready" : "failed";
  setStatus(status);
  return status;
}
