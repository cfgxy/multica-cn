// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { StorageAdapter } from "../types";
import {
  BUILT_IN_SERVER_ID,
  SERVER_STORE_VERSION,
  TOKEN_STORAGE_KEY,
  serializePersistedState,
} from "./server-config";
import {
  activateServerToken,
  captureActiveServerToken,
  clearServerToken,
  resetActiveServerSession,
  serverTokenKey,
  switchActiveServerSync,
} from "./session-snapshot";

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

function persistServers(
  storage: StorageAdapter,
  servers: { id: string; apiUrl: string; name?: string }[],
  activeServerId: string,
) {
  storage.setItem(
    "multica_servers",
    serializePersistedState(
      servers.map((s) => ({
        id: s.id,
        name: s.name ?? s.id,
        apiUrl: s.apiUrl,
        webUrl: null,
        builtIn: false,
      })),
      activeServerId,
    ),
  );
}

const B = { id: "srv_b", apiUrl: "https://b.example.com" };

describe("serverTokenKey", () => {
  it("namespaces by server id", () => {
    expect(serverTokenKey("srv_b")).toBe("multica_server_token_srv_b");
  });
});

describe("captureActiveServerToken", () => {
  it("snapshots the live token under the outgoing server's key", () => {
    const storage = makeStorage({ [TOKEN_STORAGE_KEY]: "tok-a" });
    captureActiveServerToken(storage, "srv_a");
    expect(storage.getItem(serverTokenKey("srv_a"))).toBe("tok-a");
    expect(storage.getItem(TOKEN_STORAGE_KEY)).toBe("tok-a");
  });

  it("clears the snapshot when there is no live token (logged out)", () => {
    const storage = makeStorage({ [serverTokenKey("srv_a")]: "stale" });
    captureActiveServerToken(storage, "srv_a");
    expect(storage.getItem(serverTokenKey("srv_a"))).toBeNull();
  });
});

describe("activateServerToken", () => {
  it("restores the target server's snapshot into the live key", () => {
    const storage = makeStorage({
      [TOKEN_STORAGE_KEY]: "tok-a",
      [serverTokenKey("srv_b")]: "tok-b",
    });
    activateServerToken(storage, "srv_b");
    expect(storage.getItem(TOKEN_STORAGE_KEY)).toBe("tok-b");
  });

  it("removes the live token when the target has no snapshot", () => {
    const storage = makeStorage({ [TOKEN_STORAGE_KEY]: "tok-a" });
    activateServerToken(storage, "srv_b");
    expect(storage.getItem(TOKEN_STORAGE_KEY)).toBeNull();
  });
});

describe("clearServerToken", () => {
  it("drops the per-server snapshot", () => {
    const storage = makeStorage({ [serverTokenKey("srv_b")]: "tok-b" });
    clearServerToken(storage, "srv_b");
    expect(storage.getItem(serverTokenKey("srv_b"))).toBeNull();
  });
});

