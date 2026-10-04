import { create } from "zustand";
import { getStartupServerTarget } from "./server-config";
import { probeServer } from "./probe-server";
import { useServerStore } from "./server-store";

type StartupPhase = "checking" | "select" | "ready";

interface StartupServerState {
  phase: StartupPhase;
  previousId: string | null;
  begin: () => Promise<void>;
  connect: (serverId: string) => Promise<void>;
}

export const useStartupServerStore = create<StartupServerState>((set, get) => ({
  phase: "checking",
  previousId: null,
  begin: async () => {
    const previousId = await useServerStore.getState().hydrate();
    const target = getStartupServerTarget(useServerStore.getState().servers, previousId);
    if (target === undefined) {
      // Zero or one configured server: nothing to choose — open the gate.
      set({ previousId: null, phase: "ready" });
      return;
    }
    if (target === null) {
      // Multiple servers and no valid previous one: startup cannot proceed
      // without the user, so park at the explicit-selection state. This is
      // a converged terminal state, not an in-flight phase — routing to the
      // picker from here is safe.
      set({ previousId: null, phase: "select" });
      return;
    }
    // RUYI-346 round 3: a resolvable startup target must converge without
    // the selection UI. The picker only mounts through navigation, and any
    // navigation before the gate settles discards a cold-start deep link
    // (QA rounds 1-2: force-start deep links landed on the default Inbox).
    // connect() below only persists the choice locally — it neither sends a
    // request nor rejects when the target is down (RUYI-404) — so probe the
    // target first (bounded by STARTUP_PROBE_TIMEOUT_MS). Unreachable
    // degrades to explicit selection, keeping previousId so the picker's
    // countdown retry fires; only storage errors surface through connect().
    const server = useServerStore.getState().servers.find((s) => s.id === target);
    const reachable =
      server !== undefined &&
      (await probeServer(server.apiUrl, new AbortController().signal));
    if (!reachable) {
      set({ previousId: target, phase: "select" });
      return;
    }
    try {
      await get().connect(target);
    } catch {
      set({ previousId: target, phase: "select" });
    }
  },
  connect: async (serverId) => {
    const store = useServerStore.getState();
    if (!store.servers.some((server) => server.id === serverId)) return;
    await store.setActiveServer(serverId);
    set({ previousId: null, phase: "ready" });
  },
}));
