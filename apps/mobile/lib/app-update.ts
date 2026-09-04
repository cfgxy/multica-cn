/**
 * Serverless app-update check (RUYI-36).
 *
 * Owner constraint: no self-hosted update server. The single source of
 * truth is the latest GitHub Release of cfgxy/multica (a public repo, so
 * the REST endpoint is anonymously readable — verified by inspection on
 * the task's fact card). The check classifies every outcome into one of
 * three UI states; NOTHING here may throw across the UI boundary:
 *
 *   up_to_date        latest release <= installed version
 *   update_available  latest release > installed version, with an APK asset
 *   failed + reason   network / rate-limited / no release / no APK asset /
 *                     unparsable versions / anything unexpected
 *
 * Design notes:
 *   - Pure helpers (`parseVersionFromTag`, `compareVersions`,
 *     `resolveApkAsset`) are exported for direct testing; `checkAppUpdate`
 *     takes `fetchImpl` so tests inject a stub instead of stubbing globals.
 *   - Release tags are not fully under our control (CI may tag `v0.2.0`,
 *     `mobile-v0.2.0`, with prerelease suffixes). Tag parsing therefore
 *     extracts the first x.y.z triple anywhere in the string and refuses
 *     anything else — a null degrades to a failure state, never to a false
 *     "up to date".
 *   - No `Authorization` header, ever: the endpoint is public and the task
 *     forbids shipping credentials for it.
 *   - Timeout uses a manual AbortController + setTimeout, NOT
 *     `AbortSignal.timeout()` — Hermes doesn't implement it
 *     (facebook/react-native#42042, apps/mobile/CLAUDE.md lesson 4).
 *   - Response shape is validated with zod (mobile's existing validation
 *     rail); a body that doesn't match degrades to `unexpected`.
 */

import { z } from "zod";

const GITHUB_OWNER = "cfgxy";
const GITHUB_REPO = "multica";

export const GITHUB_RELEASE_API_URL = `https://api.github.com/repos/${GITHUB_OWNER}/${GITHUB_REPO}/releases/latest`;
export const GITHUB_RELEASES_PAGE_URL = `https://github.com/${GITHUB_OWNER}/${GITHUB_REPO}/releases/latest`;

/** Hard request timeout. Short on purpose: the check is user-initiated and
 * the UI shows nothing until it resolves (dropdown closes on tap). */
const DEFAULT_TIMEOUT_MS = 15_000;

const ACCEPT_HEADER = "application/vnd.github+json";

export interface ParsedVersion {
  major: number;
  minor: number;
  patch: number;
}

/**
 * Extract the first x.y.z triple from an arbitrary release tag.
 * Tolerates `v` prefixes and CI prefixes (`mobile-v0.2.0`), ignores a
 * trailing prerelease/build suffix (`v0.2.0-beta.1` → 0.2.0), and refuses
 * anything that isn't exactly three numeric segments — 0.2 and 1.2.3.4 are
 * null, not guesses.
 */
export function parseVersionFromTag(
  raw: string | null | undefined,
): ParsedVersion | null {
  if (!raw) return null;
  const match = raw.match(/(?:^|[^0-9.])(\d+)\.(\d+)\.(\d+)(?:[^0-9.]|$)/);
  if (!match) return null;
  const [, major, minor, patch] = match;
  return {
    major: Number(major),
    minor: Number(minor),
    patch: Number(patch),
  };
}

/**
 * Compare two version strings by their numeric triple.
 * Returns -1 / 0 / 1 as `local` vs `remote`, or null when either side
 * can't be parsed — callers must treat null as "cannot determine" and
 * surface a failure, never as "up to date".
 */
export function compareVersions(
  local: string,
  remote: string,
): -1 | 0 | 1 | null {
  const a = parseVersionFromTag(local);
  const b = parseVersionFromTag(remote);
  if (!a || !b) return null;
  if (a.major !== b.major) return a.major < b.major ? -1 : 1;
  if (a.minor !== b.minor) return a.minor < b.minor ? -1 : 1;
  if (a.patch !== b.patch) return a.patch < b.patch ? -1 : 1;
  return 0;
}

export interface ApkAsset {
  name: string;
  url: string;
}

/**
 * Pick the APK asset to offer for download. CI may publish per-ABI splits;
 * `universal` runs everywhere and is the safe default, `arm64-v8a` covers
 * every modern device after that, otherwise take the first .apk. Anything
 * that isn't an .apk (aab bundles, checksums, sources) is skipped.
 */
