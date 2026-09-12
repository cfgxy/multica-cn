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
        workspaceSlug: workspace.slug,
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

  it("keeps routing identity while omitting unknown display snapshots", () => {
    expect(
      buildInboxNotificationOrigin({
        serverId: "server-b",
        workspaceSlug: null,
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
