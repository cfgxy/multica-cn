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
import { useMemo } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Pressable, View } from "react-native";
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
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId) ?? "";
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];

  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const getActorName = useMemo(
    () => buildActorNameResolver({ members: [], agents, squads: [] }),
    [agents],
  );
  const originName = getActorName("agent", request.origin_agent_id);

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
    : request.action_type === "workspace_info_read"
      ? t("requests.action_workspace_info_read", "Read workspace info")
      : request.action_type;
  const riskLabel = request.risk_tier === "write_low"
    ? t("requests.risk_write_low", "Low-risk write")
    : request.risk_tier === "read"
      ? t("requests.risk_read", "Read-only")
      : request.risk_tier;

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
            <Text className="shrink text-xs text-muted-foreground">
              {t(
                "requests.projection_hint",
                "This row is a read-only projection — act in the target workspace",
              )}
            </Text>
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

      {request.status === "execute_failed" && request.execution_error ? (
        <Text className="mt-1 text-xs text-destructive" numberOfLines={2}>
          {t("requests.executed_failed", "Execution failed: {{reason}}", {
            reason: request.execution_error,
          })}
        </Text>
      ) : null}
    </View>
  );
}
