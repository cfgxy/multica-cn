/**
 * Desktop server-switcher platform wiring (RUYI-59).
 *
 * The desktop's "built-in" server is the runtime config the app booted
 * with (`~/.multica/desktop.json` in packaged builds, VITE_* env in dev).
 * A user-added server overrides it at boot: the effective config is
 * resolved from the persisted server list BEFORE CoreProvider creates the
 * ApiClient, and switching servers swaps session snapshots in storage and
 * reloads the window — the established desktop pattern for rebuilding
 * every boot-time singleton (language switching reloads the same way).
 */
import { defaultStorage } from "@multica/core/platform";
import {
  buildBuiltInServer,
  createServerStore,
  getRegisteredServerStore,
  parsePersistedState,
  pickActiveServer,
  registerServerStore,
  resetActiveServerSession,
  switchActiveServerSync,
  useServerStore as useRegisteredServerStore,
} from "@multica/core/servers";
import type {
  PersistedServerState,
  ServerEntry,
} from "@multica/core/servers";
import type { StorageAdapter } from "@multica/core/types";
import { deriveWsUrl, type RuntimeConfig } from "../../../shared/runtime-config";

/**
 * Resolve the config the app should boot against: the built-in runtime
 * config, or — when the persisted active id points at a custom server —
 * that server's addresses. Unreadable payloads and dangling ids degrade to
 * the built-in config, so a corrupt list can never wedge the boot.
 */
export function resolveEffectiveRuntimeConfig(
  builtin: RuntimeConfig,
  storage: StorageAdapter,
): RuntimeConfig {
  const builtIn = resolveBuiltInEntry(builtin);
  const persisted = parsePersisted(storage);
  const servers = persisted
    ? [builtIn, ...persisted.servers.filter((s) => !s.builtIn)]
    : [builtIn];
  const active = pickActiveServer(servers, persisted?.activeServerId ?? "default");
  if (active.id === builtIn.id) return builtin;
  return {
    schemaVersion: 1,
    apiUrl: active.apiUrl,
    wsUrl: deriveWsUrl(active.apiUrl),
    appUrl: active.webUrl ?? active.apiUrl,
  };
}

let storeRegistered = false;

/** Idempotently create + register + hydrate the server store for the UI. */
export function ensureServerStore(
  builtin: RuntimeConfig,
  storage: StorageAdapter = defaultStorage,
): void {
  if (storeRegistered) return;
  storeRegistered = true;
  registerServerStore(
    createServerStore({
      storage,
      builtIn: resolveBuiltInEntry(builtin),
    }),
  );
  getRegisteredServerStore().getState().hydrate();
}

/**
 * Storage-level server switch. The caller must reload the window when this
 * returns true (see switchActiveServerSync for the ordering guarantees).
 */
export function switchToServer(
  targetId: string,
  storage: StorageAdapter = defaultStorage,
): boolean {
  return switchActiveServerSync(storage, targetId);
}

/** Invalidate the active server's session (edit-active-address flow). */
export function resetActiveServerSessionStorage(
  storage: StorageAdapter = defaultStorage,
): void {
  resetActiveServerSession(storage);
}

function resolveBuiltInEntry(builtin: RuntimeConfig): ServerEntry {
  return buildBuiltInServer(builtin.apiUrl, builtin.appUrl);
}

function parsePersisted(storage: StorageAdapter): PersistedServerState | null {
  try {
    return parsePersistedState(storage.getItem("multica_servers"));
  } catch {
    return null;
  }
}

/** Test/instance accessor — the registered singleton store. */
export const useServerStore = useRegisteredServerStore;