describe("switchActiveServerSync", () => {
  it("persists the new active id first, then swaps session snapshots", () => {
    const storage = makeStorage({
      [TOKEN_STORAGE_KEY]: "tok-a",
      [serverTokenKey("srv_b")]: "tok-b",
    });
    persistServers(storage, [B], BUILT_IN_SERVER_ID);

    const ok = switchActiveServerSync(storage, "srv_b");
    expect(ok).toBe(true);
    // Active id moved to the target.
    const persisted = JSON.parse(storage.getItem("multica_servers")!);
    expect(persisted.activeServerId).toBe("srv_b");
    // Outgoing server's session was captured; target's restored.
    expect(storage.getItem(serverTokenKey(BUILT_IN_SERVER_ID))).toBe("tok-a");
    expect(storage.getItem(TOKEN_STORAGE_KEY)).toBe("tok-b");
  });

  it("moving to a server without a snapshot signs out of the live key", () => {
    const storage = makeStorage({ [TOKEN_STORAGE_KEY]: "tok-a" });
    persistServers(storage, [B], BUILT_IN_SERVER_ID);

    expect(switchActiveServerSync(storage, "srv_b")).toBe(true);
    expect(storage.getItem(TOKEN_STORAGE_KEY)).toBeNull();
    expect(storage.getItem(serverTokenKey(BUILT_IN_SERVER_ID))).toBe("tok-a");
  });

  it("rejects an unknown target without touching anything", () => {
    const storage = makeStorage({ [TOKEN_STORAGE_KEY]: "tok-a" });
    persistServers(storage, [B], BUILT_IN_SERVER_ID);

    expect(switchActiveServerSync(storage, "srv_missing")).toBe(false);
    expect(storage.getItem(TOKEN_STORAGE_KEY)).toBe("tok-a");
    expect(storage.getItem(serverTokenKey(BUILT_IN_SERVER_ID))).toBeNull();
    const persisted = JSON.parse(storage.getItem("multica_servers")!);
    expect(persisted.activeServerId).toBe(BUILT_IN_SERVER_ID);
  });

  it("captures under the built-in id when nothing is persisted yet", () => {
    const storage = makeStorage({ [TOKEN_STORAGE_KEY]: "tok-a" });
    persistServers(storage, [B], BUILT_IN_SERVER_ID);
    // No multica_servers payload at all:
    const bare = makeStorage({ [TOKEN_STORAGE_KEY]: "tok-a" });
    expect(switchActiveServerSync(bare, "srv_b")).toBe(false);
  });

  it("switches between two custom entries, snapshotting each way", () => {
    const storage = makeStorage({
      [TOKEN_STORAGE_KEY]: "tok-a",
      [serverTokenKey("srv_b")]: "tok-b",
    });
    persistServers(
      storage,
      [
        { id: "srv_a", apiUrl: "https://a.example.com" },
        { id: "srv_b", apiUrl: "https://b.example.com" },
      ],
      "srv_a",
    );

    expect(switchActiveServerSync(storage, "srv_b")).toBe(true);
    // A's live session captured, B's restored, active id moved.
    expect(storage.getItem(serverTokenKey("srv_a"))).toBe("tok-a");
    expect(storage.getItem(TOKEN_STORAGE_KEY)).toBe("tok-b");
    const persisted = JSON.parse(storage.getItem("multica_servers")!);
    expect(persisted.activeServerId).toBe("srv_b");
  });

  it("switches BACK to the built-in server (RUYI-59 review blocker)", () => {
    const storage = makeStorage({
      [TOKEN_STORAGE_KEY]: "tok-b",
      [serverTokenKey(BUILT_IN_SERVER_ID)]: "tok-default",
    });
    persistServers(storage, [B], "srv_b");

    expect(switchActiveServerSync(storage, BUILT_IN_SERVER_ID)).toBe(true);
    // The custom server's session is captured for a future switch back.
    expect(storage.getItem(serverTokenKey("srv_b"))).toBe("tok-b");
    // The built-in server's snapshot becomes the live token.
    expect(storage.getItem(TOKEN_STORAGE_KEY)).toBe("tok-default");
    // The built-in entry never persists; the active id points at it.
    const persisted = JSON.parse(storage.getItem("multica_servers")!);
    expect(persisted.servers.map((s: { id: string }) => s.id)).toEqual(["srv_b"]);
    expect(persisted.activeServerId).toBe(BUILT_IN_SERVER_ID);
  });

  it("switches back to the built-in server without a saved snapshot", () => {
    const storage = makeStorage({ [TOKEN_STORAGE_KEY]: "tok-b" });
    persistServers(storage, [B], "srv_b");

    expect(switchActiveServerSync(storage, BUILT_IN_SERVER_ID)).toBe(true);
    // Built-in has no snapshot → signed out of the live key.
    expect(storage.getItem(TOKEN_STORAGE_KEY)).toBeNull();
    expect(storage.getItem(serverTokenKey("srv_b"))).toBe("tok-b");
    const persisted = JSON.parse(storage.getItem("multica_servers")!);
    expect(persisted.activeServerId).toBe(BUILT_IN_SERVER_ID);
  });

  it("keeps the payload version stable across a switch", () => {
    const storage = makeStorage({});
    persistServers(storage, [B], BUILT_IN_SERVER_ID);
    switchActiveServerSync(storage, "srv_b");
    const persisted = JSON.parse(storage.getItem("multica_servers")!);
    expect(persisted.version).toBe(SERVER_STORE_VERSION);
  });
});

describe("resetActiveServerSession", () => {
  it("clears the live token and the active server's snapshot", () => {
    const storage = makeStorage({
      [TOKEN_STORAGE_KEY]: "tok-a",
      [serverTokenKey("srv_a")]: "tok-a",
    });
    persistServers(storage, [B], "srv_a");

    resetActiveServerSession(storage);
    expect(storage.getItem(TOKEN_STORAGE_KEY)).toBeNull();
    expect(storage.getItem(serverTokenKey("srv_a"))).toBeNull();
  });

  it("is a no-op safe call when nothing is persisted", () => {
    const storage = makeStorage({ [TOKEN_STORAGE_KEY]: "tok-a" });
    resetActiveServerSession(storage);
    expect(storage.getItem(TOKEN_STORAGE_KEY)).toBeNull();
  });
});
