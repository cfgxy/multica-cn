// @vitest-environment node
import { describe, expect, it, vi } from "vitest";

/**
 * Pure-function tests for the app-update feature (RUYI-36).
 *
 * The update check is serverless by design (Owner constraint): the single
 * source of truth is the latest GitHub Release of cfgxy/multica, fetched
 * anonymously. These tests pin the three pieces the UI relies on:
 *
 *   - `parseVersionFromTag` — Release tags are not fully under our control
 *     (CI may tag `v0.2.0`, `mobile-v0.2.0`, `android-v1.2.3`, with or
 *     without a prerelease suffix), so tag parsing extracts the first
 *     x.y.z triple and returns null otherwise. A null anywhere degrades to
 *     a "cannot compare" failure — never a false "up to date".
 *   - `compareVersions` — semver triple ordering; either side unparsable →
 *     null (caller shows the failure path instead of guessing).
 *   - `resolveApkAsset` — asset picking from the Release assets list
 *     (universal > arm64 > first .apk; .aab and non-APK files skipped).
 *   - `checkAppUpdate` — outcome classification over the anonymous GitHub
 *     endpoint: 200/404/403/network/ malformed body, each mapped to one of
 *     the three UI states (up_to_date / update_available / failed+reason).
 *     The request must stay anonymous (no Authorization header) per the
 *     task's security constraint.
 *
 * fetch is injected (`fetchImpl`) so no global stubbing is needed.
 */

import {
  checkAppUpdate,
  compareVersions,
  GITHUB_RELEASE_API_URL,
  parseVersionFromTag,
  resolveApkAsset,
  type ApkAsset,
} from "./app-update";

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  } as unknown as Response;
}

describe("parseVersionFromTag", () => {
  it("parses a plain x.y.z tag", () => {
    expect(parseVersionFromTag("0.2.0")).toEqual({ major: 0, minor: 2, patch: 0 });
  });

  it("parses a v-prefixed tag", () => {
    expect(parseVersionFromTag("v0.2.0")).toEqual({ major: 0, minor: 2, patch: 0 });
  });

  it("parses the mobile-v / android-v release tag convention", () => {
    expect(parseVersionFromTag("mobile-v0.2.0")).toEqual({ major: 0, minor: 2, patch: 0 });
    expect(parseVersionFromTag("android-v1.2.3")).toEqual({ major: 1, minor: 2, patch: 3 });
  });

  it("ignores a prerelease suffix", () => {
    expect(parseVersionFromTag("v0.2.0-beta.1")).toEqual({ major: 0, minor: 2, patch: 0 });
  });

  it("returns null for non-version strings", () => {
    expect(parseVersionFromTag("abc")).toBeNull();
    expect(parseVersionFromTag("nightly")).toBeNull();
  });

  it("returns null unless exactly three numeric segments are present", () => {
    expect(parseVersionFromTag("0.2")).toBeNull();
    expect(parseVersionFromTag("v1.2.3.4")).toBeNull();
  });

  it("returns null for nullish or empty input", () => {
    expect(parseVersionFromTag(null)).toBeNull();
    expect(parseVersionFromTag(undefined)).toBeNull();
    expect(parseVersionFromTag("")).toBeNull();
  });
});

describe("compareVersions", () => {
  it("treats equal versions as 0 (with and without v prefix)", () => {
    expect(compareVersions("0.1.0", "0.1.0")).toBe(0);
    expect(compareVersions("0.1.0", "v0.1.0")).toBe(0);
  });

  it("orders by major, then minor, then patch", () => {
    expect(compareVersions("0.1.0", "1.0.0")).toBe(-1);
    expect(compareVersions("0.1.0", "0.2.0")).toBe(-1);
    expect(compareVersions("0.1.0", "0.1.1")).toBe(-1);
    expect(compareVersions("1.0.0", "0.9.9")).toBe(1);
    expect(compareVersions("0.1.1", "0.1.0")).toBe(1);
  });

  it("compares against prerelease tags by their numeric triple", () => {
    expect(compareVersions("0.1.0", "v0.2.0-beta.1")).toBe(-1);
  });

  it("returns null (not a guess) when either side is unparsable", () => {
    expect(compareVersions("0.1.0", "garbage")).toBeNull();
    expect(compareVersions("garbage", "0.1.0")).toBeNull();
    expect(compareVersions("", "")).toBeNull();
  });
});

describe("resolveApkAsset", () => {
  const apk = (name: string): ApkAsset => ({ name, url: `https://example.test/${name}` });

  it("returns null when there are no .apk assets", () => {
    expect(resolveApkAsset([])).toBeNull();
    expect(resolveApkAsset([apk("app.aab"), apk("checksums.txt")])).toBeNull();
  });

  it("matches .apk case-insensitively", () => {
    expect(resolveApkAsset([apk("App-0.2.0.APK")])?.name).toBe("App-0.2.0.APK");
  });

  it("returns the single .apk asset", () => {
    expect(resolveApkAsset([apk("multica-0.2.0.apk")])?.name).toBe("multica-0.2.0.apk");
  });

  it("prefers the universal APK when several are present", () => {
    const picked = resolveApkAsset([
      apk("multica-arm64-v8a-0.2.0.apk"),
      apk("multica-universal-0.2.0.apk"),
    ]);
    expect(picked?.name).toBe("multica-universal-0.2.0.apk");
  });

  it("falls back to arm64 when no universal APK exists", () => {
    const picked = resolveApkAsset([
      apk("multica-armeabi-v7a-0.2.0.apk"),
      apk("multica-arm64-v8a-0.2.0.apk"),
    ]);
    expect(picked?.name).toBe("multica-arm64-v8a-0.2.0.apk");
  });

  it("falls back to the first .apk otherwise", () => {
    const picked = resolveApkAsset([apk("b.apk"), apk("a.apk")]);
    expect(picked?.name).toBe("b.apk");
  });
});

