// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { StorageAdapter } from "../types";
import { BUILT_IN_SERVER_ID, buildBuiltInServer } from "./server-config";
import { serverTokenKey } from "./session-snapshot";
import { createServerStore } from "./store";

const BUILT_IN = buildBuiltInServer("https://api.multica.example", undefined);

function makeStorage(initial: Record<string, string> = {}): StorageAdapter {
  const data = { ...initial };
  return {
    getItem: (k) => data[k] ?? null,
    setItem: (k, v) => {
      data[k] = v;
    },
    removeItem: (k) => {
      delete data[k];
    },
  };
}

function makeStore(storage = makeStorage()) {
  return createServerStore({ storage, builtIn: BUILT_IN });
}

describe("server store", () => {
  it("starts with only the built-in entry and reports unhydrated", () => {
    const store = makeStore();
    expect(store.getState().servers).toEqual([BUILT_IN]);
    expect(store.getState().activeServerId).toBe(BUILT_IN_SERVER_ID);
    expect(store.getState().hydrated).toBe(false);
  });

  it("hydrate restores persisted custom servers and the active id", () => {
    const storage = makeStorage();
    storage.setItem(
      "multica_servers",
      JSON.stringify({
        version: 1,
        servers: [{ id: "srv_a", name: "A", apiUrl: "https://a.example.com" }],
        activeServerId: "srv_a",
      }),
    );
    const store = makeStore(storage);
    store.getState().hydrate();
    const { servers, activeServerId, hydrated } = store.getState();
    expect(servers.map((s) => s.id)).toEqual([BUILT_IN_SERVER_ID, "srv_a"]);
    expect(activeServerId).toBe("srv_a");
    expect(hydrated).toBe(true);
  });

  it("hydrate falls back to the built-in entry on a dangling active id", () => {
    const storage = makeStorage();
    storage.setItem(
      "multica_servers",
      JSON.stringify({
        version: 1,
        servers: [],
        activeServerId: "srv_gone",
      }),
    );
    const store = makeStore(storage);
    store.getState().hydrate();
    expect(store.getState().activeServerId).toBe(BUILT_IN_SERVER_ID);
  });

  it("hydrate survives an unreadable payload", () => {
    const storage = makeStorage();
    storage.setItem("multica_servers", "{broken");
    const store = makeStore(storage);
    store.getState().hydrate();
    expect(store.getState().servers).toEqual([BUILT_IN]);
    expect(store.getState().hydrated).toBe(true);
  });

  it("addServer appends a normalized custom entry and persists it", () => {
    const storage = makeStorage();
    const store = makeStore(storage);
    const entry = store.getState().addServer({
      name: "  Home lab  ",
      apiUrl: "https://home.example.com/",
      webUrl: null,
    });
    expect(entry.name).toBe("Home lab");
    expect(entry.apiUrl).toBe("https://home.example.com");
    expect(entry.builtIn).toBe(false);
    // Storage first, then memory: the raw payload exists even if the caller
    // never touches the in-memory state again.
    expect(JSON.parse(storage.getItem("multica_servers")!).servers).toHaveLength(1);
    expect(store.getState().servers.map((s) => s.id)).toEqual([
      BUILT_IN_SERVER_ID,
      entry.id,
    ]);
  });

  it("addServer keeps the active id untouched", () => {
    const store = makeStore();
    store.getState().addServer({ name: "A", apiUrl: "https://a.example.com", webUrl: null });
    expect(store.getState().activeServerId).toBe(BUILT_IN_SERVER_ID);
  });

  it("updateServer rewrites a custom entry and rejects the built-in", () => {
    const storage = makeStorage();
    const store = makeStore(storage);
    const entry = store.getState().addServer({ name: "A", apiUrl: "https://a.example.com", webUrl: null });

    store.getState().updateServer(entry.id, {
      name: "A2",
      apiUrl: "https://a2.example.com",
      webUrl: "https://a2web.example.com",
    });
    const updated = store.getState().servers.find((s) => s.id === entry.id)!;
    expect(updated.name).toBe("A2");
    expect(updated.apiUrl).toBe("https://a2.example.com");
    expect(updated.webUrl).toBe("https://a2web.example.com");

    expect(() =>
      store.getState().updateServer(BUILT_IN_SERVER_ID, {
        name: "X",
        apiUrl: "https://x.example.com",
        webUrl: null,
      }),
    ).not.toThrow();
    expect(store.getState().servers[0]).toEqual(BUILT_IN);
  });

  it("removeServer deletes a non-active custom entry and its session snapshot", () => {
    const storage = makeStorage({ [serverTokenKey("srv_a")]: "tok-a" });
    const store = makeStore(storage);
    const entry = store.getState().addServer({ name: "A", apiUrl: "https://a.example.com", webUrl: null });
    storage.setItem(serverTokenKey(entry.id), "tok-a");

    store.getState().removeServer(entry.id);
    expect(store.getState().servers).toEqual([BUILT_IN]);
    expect(JSON.parse(storage.getItem("multica_servers")!).servers).toHaveLength(0);
    expect(storage.getItem(serverTokenKey(entry.id))).toBeNull();
  });

  it("removeServer refuses the built-in entry and the active entry", () => {
    const storage = makeStorage();
    const store = makeStore(storage);
    const entry = store.getState().addServer({ name: "A", apiUrl: "https://a.example.com", webUrl: null });
    // Make the custom entry active, as a switch would.
    storage.setItem(
      "multica_servers",
      JSON.stringify({
        version: 1,
        servers: [{ id: entry.id, name: "A", apiUrl: entry.apiUrl }],
        activeServerId: entry.id,
      }),
    );
    store.getState().hydrate();

    store.getState().removeServer(BUILT_IN_SERVER_ID);
    store.getState().removeServer(entry.id);
    expect(store.getState().servers.map((s) => s.id)).toEqual([
      BUILT_IN_SERVER_ID,
      entry.id,
    ]);
  });

  it("removeServer of an unknown id is a no-op", () => {
    const store = makeStore();
    expect(() => store.getState().removeServer("srv_nope")).not.toThrow();
  });
});
