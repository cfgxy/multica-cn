/**
 * Server configuration pure logic — URL normalization/validation, built-in
 * entry synthesis, and persistence payload (de)serialization for the
 * desktop server switcher (RUYI-59).
 *
 * Semantics intentionally mirror the accepted mobile implementation
 * (`apps/mobile/data/server-config.ts`): the same entry shape, the same
 * storage payload version, the same validation and probe rules. Mobile
 * owns its copy per the Sharing Rules (mobile shares only types and pure
 * functions from core, and its module predates this one); if the rules ever
 * change, both copies must move together.
 *
 * The regex-based URL validation is kept instead of the WHATWG URL
 * constructor for the same reason mobile adopted it: identical behavior in
 * every JS runtime, so unit conclusions hold wherever the code runs.
 */

export interface ServerEntry {
  /** uuid; the built-in entry is fixed to BUILT_IN_SERVER_ID */
  id: string;
  /** Display name; the built-in entry is "Multica Official" */
  name: string;
  /** Normalized http(s) address (no trailing slash) */
  apiUrl: string;
  /** Optional; null = fall back to apiUrl (single-domain deployments) */
  webUrl: string | null;
  /** Built-in entry: never editable, never deletable */
  builtIn: boolean;
}

/** Fixed id of the built-in entry. Never present in persisted data. */
export const BUILT_IN_SERVER_ID = "default";

/** Persisted payload version, kept in lockstep with mobile. */
export const SERVER_STORE_VERSION = 1;

/** Storage key for the server list payload. */
export const SERVERS_STORAGE_KEY = "multica_servers";

/** Storage key holding the live auth token (desktop token mode). */
export const TOKEN_STORAGE_KEY = "multica_token";

export interface PersistedServerState {
  version: number;
  /** Custom entries only — the built-in entry is synthesized at boot. */
  servers: ServerEntry[];
  activeServerId: string;
}

/**
 * Strip trailing slashes and surrounding whitespace. API addresses are
 * stored without a trailing slash because every request path starts with
 * `/api/...`.
 */
export function normalizeUrl(raw: string): string {
  return raw.trim().replace(/\/+$/, "");
}

/**
 * Shape constraint for a server base address, deliberately a regex.
 * Constraints: http/https, non-empty host without whitespace or `@`
 * (rejects `https://evil.com@real.com` style confusion), optional port and
 * path prefix, no query or fragment (a base address carrying those is a
 * mis-paste).
 */
const SERVER_URL_PATTERN = /^https?:\/\/[^\s/?#@]+(?:\/[^\s?#]*)?$/i;

/** Validity check: must be http/https with a non-empty host. */
export function isValidServerUrl(raw: string): boolean {
  return SERVER_URL_PATTERN.test(normalizeUrl(raw));
}

/** Plain http:// — allowed, but the UI shows a one-time weak warning. */
export function isPlainHttp(raw: string): boolean {
  const normalized = normalizeUrl(raw);
  if (!isValidServerUrl(normalized)) return false;
  return /^http:\/\//i.test(normalized);
}

/**
 * Duplicate-address guard: the same apiUrl may not appear twice (exclude
 * the entry being edited). Comparison uses normalized values, so
 * `https://x.com/` and `https://x.com` are the same address.
 */
export function findDuplicateServer(
  servers: ServerEntry[],
  apiUrl: string,
  excludeId?: string,
): ServerEntry | undefined {
  const target = normalizeUrl(apiUrl);
  return servers.find((s) => s.id !== excludeId && s.apiUrl === target);
}

/**
 * Synthesize the built-in entry from the app's configured urls. The
 * desktop passes its runtime config (apiUrl + appUrl); never persisted, so
 * a config change on next launch updates the built-in entry automatically.
 */
export function buildBuiltInServer(
  apiUrl: string | undefined,
  webUrl: string | undefined,
): ServerEntry {
  if (!apiUrl) {
    throw new Error(
      "No built-in API url configured — the desktop runtime config must provide apiUrl.",
    );
  }
  return {
    id: BUILT_IN_SERVER_ID,
    name: "Multica Official",
    apiUrl: normalizeUrl(apiUrl),
    webUrl: webUrl ? normalizeUrl(webUrl) : null,
    builtIn: true,
  };
}

/** The built-in entry always sorts first, custom entries follow in join order. */
export function composeServerList(
  builtIn: ServerEntry,
  customServers: ServerEntry[],
): ServerEntry[] {
  return [builtIn, ...customServers.filter((s) => !s.builtIn)];
}

/**
 * Web address resolution, the single funnel: `webUrl ?? apiUrl`. The
 * fallback is never persisted — editing a server's address keeps web
 * jumps following it.
 */
export function resolveWebUrl(entry: ServerEntry): string {
  return entry.webUrl ?? entry.apiUrl;
}

/**
 * Parse a persisted payload. Any malformed shape (bad JSON, unknown
 * version, missing fields) returns null and the caller falls back to the
 * built-in-only initial state.
 */
export function parsePersistedState(
  raw: string | null,
): PersistedServerState | null {
  if (!raw) return null;
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof data !== "object" || data === null) return null;
  const obj = data as Record<string, unknown>;
  if (obj.version !== SERVER_STORE_VERSION) return null;
  if (!Array.isArray(obj.servers)) return null;
  if (typeof obj.activeServerId !== "string" || !obj.activeServerId) return null;

  const servers: ServerEntry[] = [];
  for (const item of obj.servers) {
    const entry = parseServerEntry(item);
    // One corrupt entry is dropped, not the whole payload — the user's
    // other servers keep working.
    if (entry) servers.push(entry);
  }
  return {
    version: SERVER_STORE_VERSION,
    servers,
    activeServerId: obj.activeServerId,
  };
}

