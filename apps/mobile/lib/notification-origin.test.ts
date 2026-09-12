// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { Workspace } from "@multica/core/types";
import type { ServerEntry } from "@/data/server-config";
import { buildInboxNotificationOrigin } from "./notification-origin";

const server: ServerEntry = {
  id: "server-b",
  name: "Self-hosted server",
  apiUrl: "https://server.example.com",
  webUrl: null,
  builtIn: false,
};

const workspace: Workspace = {
  id: "workspace-b",
  slug: "other",
  name: "Other workspace",
  description: null,
  context: null,
  settings: {},
  repos: [],
  issue_prefix: "TEST",
  avatar_url: null,
  created_at: "2026-09-12T00:00:00Z",
  updated_at: "2026-09-12T00:00:00Z",
};

describe("buildInboxNotificationOrigin", () => {
  it("uses the active server and workspace identities with display snapshots", () => {
    expect(
      buildInboxNotificationOrigin({
        serverId: server.id,
        workspaceId: workspace.id,
        servers: [server],
        workspaces: [workspace],
      }),
    ).toEqual({
      serverId: "server-b",
      workspaceSlug: "other",
      workspaceName: "Other workspace",
      serverName: "Self-hosted server",
    });
  });

  it("uses the inbox item's source workspace instead of the active workspace", () => {
    const sourceWorkspace: Workspace = {
      ...workspace,
      id: "workspace-a",
      slug: "source",
      name: "Source workspace",
    };
    const origin = {
      serverId: server.id,
      workspaceId: sourceWorkspace.id,
      servers: [server],
      workspaces: [workspace, sourceWorkspace],
    };

    expect(buildInboxNotificationOrigin(origin)).toEqual({
      serverId: "server-b",
      workspaceSlug: "source",
      workspaceName: "Source workspace",
      serverName: "Self-hosted server",
    });
  });

  it("keeps routing identity while omitting unknown display snapshots", () => {
    expect(
      buildInboxNotificationOrigin({
        serverId: "server-b",
        workspaceId: null,
        servers: [],
        workspaces: undefined,
      }),
    ).toEqual({
      serverId: "server-b",
      workspaceSlug: "",
      workspaceName: null,
      serverName: null,
    });
  });
});
