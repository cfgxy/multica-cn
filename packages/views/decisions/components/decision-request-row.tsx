"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ArrowUpRight, ChevronDown, ChevronRight, ShieldCheck } from "lucide-react";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { paths } from "@multica/core/paths";
import type { DecisionRequest } from "@multica/core/types";
import {
  answerDecisionRequest,
  cancelDecisionRequest,
  decisionRequestKeys,
  decisionRequestDetailQueryOptions,
} from "@multica/core/issues/decision-requests";
import { useActorName } from "@multica/core/workspace/hooks";
import { useT, useTimeAgo } from "../../i18n";
import { AppLink } from "../../navigation";

// One row of the decision center's authorization-requests section
// (RUYI-630). The summary is deliberately issue-free: the origin issue shows
// as title-level text and the row never deep-links into the issue thread
// (决策中心点击不进 Issue). Operable rows carry the answer buttons; a
// read-only projection (the origin space's row when the target is another
// space, or when an issue card carries the step) shows a hint instead.

type DecisionRequestStatus = DecisionRequest["status"];

const ACTION_LABEL_KEYS: Record<string, "action_workspace_info_read" | "action_prompt_restore" | "action_project_prompt_sync" | "action_single_issue_create"> = {
  workspace_info_read: "action_workspace_info_read",
  prompt_restore: "action_prompt_restore",
  project_prompt_sync: "action_project_prompt_sync",
  single_issue_create: "action_single_issue_create",
};

const RISK_LABEL_KEYS: Record<string, "risk_read" | "risk_write_low"> = {
  read: "risk_read",
  write_low: "risk_write_low",
};

const KIND_LABEL_KEYS = {
  cli: "kind_cli",
  restricted_script: "kind_script",
  issue_creation: "kind_issue",
  single_issue: "kind_issue",
} as const;

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

function projectPromptResults(value: unknown): Array<{ projectId: string; status: string; version: number }> {
  if (!value || typeof value !== "object") return [];
  const projects = (value as { projects?: unknown }).projects;
  if (!Array.isArray(projects)) return [];
  return projects.flatMap((item) => {
    if (!item || typeof item !== "object") return [];
    const result = item as { project_id?: unknown; status?: unknown; version?: unknown };
    if (typeof result.project_id !== "string" || typeof result.status !== "string" || typeof result.version !== "number") return [];
    return [{ projectId: result.project_id, status: result.status, version: result.version }];
  });
}