function parseServerEntry(item: unknown): ServerEntry | null {
  if (typeof item !== "object" || item === null) return null;
  const o = item as Record<string, unknown>;
  if (typeof o.id !== "string" || !o.id) return null;
  if (o.id === BUILT_IN_SERVER_ID) return null; // built-in never persists
  if (typeof o.apiUrl !== "string" || !isValidServerUrl(o.apiUrl)) return null;
  const webUrl =
    typeof o.webUrl === "string" && isValidServerUrl(o.webUrl)
      ? normalizeUrl(o.webUrl)
      : null;
  return {
    id: o.id,
    name: typeof o.name === "string" ? o.name : "",
    apiUrl: normalizeUrl(o.apiUrl),
    webUrl,
    builtIn: false,
  };
}

/** Build the payload to persist: built-in and derived webUrl never stored. */
export function serializePersistedState(
  servers: ServerEntry[],
  activeServerId: string,
): string {
  const payload: PersistedServerState = {
    version: SERVER_STORE_VERSION,
    servers: servers.filter((s) => !s.builtIn),
    activeServerId,
  };
  return JSON.stringify(payload);
}

/**
 * Pick the effective entry: the one activeServerId points at; when it
 * dangles (e.g. the entry was removed elsewhere) fall back to the built-in
 * entry.
 */
export function pickActiveServer(
  servers: ServerEntry[],
  activeServerId: string,
): ServerEntry {
  const active =
    servers.find((s) => s.id === activeServerId) ??
    servers.find((s) => s.builtIn) ??
    servers[0];
  if (!active) {
    // composeServerList always places the built-in entry first, so an empty
    // list is a caller bug — fail loudly instead of returning undefined.
    throw new Error("pickActiveServer requires a non-empty server list");
  }
  return active;
}

/**
 * Path the "test connection" probe hits. Not `/health`: it is mounted on
 * the server root route and single-domain reverse proxies hand `/` to the
 * web frontend, where `/health` 404s even though the API is fine. `/api/me`
 * is served by the server in every deployment shape and stably returns 401
 * without credentials — the strongest possible "API is alive" signal.
 * Kept identical to the accepted mobile probe.
 */
export const SERVER_PROBE_PATH = "/api/me";

/**
 * Interpret a probe response. 401/403: reached and authenticated by the
 * server — reachable (the probe carries no token, this is expected). 2xx:
 * reachable unless the body is HTML — under single-domain deployments the
 * web frontend may answer unknown paths with 200 HTML, meaning the request
 * never reached the server. Everything else (including 404): unreachable.
 */
export function interpretProbeResponse(
  status: number,
  contentType: string | null,
): boolean {
  if (status === 401 || status === 403) return true;
  if (status < 200 || status >= 300) return false;
  return !/\btext\/html\b/i.test(contentType ?? "");
}
