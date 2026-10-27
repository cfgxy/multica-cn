// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  BUILT_IN_SERVER_ID,
  SERVER_STORE_VERSION,
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
  type ServerEntry,
} from "./server-config";

// Tests always use fake URLs — never a real instance address or token.
const builtIn: ServerEntry = {
  id: BUILT_IN_SERVER_ID,
  name: "Multica Official",
  apiUrl: "https://api.multica.example",
  webUrl: null,
  builtIn: true,
};

describe("normalizeUrl", () => {
  it("trims whitespace and trailing slashes", () => {
    expect(normalizeUrl("  https://api.example.com/  ")).toBe(
      "https://api.example.com",
    );
    expect(normalizeUrl("https://api.example.com///")).toBe(
      "https://api.example.com",
    );
  });

  it("keeps a path prefix", () => {
    expect(normalizeUrl("https://example.com/multica/")).toBe(
      "https://example.com/multica",
    );
  });
});

describe("isValidServerUrl", () => {
  it.each([
    "https://api.example.com",
    "http://localhost:8080",
    "https://example.com/multica",
    "http://192.168.1.10:8080",
  ])("accepts %s", (url) => {
    expect(isValidServerUrl(url)).toBe(true);
  });

  it.each([
    "",
    "not a url",
    "ftp://api.example.com",
    "api.example.com",
    "https://evil.com@real.com",
    "https://api.example.com/?x=1",
    "https://api.example.com/#frag",
    "https://spaces .example.com",
  ])("rejects %s", (url) => {
    expect(isValidServerUrl(url)).toBe(false);
  });
});

describe("isPlainHttp", () => {
  it("flags plain http targets", () => {
    expect(isPlainHttp("http://localhost:8080")).toBe(true);
  });

  it("does not flag https", () => {
    expect(isPlainHttp("https://api.example.com")).toBe(false);
  });

  it("does not flag invalid urls", () => {
    expect(isPlainHttp("nope")).toBe(false);
  });
});

describe("findDuplicateServer", () => {
  const servers: ServerEntry[] = [
    builtIn,
    {
      id: "srv_a",
      name: "Home lab",
      apiUrl: "https://home.example.com",
      webUrl: null,
      builtIn: false,
    },
  ];

  it("finds a duplicate by normalized url", () => {
    expect(findDuplicateServer(servers, "https://home.example.com/")).toEqual(
      servers[1],
    );
  });

  it("excludes the entry being edited", () => {
    expect(
      findDuplicateServer(servers, "https://home.example.com", "srv_a"),
    ).toBeUndefined();
  });
});

describe("buildBuiltInServer", () => {
  it("synthesizes the built-in entry from configured urls", () => {
    const entry = buildBuiltInServer(
      "https://api.multica.example/",
      "https://multica.example",
    );
    expect(entry).toEqual({
      id: BUILT_IN_SERVER_ID,
      name: "Multica Official",
      apiUrl: "https://api.multica.example",
      webUrl: "https://multica.example",
      builtIn: true,
    });
  });

  it("falls back webUrl to null when unset", () => {
    const entry = buildBuiltInServer("https://api.multica.example", undefined);
    expect(entry.webUrl).toBeNull();
  });

  it("throws when the api url is missing", () => {
    expect(() => buildBuiltInServer(undefined, undefined)).toThrow();
  });
});

describe("composeServerList", () => {
  it("keeps the built-in entry first and drops stray built-in clones", () => {
    const list = composeServerList(builtIn, [
      { id: "srv_a", name: "A", apiUrl: "https://a.example.com", webUrl: null, builtIn: false },
      // A malformed persisted payload could carry a built-in clone; it must
      // never surface twice.
      { ...builtIn, id: "srv_b", builtIn: true },
    ]);
    expect(list.map((s) => s.id)).toEqual([BUILT_IN_SERVER_ID, "srv_a"]);
  });
});

