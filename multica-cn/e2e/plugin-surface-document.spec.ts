import { expect, test } from "@playwright/test";
import { buildSurfaceFrameDocument } from "../packages/views/plugins/surface-document";

/**
 * Real Chromium coverage for trusted-wrapper runtime behavior jsdom does not
 * implement: executing the wrapper as a sandboxed srcdoc document, the reload
 * lifecycle when the host rewrites srcdoc, and the one-shot guest error relay
 * with its terminal state. Document shape and input validation are unit-tested
 * in packages/views/plugins/surface-document.test.ts; the network and
 * cross-frame isolation boundary is covered by plugin-surface-security.spec.ts.
 */

test.describe("plugin surface document (real Chromium, trusted wrapper lifecycle)", () => {
  test("a host-authored srcdoc reload is not reported as hostile navigation", async ({ page }) => {
    await page.route("https://plugin-content.example.test/**", async (route) => {
      await route.fulfill({
        contentType: "text/html",
        headers: { "Content-Security-Policy": "default-src 'none'; script-src 'unsafe-inline'" },
        body: `<!doctype html><script>
          const channel = new MessageChannel();
          parent.postMessage({
            type: "multica:plugin-bridge-connect",
            version: 2,
            challenge: "proof"
          }, "*", [channel.port1]);
        </script>`,
      });
    });
    const url = "https://plugin-content.example.test/plugin-surfaces/opaque";
    const wrapper = buildSurfaceFrameDocument({ url, bridgeToken: "proof" });

    await page.setContent(`<script>
      window.surface = { bridges: 0, navigated: 0, blocked: 0 };
      addEventListener("message", event => {
        const type = event.data?.type;
        if (type === "multica:plugin-bridge-connect" && event.ports[0]) window.surface.bridges += 1;
        if (type === "multica:plugin-surface-navigated") window.surface.navigated += 1;
        if (type === "multica:plugin-surface-navigation-blocked") window.surface.blocked += 1;
      });
    </script><iframe id="surface" sandbox="allow-scripts allow-same-origin"></iframe>`);
    await page.locator("#surface").evaluate((frame, srcdoc) => {
      (frame as HTMLIFrameElement).srcdoc = srcdoc as string;
    }, wrapper);

    await expect.poll(() => page.evaluate(() =>
      (window as unknown as { surface: { bridges: number } }).surface.bridges,
    )).toBe(1);
    expect(page.frames().some((frame) => frame.url() === url)).toBe(true);

    // A React re-render reassigns srcdoc. The wrapper document reloads and its
    // guest handshake restarts from scratch — neither instance may report the
    // reload as a hostile navigation.
    await page.locator("#surface").evaluate((frame, srcdoc) => {
      (frame as HTMLIFrameElement).srcdoc = srcdoc as string;
    }, wrapper);

    await expect.poll(() => page.evaluate(() =>
      (window as unknown as { surface: { bridges: number } }).surface.bridges,
    )).toBe(2);
    const state = await page.evaluate(() =>
      (window as unknown as { surface: { navigated: number; blocked: number } }).surface,
    );
    expect(state.navigated).toBe(0);
    expect(state.blocked).toBe(0);
  });

  test("a guest-reported error is relayed once and a terminal surface rejects later connects", async ({ page }) => {
    await page.route("https://plugin-content.example.test/**", async (route) => {
      await route.fulfill({
        contentType: "text/html",
        headers: { "Content-Security-Policy": "default-src 'none'; script-src 'unsafe-inline'" },
        body: `<!doctype html><script>
          parent.postMessage({ type: "multica:plugin-surface-error" }, "*");
          setTimeout(() => {
            const channel = new MessageChannel();
            parent.postMessage({
              type: "multica:plugin-bridge-connect",
              version: 2,
              challenge: "proof"
            }, "*", [channel.port1]);
          }, 50);
        </script>`,
      });
    });
    const wrapper = buildSurfaceFrameDocument({
      url: "https://plugin-content.example.test/plugin-surfaces/opaque",
      bridgeToken: "proof",
    });

    await page.setContent(`<script>
      window.surface = { errors: 0, bridges: 0 };
      addEventListener("message", event => {
        const type = event.data?.type;
        if (type === "multica:plugin-surface-error") window.surface.errors += 1;
        if (type === "multica:plugin-bridge-connect" && event.ports[0]) window.surface.bridges += 1;
      });
    </script><iframe id="surface" sandbox="allow-scripts allow-same-origin"></iframe>`);
    await page.locator("#surface").evaluate((frame, srcdoc) => {
      (frame as HTMLIFrameElement).srcdoc = srcdoc as string;
    }, wrapper);

    await expect.poll(() => page.evaluate(() =>
      (window as unknown as { surface: { errors: number } }).surface.errors,
    )).toBe(1);
    await page.waitForTimeout(300);
    const state = await page.evaluate(() =>
      (window as unknown as { surface: { errors: number; bridges: number } }).surface,
    );
    expect(state.bridges).toBe(0);
    expect(state.errors).toBe(1);
  });
});
