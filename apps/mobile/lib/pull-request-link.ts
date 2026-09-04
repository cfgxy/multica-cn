/**
 * Opening a related PR from the mobile list (RUYI-43).
 *
 * The task's split — GitHub App installed → the app's PR page; not
 * installed → system browser — is delegated to the OS via the ONE
 * canonical URL (`pr.html_url`, `https://github.com/<owner>/<repo>/pull/<n>`):
 *
 *   - Android: github.com is a verified App Link domain for
 *     com.github.android (github.com/.well-known/assetlinks.json declares
 *     the package), so a VIEW intent on the https URL resolves straight
 *     into the GitHub App's PR page when installed, and into the default
 *     browser otherwise.
 *   - iOS: the same URL is a Universal Link with identical semantics.
 *
 * An explicit `Linking.canOpenURL("github://")` probe was considered and
 * rejected: GitHub Mobile documents no custom-scheme deep link for PR
 * pages, so the probe can only produce false negatives (installed users
 * routed to the browser), and an undocumented path format risks landing
 * on the wrong screen inside the app. canOpenURL-based probing would also
 * require an Android `<queries>` declaration that plain openURL does not.
 *
 * RN `Linking` stays injectable (node-only vitest lane) and rejections are
 * converted to a typed result so the screen can Alert without try/catch.
 */

export interface URLOpener {
  openURL(url: string): Promise<unknown>;
}

export type OpenUrlResult = { ok: true } | { ok: false; message: string };

export async function openExternalUrl(
  url: string,
  opener: URLOpener,
): Promise<OpenUrlResult> {
  try {
    await opener.openURL(url);
    return { ok: true };
  } catch (err) {
    return {
      ok: false,
      message:
        err instanceof Error ? err.message : "Could not open the link.",
    };
  }
}
