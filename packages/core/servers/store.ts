/**
 * Server configuration store — Zustand, following the options-injected
 * creation + registration pattern of the auth store, and the persisted
 * payload rules of the accepted mobile server store: write storage FIRST,
 * then memory, so a failed write never splits what the UI shows from what
 * a restart restores.
 *
 * The built-in entry is synthesized by the host app (desktop: from its
 * runtime config) and never persisted. Unlike mobile there is no
 * setActiveServer here: on desktop a switch is a storage-level swap plus a
 * window reload (see ./session-snapshot), not an in-process transition.
 */
import { create } from "zustand";
import type { StorageAdapter } from "../types";
import {
  BUILT_IN_SERVER_ID,
  SERVERS_STORAGE_KEY,
  composeServerList,
  normalizeUrl,
  parsePersistedState,
  pickActiveServer,
  serializePersistedState,
  type ServerEntry,
} from "./server-config";
import { clearServerToken } from "./session-snapshot";

export interface NewServerInput {
  name: string;
  apiUrl: string;
  webUrl: string | null;
}

export interface ServerStoreOptions {
  storage: StorageAdapter;
  builtIn: ServerEntry;
}

export interface ServerState {
  servers: ServerEntry[];
  activeServerId: string;
  /** True once hydrate() ran. UI renders the switcher only after it. */
  hydrated: boolean;
  hydrate: () => void;
  addServer: (input: NewServerInput) => ServerEntry;
  updateServer: (id: string, input: NewServerInput) => void;
  removeServer: (id: string) => void;
}

function createServerId(): string {
  const rand = Math.random().toString(36).slice(2, 10);
  const ts = Date.now().toString(36);
  return `srv_${rand}${ts}`;
}

export function createServerStore(options: ServerStoreOptions) {
  const { storage, builtIn } = options;

  return create<ServerState>((set, get) => {
    /** Persist + memory write, the single outlet of every mutation. */
    const commit = (servers: ServerEntry[], activeServerId: string) => {
      storage.setItem(
        SERVERS_STORAGE_KEY,
        serializePersistedState(servers, activeServerId),
      );
      set({ servers, activeServerId });
    };

    return {
      servers: composeServerList(builtIn, []),
      activeServerId: BUILT_IN_SERVER_ID,
      hydrated: false,

      hydrate: () => {
        let raw: string | null = null;
        try {
          raw = storage.getItem(SERVERS_STORAGE_KEY);
        } catch {
          // Unreadable storage counts as "no custom configuration" — same
          // behavior as a fresh install.
          raw = null;
        }
        const persisted = parsePersistedState(raw);
        const servers = composeServerList(builtIn, persisted?.servers ?? []);
        // A dangling active id falls back to the built-in entry so the app
        // never boots into a server that no longer exists.
        const active = pickActiveServer(
          servers,
          persisted?.activeServerId ?? BUILT_IN_SERVER_ID,
        );
        set({ servers, activeServerId: active.id, hydrated: true });
      },

      addServer: (input) => {
        const entry: ServerEntry = {
          id: createServerId(),
          name: input.name.trim(),
          apiUrl: normalizeUrl(input.apiUrl),
          webUrl: input.webUrl ? normalizeUrl(input.webUrl) : null,
          builtIn: false,
        };
        const { servers, activeServerId } = get();
        commit([...servers, entry], activeServerId);
        return entry;
      },

      updateServer: (id, input) => {
        const { servers, activeServerId } = get();
        commit(
          servers.map((s) =>
            s.id === id && !s.builtIn
              ? {
                  ...s,
                  name: input.name.trim(),
                  apiUrl: normalizeUrl(input.apiUrl),
                  webUrl: input.webUrl ? normalizeUrl(input.webUrl) : null,
                }
              : s,
          ),
          activeServerId,
        );
      },

      removeServer: (id) => {
        const { servers, activeServerId } = get();
        // The active entry and the built-in entry are never deletable — the
        // UI hides those affordances, this guards future call sites from
        // switching the app to a "no server" state implicitly.
        if (id === activeServerId || id === BUILT_IN_SERVER_ID) return;
        const removed = servers.find((s) => s.id === id);
        if (!removed) return;
        commit(
          servers.filter((s) => s.id !== id),
          activeServerId,
        );
        // The deleted entry's session snapshot is an orphaned credential —
        // clear it after the commit, matching the mobile ordering: if the
        // write throws, the entry and its snapshot stay consistent.
        clearServerToken(storage, id);
      },
    };
  });
}
