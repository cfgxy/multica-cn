// @vitest-environment node
import { describe, expect, it } from "vitest";

import {
  NOTIFICATION_LABEL_MAX,
  parseNotificationTarget,
  resolveNotificationTap,
  truncateLabel,
  type NotificationIdentity,
} from "./notification-target";

const SERVER_A = { id: "default", name: "Multica Official", apiUrl: "https://a.example.com" };
const SERVER_B = { id: "srv_b", name: "Self Hosted", apiUrl: "https://b.example.com" };

function identity(
  overrides: Partial<NotificationIdentity> = {},
): NotificationIdentity {
  return {
    activeServerId: SERVER_A.id,
    currentWorkspaceSlug: "acme",
    servers: [SERVER_A, SERVER_B],
    ...overrides,
  };
}

const payload = {
  inbox_id: "ib_1",
  issue_id: "iss_1",
  workspace_slug: "acme",
  server_id: SERVER_A.id,
  workspace_name: "Acme",
  server_name: "Multica Official",
};

describe("parseNotificationTarget", () => {
  it("keeps routing fields and display snapshots", () => {
    expect(parseNotificationTarget(payload)).toEqual({
      issueId: "iss_1",
      workspaceSlug: "acme",
      serverId: "default",
      workspaceName: "Acme",
      serverName: "Multica Official",
    });
  });

  it("returns null without an issue id or a workspace slug", () => {
    expect(parseNotificationTarget({ ...payload, issue_id: undefined })).toBeNull();
    expect(
      parseNotificationTarget({ ...payload, workspace_slug: "" }),
    ).toBeNull();
    expect(parseNotificationTarget(null)).toBeNull();
    expect(parseNotificationTarget(undefined)).toBeNull();
  });

  it("tolerates non-string junk in every optional field", () => {
    const target = parseNotificationTarget({
      issue_id: "iss_1",
      workspace_slug: "acme",
      server_id: 42,
      workspace_name: { nope: true },
      server_name: [],
    });
    expect(target).toEqual({
      issueId: "iss_1",
      workspaceSlug: "acme",
      serverId: null,
      workspaceName: null,
      serverName: null,
    });
  });
});

describe("truncateLabel", () => {
  it("caps long names and drops blank ones", () => {
    expect(truncateLabel("x".repeat(200))).toHaveLength(NOTIFICATION_LABEL_MAX);
    expect(truncateLabel("   ")).toBeNull();
    expect(truncateLabel(undefined)).toBeNull();
    expect(truncateLabel("  Acme  ")).toBe("Acme");
  });
});

describe("resolveNotificationTap", () => {
  it("opens directly when server and workspace both match", () => {
    expect(resolveNotificationTap(payload, identity())).toEqual({
      kind: "open",
      route: "/(app)/acme/issue/iss_1",
      workspaceSlug: "acme",
    });
  });

  it("confirms a workspace switch on the same server", () => {
    expect(
      resolveNotificationTap(payload, identity({ currentWorkspaceSlug: "other" })),
    ).toEqual({
      kind: "confirm-workspace",
      route: "/(app)/acme/issue/iss_1",
      workspaceSlug: "acme",
      workspaceLabel: "Acme",
    });
  });

  it("confirms a workspace switch when no workspace is active yet", () => {
    const action = resolveNotificationTap(
      payload,
      identity({ currentWorkspaceSlug: null }),
    );
    expect(action.kind).toBe("confirm-workspace");
  });

  it("falls back to the slug when the workspace name snapshot is missing", () => {
    const action = resolveNotificationTap(
      { ...payload, workspace_name: undefined },
      identity({ currentWorkspaceSlug: "other" }),
    );
    expect(action).toMatchObject({ workspaceLabel: "acme" });
  });

  it("confirms a server switch and routes without the (app) prefix", () => {
    expect(
      resolveNotificationTap(
        { ...payload, server_id: SERVER_B.id, server_name: "stale name" },
        identity(),
      ),
    ).toEqual({
      kind: "confirm-server",
      route: "/acme/issue/iss_1",
      serverId: SERVER_B.id,
      workspaceSlug: "acme",
      // The locally configured name wins over the post-time snapshot.
      serverLabel: "Self Hosted",
      workspaceLabel: "Acme",
    });
  });

  it("falls back to the api url when the local server entry has no name", () => {
    const action = resolveNotificationTap(
      { ...payload, server_id: SERVER_B.id, server_name: undefined },
      identity({ servers: [SERVER_A, { ...SERVER_B, name: "" }] }),
    );
    expect(action).toMatchObject({ serverLabel: "https://b.example.com" });
  });

  it("reports unavailable when the source server was deleted", () => {
    expect(
      resolveNotificationTap(
        { ...payload, server_id: "srv_gone" },
        identity(),
      ),
    ).toEqual({ kind: "unavailable", reason: "server-not-configured" });
  });

  it("fails closed for a legacy payload without server_id, even with a matching workspace slug", () => {
    const legacy = {
      inbox_id: "ib_1",
      issue_id: "iss_1",
      workspace_slug: "acme",
    };
    expect(
      resolveNotificationTap(
        legacy,
        identity({
          activeServerId: SERVER_B.id,
          currentWorkspaceSlug: "acme",
        }),
      ),
    ).toEqual({
      kind: "unavailable",
      reason: "missing-server-id",
    });
  });

  it("resolves the server check before the workspace check", () => {
    // Same slug as the active workspace, different server: the identity that
    // matters is the server, so this must NOT be treated as a direct open.
    expect(
      resolveNotificationTap(
        { ...payload, server_id: SERVER_B.id },
        identity({ currentWorkspaceSlug: "acme" }),
      ),
    ).toMatchObject({ kind: "confirm-server" });
  });

  it("drops payloads with nothing to navigate to", () => {
    expect(resolveNotificationTap({ inbox_id: "ib_1" }, identity())).toEqual({
      kind: "drop",
    });
  });
});