/** Per-space step lines for cross-space requests (方案甲 dual-row carrier). */
function RequestSteps({ request }: { request: DecisionRequest }) {
  const { t } = useT("decisions");
  const timeAgo = useTimeAgo();
  const { getActorName } = useActorName();
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
        aria-expanded={open}
        aria-controls={`decision-request-steps-${request.id}`}
        onClick={() => setOpen((prev) => !prev)}
      >
        {open ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
        {t(($) => $.requests.steps_title)}
      </button>
      {open && (
        <ol id={`decision-request-steps-${request.id}`} className="mt-1 flex flex-col gap-1 border-l pl-3" aria-label={t(($) => $.requests.steps_title)}>
          {detail.isPending && <li role="status"><Skeleton className="h-4 w-40" /></li>}
          {detail.isError && (
            <li role="alert" className="text-caption text-destructive">
              {t(($) => $.requests.detail_error)}
            </li>
          )}
          {(detail.data?.steps ?? []).map((step) => (
            <li
              key={step.id}
              className="flex items-center gap-2 text-caption text-muted-foreground"
              data-testid="decision-request-step"
            >
              <span className="shrink-0 font-medium">
                {step.role === "target"
                  ? t(($) => $.requests.step_target)
                  : t(($) => $.requests.step_origin)}
              </span>
              <span>{t(($) => $.requests[STATUS_LABEL_KEYS[step.status]])}</span>
              {step.answered_by_id && (
                <span>
                  {t(($) => $.requests.approved_by, {
                    actor: getActorName(step.answered_by_type ?? "member", step.answered_by_id),
                  })}
                </span>
              )}
              {step.answered_at && <time dateTime={step.answered_at}>{timeAgo(step.answered_at)}</time>}
            </li>
          ))}
        </ol>
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
  const targetPath =
    !request.operable && isPending && request.target_workspace_slug
      ? paths.workspace(request.target_workspace_slug).decisions()
      : null;
  // The wire name wins: a cross-space origin agent is never in this
  // workspace's roster, so local resolution alone renders "Unknown Agent".
  const originName =
    request.origin_agent_name || getActorName("agent", request.origin_agent_id);
  const syncResults = request.action_type === "project_prompt_sync"
    ? projectPromptResults(request.execution_result)
    : [];
  const approvedPrompt = request.action_type === "project_prompt_sync"
    && request.action_params && typeof request.action_params === "object"
    && typeof (request.action_params as { content?: unknown }).content === "string"
    ? (request.action_params as { content: string }).content
    : null;
  const receipt = request.execution_result && typeof request.execution_result === "object"
    ? request.execution_result as {
      side_effect_status?: unknown;
      steps?: unknown;
      target_identifier?: unknown;
      target_issue_status?: unknown;
      done_receipt?: { status?: unknown };
    }
    : null;
  const receiptSteps = Array.isArray(receipt?.steps)
    ? receipt.steps.filter((step): step is { handler: string; status: string; side_effect_status: string } =>
      !!step && typeof step === "object" && typeof (step as { handler?: unknown }).handler === "string"
      && typeof (step as { status?: unknown }).status === "string"
      && typeof (step as { side_effect_status?: unknown }).side_effect_status === "string")
    : [];
  const executionInProgress = request.status === "approved"
    && (request.role === "target" || (request.role === "origin" && request.operable));
  const kindLabelKey = request.authorization_spec
    ? KIND_LABEL_KEYS[request.authorization_spec.kind]
    : null;

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

  const content = (
    <>
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
    </>
  );

  return (
    <div
      className="rounded-lg px-3 py-2 hover:bg-surface-hover"
      data-testid="decision-request-row"
      data-status={request.status}
    >
      <div
        className="flex min-w-0 flex-col gap-1"
        data-testid="decision-request-row-content"
      >
        {targetPath ? (
          <AppLink
            href={targetPath}
            className="flex min-w-0 flex-col gap-1 rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            data-testid="decision-request-target-link"
          >
            {content}
            <span className="inline-flex items-center gap-1 text-caption text-brand">
              {t(($) => $.requests.open_target_workspace)}
              <ArrowUpRight className="size-3.5" aria-hidden />
            </span>
          </AppLink>
        ) : (
          content
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
        {!request.operable && isPending && !targetPath && (
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
            {t(($) => $.requests.executed_failed)}
          </span>
        )}
        {receipt?.side_effect_status === "unknown" && (
          <span className="text-caption font-medium text-destructive" role="status" aria-live="polite" data-testid="decision-request-result-unknown">
            {t(($) => $.requests.result_unknown)}
          </span>
        )}
        {receipt?.side_effect_status === "no_side_effect" && (
          <span className="text-caption text-muted-foreground" data-testid="decision-request-no-side-effect">
            {t(($) => $.requests.no_side_effect)}
          </span>
        )}
        {request.action_type === "single_issue_create" && typeof receipt?.target_identifier === "string" && (
          <span className="text-caption text-muted-foreground" data-testid="decision-request-created-issue">
            {t(($) => $.requests.issue_created, {
              identifier: receipt.target_identifier,
              status: receipt.done_receipt?.status === "done" ? "done" : String(receipt.target_issue_status ?? "in_progress"),
            })}
          </span>
        )}
        {receiptSteps.map((step, index) => (
          <span key={`${step.handler}-${index}`} className="text-caption text-muted-foreground" data-testid="decision-request-receipt-step">
            {t(($) => $.requests.receipt_step, {
              handler: step.handler === "workspace_info_read"
                ? t(($) => $.requests.action_workspace_info_read)
                : step.handler === "prompt_restore"
                  ? t(($) => $.requests.action_prompt_restore)
                  : step.handler === "project_prompt_sync"
                    ? t(($) => $.requests.action_project_prompt_sync)
                    : step.handler,
              status: t(($) => $.requests[step.status === "completed" ? "receipt_completed" : "receipt_failed"]),
              effect: t(($) => $.requests[step.side_effect_status === "completed"
                ? "side_effect_completed"
                : step.side_effect_status === "no_side_effect"
                  ? "no_side_effect"
                  : "result_unknown"]),
            })}
          </span>
        ))}
        {syncResults.map((result) => (
          <span key={result.projectId} className="min-w-0 text-caption text-muted-foreground" data-testid="decision-request-project-result">
            {result.projectId} · {result.status} · v{result.version}
          </span>
        ))}
      </div>

      <RequestSteps request={request} />
    </div>
  );
}
