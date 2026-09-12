// @vitest-environment node
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

function source(relative: string): string {
  return readFileSync(
    resolve(dirname(fileURLToPath(import.meta.url)), relative),
    "utf8",
  );
}

const navigator = source(
  "../components/notifications/notification-response-navigator.tsx",
);
const realtime = source("../data/realtime/use-notification-realtime.ts");
const publisher = source("../data/notifications/present-inbox-notification.ts");
const action = source("./notification-action.ts");
const workspaceLayout = source("../app/(app)/[workspace]/_layout.tsx");

describe("notification identity bridge wiring", () => {
  it("resolves a tap from the live server and workspace stores", () => {
    expect(navigator).toContain("resolveNotificationTap(data, {");
    expect(navigator).toContain(
      "activeServerId: useServerStore.getState().activeServerId",
    );
    expect(navigator).toContain("useWorkspaceStore.getState().currentWorkspaceSlug");
  });

  it("publishes the source identity through the realtime notification path", () => {
    expect(realtime).toContain("buildInboxNotificationOrigin({");
    expect(realtime).toContain("serverId,");
    expect(realtime).toContain("workspaceId: item.workspace_id");
    expect(publisher).toContain("server_id: origin.serverId");
    expect(publisher).toContain("workspace_slug: origin.workspaceSlug");
  });

  it("uses localized copy instead of raw workspace or server switch errors", () => {
    expect(navigator).toContain("showWorkspaceFailed: (_error, onRetry) =>");
    expect(navigator).toContain("showServerFailed: (_error, onRetry) =>");
    expect(navigator).not.toContain("error instanceof Error");
  });

  it("activates every target workspace before navigating to its issue", () => {
    const activate = action.indexOf(
      "const activation = await handlers.activateWorkspace(action.workspaceSlug)",
    );
    const navigate = action.indexOf('handlers.navigate("push", action.route)');

    expect(activate).toBeGreaterThan(-1);
    expect(navigate).toBeGreaterThan(activate);
  });

  it("parks taps during server switching and drops settled signed-out taps", () => {
    expect(navigator).toContain(
      "switchServer: (serverId) => switchServer(serverId, qc)",
    );
    expect(navigator).toContain("authState.isLoading || authState.isServerSwitching");
    expect(navigator).toContain("if (!isAuthLoading && !isServerSwitching)");
    expect(navigator).toContain("!isServerSwitching");
  });

  it("syncs the workspace header only while its route has focus", () => {
    expect(workspaceLayout).toContain("useIsFocused");
    expect(workspaceLayout).toContain("if (matched && isFocused)");
  });
});
