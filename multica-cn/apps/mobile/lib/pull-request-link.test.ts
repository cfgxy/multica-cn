// @vitest-environment node
import { describe, expect, it, vi } from "vitest";

/**
 * Pure-function tests for opening a related PR (RUYI-43).
 *
 * Why one canonical https URL instead of an explicit "is GitHub App
 * installed" probe:
 *
 *   - GitHub Mobile has NO documented custom `github://` deep-link scheme
 *     for PR pages. Probing an undocumented scheme with canOpenURL
 *     produces false negatives (→ GitHub-app users dumped in the
 *     browser), and guessing a path format can land inside the app on
 *     the wrong screen.
 *   - The canonical `https://github.com/<owner>/<repo>/pull/<n>` URL is
 *     a verified App Link on Android (github.com/.well-known/
 *     assetlinks.json declares com.github.android) and a Universal Link
 *     on iOS: with the GitHub App installed the OS routes it into the
 *     app's PR page; without it the same URL opens in the system
 *     browser. The OS performs the installed/not-installed split the
 *     task asks for, deterministically, from one code path.
 *   - `Linking.openURL` to an https target needs no Android `<queries>`
 *     package-visibility declaration (that is only required for
 *     canOpenURL / resolveActivity probing).
 *
 * `openExternalUrl` keeps the RN `Linking` boundary injectable so these
 * branches run in the node-only vitest lane, and converts a rejection
 * into a typed result so the screen can Alert without try/catch noise.
 */

import { openExternalUrl } from "./pull-request-link";

function opener(impl: (url: string) => Promise<unknown>) {
  return { openURL: vi.fn(impl) };
}

describe("openExternalUrl", () => {
  it("opens the exact url and resolves ok", async () => {
    const ln = opener(() => Promise.resolve());
    const result = await openExternalUrl(
      "https://github.com/cfgxy/multica/pull/13",
      ln,
    );
    expect(ln.openURL).toHaveBeenCalledWith(
      "https://github.com/cfgxy/multica/pull/13",
    );
    expect(result).toEqual({ ok: true });
  });

  it("maps an Error rejection to ok:false with the error message", async () => {
    const ln = opener(() => Promise.reject(new Error("No Activity found")));
    const result = await openExternalUrl("https://github.com/a/b/pull/1", ln);
    expect(result).toEqual({ ok: false, message: "No Activity found" });
  });

  it("maps a non-Error rejection to a fallback message", async () => {
    const ln = opener(() => Promise.reject("boom"));
    const result = await openExternalUrl("https://github.com/a/b/pull/1", ln);
    expect(result).toEqual({ ok: false, message: "Could not open the link." });
  });
});
