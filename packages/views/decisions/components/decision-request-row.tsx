"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ChevronDown, ChevronRight, ShieldCheck } from "lucide-react";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import type { DecisionRequest } from "@multica/core/types";
import {
  answerDecisionRequest,
  cancelDecisionRequest,
  decisionRequestKeys,
  decisionRequestDetailQueryOptions,
} from "@multica/core/issues/decision-requests";
import { useActorName } from "@multica/core/workspace/hooks";
import { useT, useTimeAgo } from "../../i18n";

// One row of the decision center's authorization-requests section
// (RUYI-630). The summary is deliberately issue-free: the origin issue shows
// as title-level text and the row never deep-links into the issue thread
// (决策中心点击不进 Issue). Operable rows carry the answer buttons; a
// read-only projection (the origin space's row when the target is another
// space, or when an issue card carries the step) shows a hint instead.

type DecisionRequestStatus = DecisionRequest["status"];

const ACTION_LABEL_KEYS: Record<string, "action_workspace_info_read" | "action_prompt_restore"> = {
  workspace_info_read: "action_workspace_info_read",
  prompt_restore: "action_prompt_restore",
};

const RISK_LABEL_KEYS: Record<string, "risk_read" | "risk_write_low"> = {
  read: "risk_read",
  write_low: "risk_write_low",
};

const STATUS_LABEL_KEYS: Record<
  DecisionRequestStatus,
  | "status_pending"
  | "status_approved"
  | "status_denied"
  | "status_expired"
  | "status_revoked"
  | "status_executed"
  | "status_execute_failed"
> = {
  pending: "status_pending",
  approved: "status_approved",
  denied: "status_denied",
  expired: "status_expired",
  revoked: "status_revoked",
  executed: "status_executed",
  execute_failed: "status_execute_failed",
};

function statusBadgeVariant(
  status: DecisionRequestStatus,
): "default" | "secondary" | "outline" | "destructive" {
  switch (status) {
    case "pending":
      return "default";
    case "approved":
    case "executed":
      return "secondary";
    case "execute_failed":
      return "destructive";
    default:
      return "outline";
  }
}

