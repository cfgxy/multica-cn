import { beforeEach, describe, expect, it } from "vitest";
import type { StorageAdapter } from "@multica/core/types";
import { BUILT_IN_SERVER_ID, serverTokenKey } from "@multica/core/servers";
import { DEFAULT_RUNTIME_CONFIG } from "../../../shared/runtime-config";
import {
  ensureServerStore,
  resolveEffectiveRuntimeConfig,
  useServerStore,
} from "./desktop-servers";

// Tests always use fake URLs — never a real instance address or token.
const BUILTIN = { ...DEFAULT_RUNTIME_CONFIG };

function makeStorage(initial: Record<string, string> = {}): StorageAdapter {
  const data = { ...initial };
  return {
    getItem: (k: string) => data[k] ?? null,
    setItem: (k: string, v: string) => {
      data[k] = v;
    },
    removeItem: (k: string) => {
      delete data[k];
    },
  };
}

function persistServers(storage: StorageAdapter, servers: unknown[], activeServerId: string) {
  storage.setItem(
    "multica_servers",
    JSON.stringify({ version: 1, servers, activeServerId }),
  );
}

describe("resolveEffectiveRuntimeConfig", () => {
  it("returns the built-in config when nothing is persisted", () => {
    const storage = makeStorage();
    expect(resolveEffectiveRuntimeConfig(BUILTIN, storage)).toEqual(BUILTIN);
  });

  it("returns the built-in config when the active id is the built-in", () => {
    const storage = makeStorage();
    persistServers(
      storage,
      [{ id: "srv_a", name: "A", apiUrl: "https://a.example.com" }],
      BUILT_IN_SERVER_ID,
    );
    expect(resolveEffectiveRuntimeConfig(BUILTIN, storage)).toEqual(BUILTIN);
  });

  it("overrides with the active custom server and derives ws/app urls", () => {
    const storage = makeStorage();
    persistServers(
      storage,
      [
        { id: "srv_a", name: "A", apiUrl: "https://api.a.example.com" },
      ],
      "srv_a",
    );
    const effective = resolveEffectiveRuntimeConfig(BUILTIN, storage);
    expect(effective.apiUrl).toBe("https://api.a.example.com");
    expect(effective.wsUrl).toBe("wss://api.a.example.com/ws");
    // No explicit webUrl: falls back to the api url.
    expect(effective.appUrl).toBe("https://api.a.example.com");
  });

  it("uses an explicit webUrl as the app url", () => {
    const storage = makeStorage();
    persistServers(
      storage,
      [
        {
          id: "srv_a",
          name: "A",
          apiUrl: "https://api.a.example.com",
          webUrl: "https://a.example.com",
        },
      ],
      "srv_a",
    );
    expect(resolveEffectiveRuntimeConfig(BUILTIN, storage).appUrl).toBe(
      "https://a.example.com",
    );
  });

  it("falls back to the built-in config on a dangling active id", () => {
    const storage = makeStorage();
    persistServers(storage, [], "srv_gone");
    expect(resolveEffectiveRuntimeConfig(BUILTIN, storage)).toEqual(BUILTIN);
  });

  it("falls back to the built-in config on a corrupted payload", () => {
    const storage = makeStorage({ multica_servers: "{broken" });
    expect(resolveEffectiveRuntimeConfig(BUILTIN, storage)).toEqual(BUILTIN);
  });
});

describe("ensureServerStore", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("registers a hydrated store backed by localStorage", () => {
    window.localStorage.setItem(
      "multica_servers",
      JSON.stringify({
        version: 1,
        servers: [{ id: "srv_a", name: "A", apiUrl: "https://a.example.com" }],
        activeServerId: BUILT_IN_SERVER_ID,
      }),
    );
    ensureServerStore(BUILTIN);
    const state = useServerStore.getState();
    expect(state.hydrated).toBe(true);
    expect(state.servers.map((s) => s.id)).toEqual([BUILT_IN_SERVER_ID, "srv_a"]);
    expect(state.activeServerId).toBe(BUILT_IN_SERVER_ID);
  });

  it("is idempotent — the second call keeps the registered store", () => {
    ensureServerStore(BUILTIN);
    const before = useServerStore.getState();
    ensureServerStore(BUILTIN);
    expect(useServerStore.getState()).toBe(before);
  });

  it("picks up a snapshot written for the active server", () => {
    window.localStorage.setItem("multica_token", "tok-b");
    window.localStorage.setItem(serverTokenKey("srv_b"), "tok-b");
    persistServers(
      {
        getItem: (k: string) => window.localStorage.getItem(k),
        setItem: (k: string, v: string) => window.localStorage.setItem(k, v),
        removeItem: (k: string) => window.localStorage.removeItem(k),
      },
      [{ id: "srv_b", name: "B", apiUrl: "https://b.example.com" }],
      "srv_b",
    );
    expect(resolveEffectiveRuntimeConfig(BUILTIN, window.localStorage).apiUrl).toBe(
      "https://b.example.com",
    );
  });
});