export function resolveApkAsset(assets: ApkAsset[]): ApkAsset | null {
  const apks = assets.filter((a) => a.name.toLowerCase().endsWith(".apk"));
  if (apks.length === 0) return null;
  return (
    apks.find((a) => a.name.toLowerCase().includes("universal")) ??
    apks.find((a) => a.name.toLowerCase().includes("arm64")) ??
    apks[0]
  );
}

export type UpdateCheckFailureReason =
  | "invalid_local_version" // installed version string can't be parsed
  | "network" // request failed / offline
  | "rate_limited" // GitHub 403
  | "no_release" // 404 — repo has no releases (RUYI-34 not landed yet)
  | "no_apk_asset" // latest release exists but ships no .apk
  | "incomparable" // release tag carries no x.y.z triple
  | "unexpected"; // non-200, malformed body, schema mismatch

export type UpdateCheckOutcome =
  | { status: "up_to_date"; latestVersion: string }
  | {
      status: "update_available";
      latestVersion: string;
      apkUrl: string;
      apkName: string;
      /** Release page, used as the fallback when no APK asset exists. */
      releaseUrl: string | null;
    }
  | { status: "failed"; reason: UpdateCheckFailureReason };

const githubAssetSchema = z.object({
  name: z.string(),
  browser_download_url: z.string(),
});

const githubReleaseSchema = z.object({
  tag_name: z.string(),
  html_url: z.string().nullish(),
  assets: z.array(githubAssetSchema).nullish(),
});

export interface CheckAppUpdateOptions {
  localVersion: string;
  /** Injectable for tests; defaults to the global fetch. */
  fetchImpl?: typeof fetch;
  /** AbortSignal the caller can use to cancel a hung request. */
  signal?: AbortSignal;
  timeoutMs?: number;
}

export async function checkAppUpdate(
  options: CheckAppUpdateOptions,
): Promise<UpdateCheckOutcome> {
  const { localVersion, signal, timeoutMs = DEFAULT_TIMEOUT_MS } = options;
  const fetchImpl = options.fetchImpl ?? fetch;

  // Local version is ours (expo-constants); if it's unparsable that's a
  // local defect — fail fast without burning a rate-limited request.
  if (!parseVersionFromTag(localVersion)) {
    return { status: "failed", reason: "invalid_local_version" };
  }

  // Manual AbortController instead of AbortSignal.timeout() — Hermes
  // doesn't implement it (see file header).
  const timeoutController = new AbortController();
  const timeoutId = setTimeout(
    () => timeoutController.abort(),
    timeoutMs,
  );
  // Forward an external abort into the same controller.
  const onExternalAbort = () => timeoutController.abort();
  signal?.addEventListener("abort", onExternalAbort);

  let response: Response;
  try {
    response = await fetchImpl(GITHUB_RELEASE_API_URL, {
      // 匿名公开接口：Accept 是 GitHub API 惯例头，绝不携带认证凭据。
      headers: { Accept: ACCEPT_HEADER },
      signal: timeoutController.signal,
    });
  } catch {
    return { status: "failed", reason: "network" };
  } finally {
    clearTimeout(timeoutId);
    signal?.removeEventListener("abort", onExternalAbort);
  }

  if (response.status === 404) {
    return { status: "failed", reason: "no_release" };
  }
  if (response.status === 403) {
    return { status: "failed", reason: "rate_limited" };
  }
  if (!response.ok) {
    return { status: "failed", reason: "unexpected" };
  }

  let body: unknown;
  try {
    body = await response.json();
  } catch {
    return { status: "failed", reason: "unexpected" };
  }

  const parsed = githubReleaseSchema.safeParse(body);
  if (!parsed.success) {
    return { status: "failed", reason: "unexpected" };
  }

  const { tag_name: tagName, html_url: htmlUrl, assets } = parsed.data;
  const latestVersion = parseVersionFromTag(tagName);
  if (!latestVersion) {
    return { status: "failed", reason: "incomparable" };
  }

  const comparison = compareVersions(localVersion, tagName);
  if (comparison === null) {
    return { status: "failed", reason: "incomparable" };
  }
  if (comparison >= 0) {
    return { status: "up_to_date", latestVersion: formatVersion(latestVersion) };
  }

  const apk = resolveApkAsset(
    (assets ?? []).map((a) => ({ name: a.name, url: a.browser_download_url })),
  );
  if (!apk) {
    return { status: "failed", reason: "no_apk_asset" };
  }

  return {
    status: "update_available",
    latestVersion: formatVersion(latestVersion),
    apkUrl: apk.url,
    apkName: apk.name,
    releaseUrl: htmlUrl ?? GITHUB_RELEASES_PAGE_URL,
  };
}

function formatVersion(v: ParsedVersion): string {
  return `${v.major}.${v.minor}.${v.patch}`;
}