/** Per-space step lines for cross-space requests (方案甲 dual-row carrier). */
function RequestSteps({ request }: { request: DecisionRequest }) {
  const { t } = useT("decisions");
  const [open, setOpen] = useState(false);
  const detail = useQuery({
    ...decisionRequestDetailQueryOptions(request.workspace_id, request.request_group_id),
    enabled: open,
  });

  if (request.role !== "origin") return null;

  return (
    <div className="mt-1">
      <button
        type="button"
        className="flex items-center gap-1 text-caption text-muted-foreground hover:text-foreground"
        data-testid="decision-request-steps-toggle"
        onClick={() => setOpen((prev) => !prev)}
      >
        {open ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
        {t(($) => $.requests.steps_title)}
      </button>
      {open && (
        <div className="mt-1 flex flex-col gap-1 border-l pl-3">
          {detail.isPending && <Skeleton className="h-4 w-40" />}
          {detail.isError && (
            <span className="text-caption text-destructive">
              {t(($) => $.requests.detail_error)}
            </span>
          )}
          {(detail.data?.steps ?? []).map((step) => (
            <span
              key={step.id}
              className="flex items-center gap-2 text-caption text-muted-foreground"
              data-testid="decision-request-step"
            >
              <span className="shrink-0 font-mono">
                {step.role === "target"
                  ? t(($) => $.requests.step_target)
                  : t(($) => $.requests.step_origin)}
              </span>
              <span>{t(($) => $.requests[STATUS_LABEL_KEYS[step.status]])}</span>
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

export function DecisionRequestRow({ request }: { request: DecisionRequest }) {
  const { t } = useT("decisions");
  const timeAgo = useTimeAgo();
  const { getActorName } = useActorName();
  const qc = useQueryClient();

  const actionKey = ACTION_LABEL_KEYS[request.action_type];
  const riskKey = RISK_LABEL_KEYS[request.risk_tier];
  const isPending = request.status === "pending";
  const originName = getActorName("agent", request.origin_agent_id);

  const refresh = () => {
    qc.invalidateQueries({ queryKey: decisionRequestKeys.all() });
  };

  const answer = useMutation({
    mutationFn: answerDecisionRequest,
    onSuccess: refresh,
    onError: () => {
      refresh();
      toast.error(t(($) => $.requests.answer_failed));
    },
  });

  const cancel = useMutation({
    mutationFn: cancelDecisionRequest,
    onSuccess: refresh,
    onError: () => {
      refresh();
      toast.error(t(($) => $.requests.cancel_failed));
    },
  });

  return (
    <div
      className="rounded-lg px-3 py-2 hover:bg-surface-hover"
      data-testid="decision-request-row"
      data-status={request.status}
    >
      <div className="flex min-w-0 flex-col gap-1">
        <span className="flex min-w-0 flex-wrap items-center gap-2">
          <ShieldCheck className="size-3.5 shrink-0 text-brand" aria-hidden />
          <span className="truncate text-body font-medium">{request.title}</span>
          <Badge variant="outline" className="shrink-0">
            {actionKey ? t(($) => $.requests[actionKey]) : request.action_type}
          </Badge>
          {riskKey && (
            <Badge variant="secondary" className="shrink-0">
              {t(($) => $.requests[riskKey])}
            </Badge>
          )}
          <Badge variant={statusBadgeVariant(request.status)} className="shrink-0">
            {t(($) => $.requests[STATUS_LABEL_KEYS[request.status]])}
          </Badge>
        </span>
        <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-caption text-muted-foreground">
          <span className="truncate">
            {t(($) => $.requests.origin_label, { agent: originName || request.origin_agent_id })}
          </span>
          <span aria-hidden>·</span>
          <span className="shrink-0">{timeAgo(request.created_at)}</span>
          {request.origin_issue_title && (
            <span className="truncate" title={request.origin_issue_title}>
              {t(($) => $.requests.source_issue, { title: request.origin_issue_title })}
            </span>
          )}
        </span>
        {request.detail && (
          <span className="whitespace-pre-wrap break-words text-caption text-muted-foreground">
            {request.detail}
          </span>
        )}
      </div>

      <div className="mt-2 flex flex-wrap items-center gap-2">
        {request.operable && isPending && (
          <>
            <Button
              size="sm"
              className="h-7"
              disabled={answer.isPending}
              data-testid="decision-request-approve"
              onClick={() =>
                answer.mutate({
                  workspaceId: request.workspace_id,
                  requestId: request.id,
                  decision: "approve",
                })
              }
            >
              {request.approve_label || t(($) => $.requests.approve)}
            </Button>
            <Button
              size="sm"
              variant="outline"
              className="h-7"
              disabled={answer.isPending}
              data-testid="decision-request-deny"
              onClick={() =>
                answer.mutate({
                  workspaceId: request.workspace_id,
                  requestId: request.id,
                  decision: "deny",
                })
              }
            >
              {request.deny_label || t(($) => $.requests.deny)}
            </Button>
          </>
        )}
        {!request.operable && isPending && (
          <span className="text-caption text-faint-foreground">
            {t(($) => $.requests.projection_hint)}
          </span>
        )}
        {isPending && (
          <Button
            size="sm"
            variant="ghost"
            className="h-7"
            disabled={cancel.isPending}
            data-testid="decision-request-cancel"
            onClick={() =>
              cancel.mutate({ workspaceId: request.workspace_id, requestId: request.id })
            }
          >
            {t(($) => $.requests.cancel)}
          </Button>
        )}
        {request.status === "execute_failed" && request.execution_error && (
          <span
            className="min-w-0 truncate text-caption text-destructive"
            title={request.execution_error}
          >
            {t(($) => $.requests.executed_failed, { reason: request.execution_error })}
          </span>
        )}
      </div>

      <RequestSteps request={request} />
    </div>
  );
}
