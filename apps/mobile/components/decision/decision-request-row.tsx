/**
 * Authorization request row (RUYI-630) — RN port of
 * packages/views/decisions/components/decision-request-row.tsx.
 *
 * One row of the mobile decisions tab's authorization section. The summary
 * is deliberately issue-free (决策中心点击不进 Issue): the origin issue shows
 * as title-level text and the row never deep-links into the thread. Operable
 * rows carry approve/deny (+ revoke for the creator/Owner); a read-only
 * projection shows a hint instead. The server enforces tier, window and
 * CAS — the row only needs to fail readably.
 */
import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Pressable, View } from "react-native";
import { useRouter } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { buildActorNameResolver } from "@multica/core/workspace/hooks";
import { decisionRequestKeys } from "@multica/core/issues/decision-requests";
import type { DecisionRequest } from "@multica/core/types";
import { agentListOptions } from "@/data/queries/agents";
import { useWorkspaceStore } from "@/data/workspace-store";
import { api } from "@/data/api";
import { Button } from "@/components/ui/button";
import { Text } from "@/components/ui/text";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { timeAgo } from "@/lib/time-ago";
import { useT } from "@/lib/use-t";
import { cn } from "@/lib/utils";
import { decisionRequestDetailOptions } from "@/data/queries/decisions";

const STATUS_LABEL_KEYS = {
  pending: "status_pending",
  approved: "status_approved",
  denied: "status_denied",
  expired: "status_expired",
  revoked: "status_revoked",
  executed: "status_executed",
  execute_failed: "status_execute_failed",
} as const;

function statusPillClass(status: DecisionRequest["status"]): string {
  switch (status) {
    case "pending":
      return "border-brand text-brand";
    case "execute_failed":
      return "border-destructive text-destructive";
    case "approved":
    case "executed":
      return "border-border text-muted-foreground";
    default:
      return "border-border text-muted-foreground";
  }
}

