import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import { quickReplyKeys } from "./queries";
import type { CreateQuickReplyRequest, UpdateQuickReplyRequest } from "../types";

/**
 * Quick-reply catalog mutations (RUYI-435).
 *
 * Every mutation invalidates the list on success. The realtime
 * `quick_reply:changed` event usually refetches first (it fires when the
 * server commits, including for MCP-side writes this client never saw), so
 * the invalidate here is the no-event fallback — without it, a client with a
 * dropped socket would keep rendering the pre-write catalog until the next
 * mount. Deliberately NOT optimistic: the settings tab is a form surface, a
 * spinner on the row beats reconciling a concurrent MCP edit.
 */

export function useCreateQuickReply() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (data: CreateQuickReplyRequest) => api.createQuickReply(data),
    onSuccess: () => {
      if (wsId) void qc.invalidateQueries({ queryKey: quickReplyKeys.all(wsId) });
    },
  });
}

export function useUpdateQuickReply() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: UpdateQuickReplyRequest }) =>
      api.updateQuickReply(id, data),
    onSuccess: () => {
      if (wsId) void qc.invalidateQueries({ queryKey: quickReplyKeys.all(wsId) });
    },
  });
}

export function useDeleteQuickReply() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (id: string) => api.deleteQuickReply(id),
    onSuccess: () => {
      if (wsId) void qc.invalidateQueries({ queryKey: quickReplyKeys.all(wsId) });
    },
  });
}

/** Sends the full catalog in its new order — reorder is a whole-list write. */
export function useReorderQuickReplies() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (ids: string[]) => api.reorderQuickReplies(ids),
    onSuccess: () => {
      if (wsId) void qc.invalidateQueries({ queryKey: quickReplyKeys.all(wsId) });
    },
  });
}