describe("resolveWebUrl", () => {
  it("prefers the explicit web url", () => {
    expect(resolveWebUrl({ ...builtIn, webUrl: "https://web.example.com" })).toBe(
      "https://web.example.com",
    );
  });

  it("falls back to the api url", () => {
    expect(resolveWebUrl(builtIn)).toBe(builtIn.apiUrl);
  });
});

describe("persisted state round-trip", () => {
  const custom: ServerEntry = {
    id: "srv_a",
    name: "Home lab",
    apiUrl: "https://home.example.com",
    webUrl: null,
    builtIn: false,
  };

  it("drops built-in and derived webUrl entries when serializing", () => {
    const raw = serializePersistedState(
      [builtIn, custom, { ...custom, id: "srv_b", webUrl: "https://b.example.com" }],
      "srv_b",
    );
    const parsed = parsePersistedState(raw);
    expect(parsed?.servers.map((s) => s.id)).toEqual(["srv_a", "srv_b"]);
    expect(parsed?.servers[1]?.webUrl).toBe("https://b.example.com");
    expect(parsed?.activeServerId).toBe("srv_b");
    expect(parsed?.version).toBe(SERVER_STORE_VERSION);
  });

  it("returns null for malformed payloads", () => {
    expect(parsePersistedState(null)).toBeNull();
    expect(parsePersistedState("not json")).toBeNull();
    expect(parsePersistedState('{"version":99}')).toBeNull();
    expect(parsePersistedState('{"version":1}')).toBeNull();
    expect(
      parsePersistedState(JSON.stringify({ version: 1, servers: "nope", activeServerId: "x" })),
    ).toBeNull();
  });

  it("drops only the corrupt entries, not the whole payload", () => {
    const raw = JSON.stringify({
      version: SERVER_STORE_VERSION,
      servers: [
        custom,
        { id: "srv_bad", apiUrl: "not a url" },
        { id: BUILT_IN_SERVER_ID, apiUrl: "https://x.example.com" },
      ],
      activeServerId: "srv_a",
    });
    const parsed = parsePersistedState(raw);
    expect(parsed?.servers.map((s) => s.id)).toEqual(["srv_a"]);
  });

  it("normalizes urls on parse", () => {
    const raw = JSON.stringify({
      version: SERVER_STORE_VERSION,
      servers: [{ id: "srv_a", name: "A", apiUrl: "https://a.example.com/" }],
      activeServerId: "srv_a",
    });
    expect(parsePersistedState(raw)?.servers[0]?.apiUrl).toBe(
      "https://a.example.com",
    );
  });
});

describe("pickActiveServer", () => {
  const custom: ServerEntry = {
    id: "srv_a",
    name: "A",
    apiUrl: "https://a.example.com",
    webUrl: null,
    builtIn: false,
  };

  it("selects the active entry", () => {
    expect(pickActiveServer([builtIn, custom], "srv_a")).toBe(custom);
  });

  it("falls back to the built-in entry when the id dangles", () => {
    expect(pickActiveServer([builtIn, custom], "srv_gone")).toBe(builtIn);
  });

  it("falls back to the first entry without a built-in", () => {
    expect(pickActiveServer([custom], "srv_gone")).toBe(custom);
  });
});

describe("interpretProbeResponse", () => {
  it("treats 401/403 as reachable without credentials", () => {
    expect(interpretProbeResponse(401, null)).toBe(true);
    expect(interpretProbeResponse(403, null)).toBe(true);
  });

  it("treats 2xx JSON as reachable", () => {
    expect(interpretProbeResponse(200, "application/json")).toBe(true);
  });

  it("treats 2xx HTML as NOT reachable (frontend catch-all)", () => {
    expect(interpretProbeResponse(200, "text/html; charset=utf-8")).toBe(false);
  });

  it("treats everything else as unreachable", () => {
    expect(interpretProbeResponse(404, null)).toBe(false);
    expect(interpretProbeResponse(500, "application/json")).toBe(false);
  });
});