export function DecisionRequestRow({ request }: { request: DecisionRequest }) {
  const { t } = useT("decisions");
  const router = useRouter();
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId) ?? "";
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const [showPrompt, setShowPrompt] = useState(false);
  const [showSteps, setShowSteps] = useState(false);

  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const getActorName = useMemo(
    () => buildActorNameResolver({ members: [], agents, squads: [] }),
    [agents],
  );
  // The wire name wins: a cross-space origin agent is never in this
  // workspace's roster, so local resolution alone renders "Unknown Agent".
  const originName =
    request.origin_agent_name || getActorName("agent", request.origin_agent_id);
  const projectResults = request.action_type === "project_prompt_sync"
    && request.execution_result && typeof request.execution_result === "object"
    && Array.isArray((request.execution_result as { projects?: unknown }).projects)
    ? (request.execution_result as { projects: Array<{ project_id?: unknown; status?: unknown; version?: unknown }> }).projects
        .filter((item) => typeof item.project_id === "string" && typeof item.status === "string" && typeof item.version === "number")
    : [];
  const approvedPrompt = request.action_type === "project_prompt_sync"
    && request.action_params && typeof request.action_params === "object"
    && typeof (request.action_params as { content?: unknown }).content === "string"
    ? (request.action_params as { content: string }).content
    : null;
  const receipt = request.execution_result && typeof request.execution_result === "object"
    ? request.execution_result as { side_effect_status?: unknown; steps?: unknown }
    : null;
  const receiptSteps = Array.isArray(receipt?.steps)
    ? receipt.steps.filter((step): step is { handler: string; status: string; side_effect_status: string } =>
      !!step && typeof step === "object" && typeof (step as { handler?: unknown }).handler === "string"
      && typeof (step as { status?: unknown }).status === "string"
      && typeof (step as { side_effect_status?: unknown }).side_effect_status === "string")
    : [];
  const detail = useQuery({
    ...decisionRequestDetailOptions(request.workspace_id, request.id),
    enabled: showSteps,
  });

  const refresh = () => {
    qc.invalidateQueries({ queryKey: decisionRequestKeys.all() });
  };

  const answer = useMutation({
    mutationFn: (decision: "approve" | "deny") =>
      api.answerDecisionRequest(request.workspace_id, request.id, decision),
    onSuccess: refresh,
    onError: () => {
      refresh();
      Alert.alert(t("requests.answer_failed", "Failed to record your answer"));
    },
  });

  const cancel = useMutation({
    mutationFn: () =>
      api.cancelDecisionRequest(request.workspace_id, request.id),
    onSuccess: refresh,
    onError: () => {
      refresh();
      Alert.alert(t("requests.cancel_failed", "Failed to revoke"));
    },
  });

  const isPending = request.status === "pending";
  const actionLabel = request.action_type === "prompt_restore"
    ? t("requests.action_prompt_restore", "Restore prompt")
    : request.action_type === "project_prompt_sync"
      ? t("requests.action_project_prompt_sync", "Sync project prompt")
    : request.action_type === "workspace_info_read"
      ? t("requests.action_workspace_info_read", "Read workspace info")
      : request.action_type;
  const riskLabel = request.risk_tier === "write_low"
    ? t("requests.risk_write_low", "Low-risk write")
    : request.risk_tier === "read"
      ? t("requests.risk_read", "Read-only")
      : request.risk_tier;
  const kindLabel = request.authorization_spec?.kind === "cli"
    ? t("requests.kind_cli", "Whitelisted operation")
    : request.authorization_spec?.kind === "restricted_script"
      ? t("requests.kind_script", "Restricted script")
      : request.authorization_spec?.kind === "issue_creation"
        ? t("requests.kind_issue", "Single issue task")
        : null;
  const executionInProgress = request.status === "approved"
    && (request.role === "target" || (request.role === "origin" && request.operable));

  return (
    <View className="px-4 py-2" testID="decision-request-row">
      <View className="flex-row flex-wrap items-center gap-2">
        <Ionicons
          name="shield-checkmark-outline"
          size={14}
          color={theme.brand}
        />
        <Text className="shrink text-sm font-medium" numberOfLines={1}>
          {request.title}
        </Text>
        <Text className="shrink-0 rounded-full border border-border px-2 py-0.5 text-xs text-muted-foreground">
          {actionLabel}
        </Text>
        {kindLabel ? (
          <Text className="shrink-0 rounded-full border border-border px-2 py-0.5 text-xs text-muted-foreground">
            {kindLabel}
          </Text>
        ) : null}
        <Text className="shrink-0 rounded-full border border-border px-2 py-0.5 text-xs text-muted-foreground">
          {riskLabel}
        </Text>
        <Text
          className={cn(
            "shrink-0 rounded-full border px-2 py-0.5 text-xs",
            statusPillClass(request.status),
          )}
        >
          {t(`requests.${STATUS_LABEL_KEYS[request.status]}`, String(request.status))}
        </Text>
        {executionInProgress ? (
          <Text className="text-xs text-muted-foreground" accessibilityRole="text" accessibilityLiveRegion="polite" testID="decision-request-executing">
            {t("requests.execution_in_progress", "Approvals complete; execution in progress")}
          </Text>
        ) : null}
      </View>
      <Text className="mt-0.5 text-xs text-muted-foreground" numberOfLines={1}>
        {t("requests.origin_label", "Requested by {{agent}}", {
          agent: originName || request.origin_agent_id,
        })}
        {" · "}
        {timeAgo(request.created_at)}
      </Text>
      {request.origin_issue_title ? (
        <Text className="mt-0.5 text-xs text-muted-foreground" numberOfLines={1}>
          {t("requests.source_issue", "From issue: {{title}}", {
            title: request.origin_issue_title,
          })}
        </Text>
      ) : null}
      {approvedPrompt !== null ? (
        <View className="mt-2">
          <Pressable onPress={() => setShowPrompt((value) => !value)} accessibilityRole="button">
            <Text className="text-xs text-muted-foreground">
              {showPrompt
                ? t("requests.hide_prompt_content", "Hide approved prompt")
                : t("requests.review_prompt_content", "Review approved prompt")}
            </Text>
          </Pressable>
          {showPrompt ? <Text className="mt-1 text-xs text-muted-foreground">{approvedPrompt}</Text> : null}
        </View>
      ) : null}

      {isPending ? (
        <View className="mt-2 flex-row items-center gap-2">
          {request.operable ? (
            <>
              <Button
                size="sm"
                disabled={answer.isPending}
                onPress={() => answer.mutate("approve")}
                testID="decision-request-approve"
              >
                <Text>
                  {request.approve_label ||
                    t("requests.approve", "Approve")}
                </Text>
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={answer.isPending}
                onPress={() => answer.mutate("deny")}
                testID="decision-request-deny"
              >
                <Text>
                  {request.deny_label || t("requests.deny", "Deny")}
                </Text>
              </Button>
            </>
          ) : (
            request.target_workspace_slug ? (
              <Pressable
                className="flex-row items-center gap-1"
                onPress={() => router.push({
                  pathname: "/[workspace]/(tabs)/decisions",
                  params: { workspace: request.target_workspace_slug! },
                })}
                accessibilityRole="link"
                testID="decision-request-target-link"
              >
                <Text className="text-xs text-brand">
                  {t("requests.open_target_workspace", "Open target workspace")}
                </Text>
                <Ionicons name="arrow-forward-outline" size={14} color={theme.brand} />
              </Pressable>
            ) : (
              <Text className="shrink text-xs text-muted-foreground">
                {t(
                  "requests.projection_hint",
                  "This row is a read-only projection — act in the target workspace",
                )}
              </Text>
            )
          )}
          <Pressable
            className="ml-auto px-2 py-1"
            disabled={cancel.isPending}
            onPress={() => cancel.mutate()}
            testID="decision-request-cancel"
          >
            <Text className="text-xs text-muted-foreground">
              {t("requests.cancel", "Revoke request")}
            </Text>
          </Pressable>
        </View>
      ) : null}

      {request.role === "origin" ? (
        <View className="mt-2">
          <Pressable
            onPress={() => setShowSteps((value) => !value)}
            accessibilityRole="button"
            accessibilityState={{ expanded: showSteps }}
            accessibilityLabel={t("requests.steps_title", "Approval timeline")}
            testID="decision-request-steps-toggle"
          >
            <Text className="text-xs text-muted-foreground">
              {showSteps ? "⌄ " : "› "}{t("requests.steps_title", "Approval timeline")}
            </Text>
          </Pressable>
          {showSteps ? (
            <View className="mt-1 border-l border-border pl-3" accessibilityRole="list">
              {detail.isPending ? (
                <Text accessibilityRole="text" accessibilityLiveRegion="polite" className="text-xs text-muted-foreground">
                  {t("requests.steps_loading", "Loading approval steps")}
                </Text>
              ) : null}
              {detail.isError ? (
                <Text accessibilityRole="alert" className="text-xs text-destructive">
                  {t("requests.detail_error", "Unable to load approval steps")}
                </Text>
              ) : null}
              {(detail.data?.steps ?? []).map((step) => (
                <View key={step.id} className="flex-row flex-wrap gap-x-2 py-0.5" accessibilityLabel={`${step.role} ${step.status}`} testID="decision-request-step">
                  <Text className="text-xs font-medium">
                    {step.role === "target"
                      ? t("requests.step_target", "Target space")
                      : t("requests.step_origin", "Origin space")}
                  </Text>
                  <Text className="text-xs text-muted-foreground">
                    {t(`requests.${STATUS_LABEL_KEYS[step.status]}`, String(step.status))}
                  </Text>
                  {step.answered_at ? (
                    <Text className="text-xs text-muted-foreground">{timeAgo(step.answered_at)}</Text>
                  ) : null}
                </View>
              ))}
            </View>
          ) : null}
        </View>
      ) : null}

      {request.status === "execute_failed" && request.execution_error ? (
        <Text className="mt-1 text-xs text-destructive" numberOfLines={2}>
          {t("requests.executed_failed", "Execution failed; it was not retried")}
        </Text>
      ) : null}
      {receipt?.side_effect_status === "unknown" ? (
        <Text className="mt-1 text-xs font-medium text-destructive" accessibilityRole="text" accessibilityLiveRegion="polite" testID="decision-request-result-unknown">
          {t("requests.result_unknown", "Result needs verification. Do not retry this request.")}
        </Text>
      ) : null}
      {receipt?.side_effect_status === "no_side_effect" ? (
        <Text className="mt-1 text-xs text-muted-foreground" testID="decision-request-no-side-effect">
          {t("requests.no_side_effect", "No side effect confirmed")}
        </Text>
      ) : null}
      {receiptSteps.map((step, index) => (
        <Text key={`${step.handler}-${index}`} className="mt-1 text-xs text-muted-foreground" testID="decision-request-receipt-step">
          {step.handler} · {step.status} · {step.side_effect_status}
        </Text>
      ))}
      {projectResults.map((result) => (
        <Text key={String(result.project_id)} className="mt-1 text-xs text-muted-foreground" testID="decision-request-project-result">
          {String(result.project_id)} · {String(result.status)} · v{String(result.version)}
        </Text>
      ))}
    </View>
  );
}
