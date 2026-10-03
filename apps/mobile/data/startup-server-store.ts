import { create } from "zustand";
import { getStartupServerTarget } from "./server-config";
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
    // Hold at "checking" while connecting; on failure degrade to explicit
    // selection, keeping previousId so the picker's countdown retry fires.
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
