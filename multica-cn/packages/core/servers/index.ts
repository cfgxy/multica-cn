export {
  BUILT_IN_SERVER_ID,
  SERVER_PROBE_PATH,
  SERVER_STORE_VERSION,
  SERVERS_STORAGE_KEY,
  TOKEN_STORAGE_KEY,
  buildBuiltInServer,
  composeServerList,
  findDuplicateServer,
  interpretProbeResponse,
  isPlainHttp,
  isValidServerUrl,
  normalizeUrl,
  parsePersistedState,
  pickActiveServer,
  resolveWebUrl,
  serializePersistedState,
} from "./server-config";
export type {
  PersistedServerState,
  ServerEntry,
} from "./server-config";
export {
  activateServerToken,
  captureActiveServerToken,
  clearServerToken,
  resetActiveServerSession,
  serverTokenKey,
  switchActiveServerSync,
} from "./session-snapshot";
export { createServerStore } from "./store";
export type { NewServerInput, ServerState, ServerStoreOptions } from "./store";

import type { createServerStore as CreateServerStoreFn } from "./store";

type ServerStoreInstance = ReturnType<typeof CreateServerStoreFn>;

/** Module-level singleton — created and registered by the host app at boot. */
let _store: ServerStoreInstance | null = null;

/** Register the server store instance created by the app. */
export function registerServerStore(store: ServerStoreInstance) {
  _store = store;
}

/** Returns the registered instance. Throws if the app has not set it up. */
export function getRegisteredServerStore(): ServerStoreInstance {
  if (!_store) {
    throw new Error(
      "Server store not initialised — call registerServerStore() first",
    );
  }
  return _store;
}

/**
 * Singleton accessor — a Zustand hook backed by the registered instance.
 * Supports `useServerStore(selector)` and `useServerStore.getState()`.
 */
export const useServerStore: ServerStoreInstance = new Proxy(
  (() => {}) as unknown as ServerStoreInstance,
  {
    apply(_target, _thisArg, args) {
      return (
        getRegisteredServerStore() as unknown as (...a: unknown[]) => unknown
      )(...args);
    },
    get(_target, prop) {
      // Allow property inspection (HMR/React Refresh) before registration
      if (!_store) return undefined;
      return Reflect.get(_store, prop);
    },
  },
);
