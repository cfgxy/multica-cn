/**
 * Notification tap → routing decision (RUYI-131).
 *
 * A system notification is posted from ONE server + ONE workspace, but the
 * app can be sitting on a different one by the time the user taps it. The
 * pre-RUYI-131 navigator pushed `/(app)/{slug}/issue/{id}` blind: with the
 * wrong identity active the issue query runs against a server/workspace
 * that never had that issue and the screen lands on its "Failed to load
 * issue" state — the 404 the owner reported.
 *
 * Everything here is a pure decision over the notification `data` bag plus
 * a snapshot of the current identity, so the whole matrix is testable in
 * the node vitest lane. The expo side (Alert, router, server switch) stays
 * in `components/notifications/notification-response-navigator.tsx`.
 *
 * Decision order is server first, then workspace: a cross-server target is
 * ALSO a cross-workspace target, but the server switch is what restores the
 * session the workspace slug is scoped to.
 *
 * Legacy notifications (posted before this change) carry no `server_id`.
 * They retain the previous behavior and use the active server, so upgrades do
 * not make existing notifications unusable. The workspace check still runs.
 */

export interface InboxNotificationData {
  inbox_id?: unknown;
  issue_id?: unknown;
  workspace_slug?: unknown;
  server_id?: unknown;
  workspace_name?: unknown;
  server_name?: unknown;
}

export interface NotificationTarget {
  issueId: string;
  workspaceSlug: string;
  /** null for notifications posted before RUYI-131. */
  serverId: string | null;
  /** Display snapshot captured at post time; null when unavailable. */
  workspaceName: string | null;
  serverName: string | null;
}

/** Structural subset of `data/server-config.ts`'s ServerEntry — kept local
 *  so this module stays free of store/native imports. */
export interface ServerIdentity {
  id: string;
  name: string;
  apiUrl: string;
}

export interface NotificationIdentity {
  activeServerId: string;
  currentWorkspaceSlug: string | null;
  servers: readonly ServerIdentity[];
}

export type NotificationAction =
  /** Nothing actionable in the payload — swallow the tap. */
  | { kind: "drop" }
  /** Same server, same workspace: validate membership, then navigate. */
  | { kind: "open"; route: string; workspaceSlug: string }
  /** Same server, different workspace: confirm, then push. */
  | {
      kind: "confirm-workspace";
      route: string;
      workspaceSlug: string;
      workspaceLabel: string;
    }
  /** Different server: confirm, switch the session, then replace. */
  | {
      kind: "confirm-server";
      route: string;
      serverId: string;
      workspaceSlug: string;
      serverLabel: string;
      workspaceLabel: string;
    }
  /** The source server is no longer configured on this device. */
  | { kind: "unavailable" };

/** Display snapshots ride in the notification payload; cap them so a long
 *  workspace name can't bloat every notification's data bag. */
export const NOTIFICATION_LABEL_MAX = 64;

export function truncateLabel(raw: string | null | undefined): string | null {
  if (typeof raw !== "string") return null;
  const trimmed = raw.trim();
  if (!trimmed) return null;
  return trimmed.slice(0, NOTIFICATION_LABEL_MAX);
}

function asString(value: unknown): string | null {
  return typeof value === "string" && value.length > 0 ? value : null;
}

export function parseNotificationTarget(
  data: InboxNotificationData | null | undefined,
): NotificationTarget | null {
  if (!data || typeof data !== "object") return null;
  const issueId = asString(data.issue_id);
  const workspaceSlug = asString(data.workspace_slug);
  // Without both we have no route to build — the inbox tab is the fallback
  // surface, so drop rather than guess.
  if (!issueId || !workspaceSlug) return null;
  return {
    issueId,
    workspaceSlug,
    serverId: asString(data.server_id),
    workspaceName: truncateLabel(asString(data.workspace_name)),
    serverName: truncateLabel(asString(data.server_name)),
  };
}

export function notificationRoute(target: NotificationTarget): string {
  return `/(app)/${target.workspaceSlug}/issue/${target.issueId}`;
}

/** Post-switch landing route. The (app) group prefix is dropped because the
 *  switch replaces the whole stack from the root. */
export function notificationRootRoute(target: NotificationTarget): string {
  return `/${target.workspaceSlug}/issue/${target.issueId}`;
}

function workspaceLabelFor(target: NotificationTarget): string {
  return target.workspaceName ?? target.workspaceSlug;
}

function serverLabelFor(
  target: NotificationTarget,
  entry: ServerIdentity,
): string {
  // Prefer the locally configured name over the post-time snapshot: the
  // user renamed it on THIS device, so that's the name they recognise.
  return entry.name || target.serverName || entry.apiUrl;
}

export function resolveNotificationAction(
  target: NotificationTarget,
  identity: NotificationIdentity,
): NotificationAction {
  const { activeServerId, currentWorkspaceSlug, servers } = identity;

  // Notifications posted before RUYI-131 lack server_id. Keep their prior
  // current-server behavior while still confirming a workspace change.
  const serverId = target.serverId ?? activeServerId;

  if (serverId !== activeServerId) {
    const entry = servers.find((s) => s.id === serverId);
    // Source server deleted from the list since the notification was
    // posted — there is no session to restore and no address to reach.
    if (!entry) return { kind: "unavailable" };
    return {
      kind: "confirm-server",
      route: notificationRootRoute(target),
      serverId,
      workspaceSlug: target.workspaceSlug,
      serverLabel: serverLabelFor(target, entry),
      workspaceLabel: workspaceLabelFor(target),
    };
  }

  if (currentWorkspaceSlug !== target.workspaceSlug) {
    return {
      kind: "confirm-workspace",
      route: notificationRoute(target),
      workspaceSlug: target.workspaceSlug,
      workspaceLabel: workspaceLabelFor(target),
    };
  }

  return {
    kind: "open",
    route: notificationRoute(target),
    workspaceSlug: target.workspaceSlug,
  };
}

/** One-shot entry point: payload → action. Returns `drop` for payloads with
 *  nothing to navigate to. */
export function resolveNotificationTap(
  data: InboxNotificationData | null | undefined,
  identity: NotificationIdentity,
): NotificationAction {
  const target = parseNotificationTarget(data);
  if (!target) return { kind: "drop" };
  return resolveNotificationAction(target, identity);
}
