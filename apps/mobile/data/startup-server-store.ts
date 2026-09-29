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

export const useStartupServerStore = create<StartupServerState>((set) => ({
  phase: "checking",
  previousId: null,
  begin: async () => {
    const previousId = await useServerStore.getState().hydrate();
    const target = getStartupServerTarget(useServerStore.getState().servers, previousId);
    set({ previousId: target ?? null, phase: target === undefined ? "ready" : "select" });
  },
  connect: async (serverId) => {
    const store = useServerStore.getState();
    if (!store.servers.some((server) => server.id === serverId)) return;
    await store.setActiveServer(serverId);
    set({ previousId: null, phase: "ready" });
  },
}));
