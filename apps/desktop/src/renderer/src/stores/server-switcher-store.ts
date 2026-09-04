import { create } from "zustand";
import type { ServerEntry } from "@multica/core/servers";

/**
 * Server switcher UI state (RUYI-59).
 *
 * The workspace switcher dropdown lives inside a DropdownMenu portal that
 * unmounts when the menu closes, so the dialogs it opens (switch
 * confirmation, manage servers) must be owned by a store and rendered by a
 * host mounted OUTSIDE the menu — the same decoupling the shared modal
 * store provides for workspace-scoped dialogs, kept desktop-local because
 * only desktop has multiple servers.
 */
interface ServerSwitcherStore {
  manageOpen: boolean;
  /** Server awaiting switch confirmation; null = no dialog. */
  pendingSwitch: ServerEntry | null;
  /** Editing target for the manage dialog; "new" = create form. */
  editingServerId: string | null;
  openManage: (editingServerId?: string | null) => void;
  closeManage: () => void;
  requestSwitch: (entry: ServerEntry) => void;
  clearPendingSwitch: () => void;
}

export const useServerSwitcherStore = create<ServerSwitcherStore>((set) => ({
  manageOpen: false,
  pendingSwitch: null,
  editingServerId: null,
  openManage: (editingServerId = null) =>
    set({ manageOpen: true, editingServerId }),
  closeManage: () => set({ manageOpen: false, editingServerId: null }),
  requestSwitch: (entry) => set({ pendingSwitch: entry }),
  clearPendingSwitch: () => set({ pendingSwitch: null }),
}));
