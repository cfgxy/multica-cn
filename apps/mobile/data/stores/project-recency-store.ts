/**
 * RUYI-413 — device-local "which project did I pick last, and when" memory
 * for the project pickers (`project-picker-body`). The picker sorts
 * recently-selected projects to the top so the日常 project is one tap away;
 * a memory that dies with the process re-sorts to plain alphabetical on the
 * next cold start, which reads as "the app forgot what I use".
 *
 * Shape: `${wsId}:${projectId}` → ISO timestamp of the last picker
 * selection. AsyncStorage, not SecureStore: nothing here is a credential —
 * it is an ordering preference only.
 *
 * No logout / server-removal cleanup, unlike quick-create's last-actor
 * memory: a stale entry can never change a rendered value — a project id
 * that no longer resolves simply never matches a listed project, so the
 * worst case is dead bytes in one JSON blob.
 *
 * `recordProjectSelection` is awaitable on purpose and resolves only after
 * AsyncStorage accepted the write (same RUYI-130 rationale as
 * quick-create-prefs-store: an unawaited write is indistinguishable from a
 * flushed one until the process dies).
 */
import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import AsyncStorage from "@react-native-async-storage/async-storage";

/** Minimal shape the recency sort needs (Project satisfies this). */
export interface RecencySortableProject {
  id: string;
  title: string;
}

export function projectRecencyKey(wsId: string, projectId: string): string {
  return `${wsId}:${projectId}`;
}

interface ProjectRecencyState {
  /** `${wsId}:${projectId}` → ISO timestamp of the last picker selection. */
  lastSelectedAt: Record<string, string>;
  recordSelection: (wsId: string, projectId: string, at?: string) => void;
}

/**
 * zustand's persist middleware wraps `set` so it returns the storage write's
 * promise, but `StoreApi.setState` is declared `void`, so TypeScript hides
 * that promise. Recover it so `recordSelection` is awaitable (same
 * `flushed` rationale as quick-create-prefs-store).
 */
function flushed(setResult: unknown): Promise<void> {
  return Promise.resolve(setResult as Promise<unknown> | undefined).then(
    () => undefined,
  );
}

export const useProjectRecencyStore = create<ProjectRecencyState>()(
  persist(
    (set) => ({
      lastSelectedAt: {},
      recordSelection: (wsId, projectId, at) =>
        flushed(
          set((s) => ({
            lastSelectedAt: {
              ...s.lastSelectedAt,
              [projectRecencyKey(wsId, projectId)]:
                at ?? new Date().toISOString(),
            },
          })),
        ),
    }),
    {
      name: "multica_mobile_project_recency",
      storage: createJSONStorage(() => AsyncStorage),
    },
  ),
);

/**
 * Await AsyncStorage hydration before the memory is read or written.
 * zustand's default merge is persisted-state-wins, so a rehydrate landing
 * after a write would silently overwrite the just-recorded timestamp with
 * the stale map (same late-merge hazard quick-create's clear guards).
 */
export async function ensureProjectRecencyHydrated(): Promise<void> {
  if (!useProjectRecencyStore.persist.hasHydrated()) {
    await useProjectRecencyStore.persist.rehydrate();
  }
}

/** Record a picker selection; resolves after the write is on disk. */
export async function recordProjectSelection(
  wsId: string | null,
  projectId: string,
): Promise<void> {
  if (!wsId) return;
  await ensureProjectRecencyHydrated();
  await useProjectRecencyStore.getState().recordSelection(wsId, projectId);
}

/**
 * Picker ordering (RUYI-413): recently-selected projects first, last
 * selection on top; never-selected projects after, alphabetical within
 * each group. ISO timestamps compare correctly as plain strings. Pure —
 * the picker runs it inside useMemo and `project-recency-store.test.ts`
 * pins the contract.
 */
export function sortProjectsByRecency<T extends RecencySortableProject>(
  projects: readonly T[],
  lastSelectedAt: Readonly<Record<string, string>>,
  wsId: string | null,
): T[] {
  const selectedAt = (p: T): string | null =>
    wsId ? (lastSelectedAt[projectRecencyKey(wsId, p.id)] ?? null) : null;
  const byTitle = (a: T, b: T) => a.title.localeCompare(b.title);
  return [...projects].sort((a, b) => {
    const ta = selectedAt(a);
    const tb = selectedAt(b);
    if (ta && tb) return tb.localeCompare(ta) || byTitle(a, b);
    if (ta) return -1;
    if (tb) return 1;
    return byTitle(a, b);
  });
}