describe("checkAppUpdate", () => {
  const release = (tagName: string, assets: Array<Record<string, string>> = []) =>
    jsonResponse({
      tag_name: tagName,
      html_url: `https://github.com/cfgxy/multica/releases/tag/${tagName}`,
      assets,
    });

  it("is a no-op failure when the local version itself is unparsable (no request sent)", async () => {
    const fetchImpl = vi.fn();
    const outcome = await checkAppUpdate({ localVersion: "", fetchImpl });
    expect(outcome).toEqual({ status: "failed", reason: "invalid_local_version" });
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it("reports an available update with the APK download URL", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(
      release("v0.2.0", [
        { name: "multica-universal-0.2.0.apk", browser_download_url: "https://example.test/multica-0.2.0.apk" },
      ]),
    );
    const outcome = await checkAppUpdate({ localVersion: "0.1.0", fetchImpl });
    expect(outcome).toEqual({
      status: "update_available",
      latestVersion: "0.2.0",
      apkUrl: "https://example.test/multica-0.2.0.apk",
      apkName: "multica-universal-0.2.0.apk",
      releaseUrl: "https://github.com/cfgxy/multica/releases/tag/v0.2.0",
    });
  });

  it("reports up-to-date when the latest release equals the installed version", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(release("v0.1.0"));
    expect(await checkAppUpdate({ localVersion: "0.1.0", fetchImpl })).toEqual({
      status: "up_to_date",
      latestVersion: "0.1.0",
    });
  });

  it("reports up-to-date when the installed version is newer than the release", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(release("v0.0.9"));
    expect(await checkAppUpdate({ localVersion: "0.1.0", fetchImpl })).toEqual({
      status: "up_to_date",
      latestVersion: "0.0.9",
    });
  });

  it("degrades when the latest release ships no APK asset yet", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(release("v0.2.0", [{ name: "source.zip", browser_download_url: "https://example.test/s.zip" }]));
    expect(await checkAppUpdate({ localVersion: "0.1.0", fetchImpl })).toEqual({
      status: "failed",
      reason: "no_apk_asset",
    });
  });

  it("maps 404 (no release yet) to the no_release failure", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({}, 404));
    expect(await checkAppUpdate({ localVersion: "0.1.0", fetchImpl })).toEqual({
      status: "failed",
      reason: "no_release",
    });
  });

  it("maps 403 to the rate_limited failure", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({}, 403));
    expect(await checkAppUpdate({ localVersion: "0.1.0", fetchImpl })).toEqual({
      status: "failed",
      reason: "rate_limited",
    });
  });

  it("maps other non-200 statuses to the unexpected failure", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({}, 500));
    expect(await checkAppUpdate({ localVersion: "0.1.0", fetchImpl })).toEqual({
      status: "failed",
      reason: "unexpected",
    });
  });

  it("maps network rejection to the network failure", async () => {
    const fetchImpl = vi.fn().mockRejectedValue(new TypeError("Network request failed"));
    expect(await checkAppUpdate({ localVersion: "0.1.0", fetchImpl })).toEqual({
      status: "failed",
      reason: "network",
    });
  });

  it("degrades on a malformed JSON body instead of throwing", async () => {
    const fetchImpl = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => {
        throw new Error("invalid json");
      },
    } as unknown as Response);
    expect(await checkAppUpdate({ localVersion: "0.1.0", fetchImpl })).toEqual({
      status: "failed",
      reason: "unexpected",
    });
  });

  it("degrades when the body does not match the release schema", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({ message: "Not Found" }));
    expect(await checkAppUpdate({ localVersion: "0.1.0", fetchImpl })).toEqual({
      status: "failed",
      reason: "unexpected",
    });
  });

  it("degrades to incomparable when the tag has no version triple", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(release("nightly"));
    expect(await checkAppUpdate({ localVersion: "0.1.0", fetchImpl })).toEqual({
      status: "failed",
      reason: "incomparable",
    });
  });

  it("hits the anonymous GitHub API endpoint without credentials", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(release("v0.1.0"));
    await checkAppUpdate({ localVersion: "0.1.0", fetchImpl });
    expect(fetchImpl).toHaveBeenCalledWith(
      GITHUB_RELEASE_API_URL,
      expect.objectContaining({
        // 安全约束：仅匿名公开接口，绝不携带认证头。
        headers: expect.not.objectContaining({ Authorization: expect.anything() }),
      }),
    );
    const [, init] = fetchImpl.mock.calls[0] as [string, RequestInit];
    const headers = init.headers as Record<string, string>;
    expect(headers["Accept"]).toBe("application/vnd.github+json");
    expect(Object.keys(headers).join(" ").toLowerCase()).not.toContain("authorization");
  });

  it("forwards an external abort to the in-flight request", async () => {
    let captured: AbortSignal | undefined;
    let resolveFetch!: (r: Response) => void;
    const fetchImpl = vi.fn().mockImplementation(
      (_url: string, init?: RequestInit) =>
        new Promise<Response>((resolve) => {
          captured = init?.signal as AbortSignal;
          resolveFetch = resolve;
        }),
    );
    const controller = new AbortController();
    const pending = checkAppUpdate({
      localVersion: "0.1.0",
      fetchImpl,
      signal: controller.signal,
    });
    // 请求进行中取消 —— 外部 signal 经 abort 监听转发到内部超时
    // controller（Hermes 无 AbortSignal.any），传导效果即可观测。
    controller.abort();
    expect(captured?.aborted).toBe(true);
    resolveFetch(release("v0.1.0"));
    await pending;
  });
});
