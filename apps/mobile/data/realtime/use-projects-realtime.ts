/**
 * Projects realtime — listing-level subscriptions. Mounted globally
 * (workspace-session-lifetime) alongside `useMyIssuesRealtime` so the
 * project list stays fresh even if the user is on chat or an issue.
 *
 * Event coverage:
 *   - project:created → upsert into the list cache. The payload carries
 *                       the full Project; no refetch.
 *   - project:updated → patch list + detail (full replace on detail).
 *   - project:deleted → strip from list and drop detail + resources caches.
 *   - reconnect       → invalidate project list (we may have missed
 *                       create/delete events while disconnected).
 *
 * Per the patch-over-invalidate rule in apps/mobile/CLAUDE.md "Realtime →
 * Patch over invalidate (cellular-data rule)", every event with a full
 * payload patches the cache directly.
 */
import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type {
  ProjectCreatedPayload,
  ProjectDeletedPayload,
  ProjectUpdatedPayload,
} from "@multica/core/types";
import { projectKeys } from "@/data/queries/projects";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useWSClient } from "./realtime-provider";
import {
  clearProjectDetail,
  patchProjectDetail,
  patchProjectsList,
  removeFromProjectsList,
  upsertIntoProjectsList,
} from "./project-ws-updaters";

export function useProjectsRealtime() {
  const ws = useWSClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const qc = useQueryClient();

  useEffect(() => {
    if (!ws || !wsId) return;

    const invalidateList = () => {
      qc.invalidateQueries({ queryKey: projectKeys.list(wsId) });
    };

    const unsubs: Array<() => void> = [
      ws.on("project:created", (p) => {
        const payload = p as ProjectCreatedPayload;
        upsertIntoProjectsList(qc, wsId, payload.project);
      }),
      ws.on("project:updated", (p) => {
        const payload = p as ProjectUpdatedPayload;
        patchProjectsList(qc, wsId, payload.project);
        patchProjectDetail(qc, wsId, payload.project);
      }),
      ws.on("project:deleted", (p) => {
        const payload = p as ProjectDeletedPayload;
        removeFromProjectsList(qc, wsId, payload.project_id);
        clearProjectDetail(qc, wsId, payload.project_id);
      }),
      ws.onReconnect(invalidateList),
    ];

    return () => {
      for (const unsub of unsubs) unsub();
    };
  }, [ws, wsId, qc]);
}
