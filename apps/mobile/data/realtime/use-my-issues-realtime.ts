/**
 * My Issues realtime — listing-level subscriptions. Mounted globally
 * (workspace-session-lifetime) alongside useInboxRealtime so the user's
 * issue list stays fresh regardless of which tab is foregrounded.
 *
 * issue:created     — invalidate myAll(wsId). We don't try to predict
 *                     whether the new issue belongs to assigned/created/
 *                     agents scope or matches the user's current filter;
 *                     a fresh fetch is the cheapest correct answer.
 * issue:updated     — patch in-place across every cached list entry.
 *                     A status change re-buckets the SectionList on the
 *                     consumer side; we don't have to invalidate.
 * issue:deleted     — strip from every cached list entry.
 * issue_labels:changed — patch labels via the shared updater
 *                     (also handles the detail cache; harmless if
 *                     the issue isn't in any cached list).
 * onReconnect       — invalidate myAll(wsId) since we may have missed
 *                     a create/delete while disconnected.
 *
 * Inbox realtime (use-inbox-realtime.ts) handles its own keys and runs
 * in parallel; the two are independent.
 */
import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type {
  IssueCreatedPayload,
  IssueDeletedPayload,
  IssueLabelsChangedPayload,
  IssueUpdatedPayload,
} from "@multica/core/types";
import { issueKeys } from "@/data/queries/issue-keys";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useWSClient } from "./realtime-provider";
import {
  patchIssueLabels,
  patchMyIssuesList,
  removeFromMyIssuesList,
} from "./issue-ws-updaters";

export function useMyIssuesRealtime() {
  const ws = useWSClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const qc = useQueryClient();

  useEffect(() => {
    if (!ws || !wsId) return;

    const invalidateMyAll = () => {
      qc.invalidateQueries({ queryKey: issueKeys.myAll(wsId) });
    };

    // Issue creation isn't a payload we can patch into a scope-filtered
    // list without knowing the user's filter context — fall back to a
    // refresh. Same on reconnect.
    const unsubs: Array<() => void> = [
      ws.on("issue:created", (_p) => {
        // We do consume the payload type-check, but we don't need any
        // fields from it — server is the authority on which scopes/filters
        // the new issue lands in.
        void (_p as IssueCreatedPayload);
        invalidateMyAll();
      }),
      ws.on("issue:updated", (p) => {
        const payload = p as IssueUpdatedPayload;
        patchMyIssuesList(qc, wsId, payload.issue);
      }),
      ws.on("issue:deleted", (p) => {
        const payload = p as IssueDeletedPayload;
        removeFromMyIssuesList(qc, wsId, payload.issue_id);
      }),
      ws.on("issue_labels:changed", (p) => {
        const payload = p as IssueLabelsChangedPayload;
        patchIssueLabels(qc, wsId, payload.issue_id, payload.labels);
      }),
      ws.onReconnect(invalidateMyAll),
    ];

    return () => {
      for (const unsub of unsubs) unsub();
    };
  }, [ws, wsId, qc]);
}
