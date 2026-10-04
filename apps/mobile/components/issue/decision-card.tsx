/**
 * Decision card (RUYI-345) — RN port of
 * packages/views/issues/components/decision-card.tsx.
 *
 * An agent run's structured question rendered in the issue comment stream.
 * Open cards take picks from human members; the server refuses agent
 * callers, so the UI only needs to fail readably — the card is the gate,
 * the server is the authority.
 *
 * Semantics mirror the DOM card exactly: single-select replaces the pick,
 * multi-select toggles, answered/cancelled cards render read-only with the
 * picked options marked. Answers go through the mobile-owned api wrapper;
 * the returned card patches the shared core-keyed decisions cache.
 *
 * Directory-backed name resolution uses the mobile queries + core's pure
 * `buildActorNameResolver` — core's `useActorName` hook is NOT reused
 * because its query options call core's unconfigured api singleton.
 */
import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, Pressable, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { buildActorNameResolver } from "@multica/core/workspace/hooks";
import { upsertDecisionInCache } from "@multica/core/issues/decisions";
import { issueKeys } from "@multica/core/issues/queries";
import type { IssueDecision } from "@multica/core/types";
import { agentListOptions } from "@/data/queries/agents";
import { memberListOptions } from "@/data/queries/members";
import { squadListOptions } from "@/data/queries/squads";
import { api } from "@/data/api";
import { useWorkspaceStore } from "@/data/workspace-store";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Text } from "@/components/ui/text";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { timeAgo } from "@/lib/time-ago";
import { useT } from "@/lib/use-t";
import { cn } from "@/lib/utils";

const EMPTY_MEMBERS: { user_id: string; name: string }[] = [];
const EMPTY_AGENTS: { id: string; name: string }[] = [];
const EMPTY_SQUADS: { id: string; name: string }[] = [];

export function DecisionCard({ decision }: { decision: IssueDecision }) {
  const { t } = useT("issues");
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const [selected, setSelected] = useState<number[]>([]);
  const [submitting, setSubmitting] = useState(false);

  const { data: members = EMPTY_MEMBERS } = useQuery(
    memberListOptions(wsId),
  );
  const { data: agents = EMPTY_AGENTS } = useQuery(agentListOptions(wsId));
  const { data: squads = EMPTY_SQUADS } = useQuery(squadListOptions(wsId));
  const getActorName = useMemo(
    () => buildActorNameResolver({ members, agents, squads }),
    [members, agents, squads],
  );

  const creatorName = getActorName(decision.created_by_type, decision.created_by_id);
  const answeredName = decision.answered_by_type
    ? getActorName(decision.answered_by_type, decision.answered_by_id ?? "")
    : null;

  const recommended = useMemo(
    () => new Set(decision.recommended_indices),
    [decision.recommended_indices],
  );

  const toggle = (idx: number) => {
    if (decision.status !== "open" || submitting) return;
    setSelected((prev) => {
      if (prev.includes(idx)) return prev.filter((i) => i !== idx);
      return decision.multi_select ? [...prev, idx] : [idx];
    });
  };

  const answer = useMutation({
    mutationFn: () =>
      api.answerIssueDecision(decision.issue_id, decision.id, selected),
    onSuccess: (updated) => {
      upsertDecisionInCache(qc, updated.issue_id, updated);
      setSelected([]);
    },
    onError: () => {
      // 409 (someone answered/cancelled first) is the common race — refetch
      // so the card flips to its terminal state instead of sitting stale-open.
      qc.invalidateQueries({ queryKey: issueKeys.decisions(decision.issue_id) });
      Alert.alert(t("decisions.answer_failed", "Failed to submit answer"));
    },
    onSettled: () => setSubmitting(false),
  });

  const cancel = useMutation({
    mutationFn: () => api.cancelIssueDecision(decision.issue_id, decision.id),
    onSuccess: (updated) => {
      upsertDecisionInCache(qc, updated.issue_id, updated);
    },
    onError: () => {
      qc.invalidateQueries({ queryKey: issueKeys.decisions(decision.issue_id) });
      Alert.alert(t("decisions.cancel_failed", "Failed to cancel card"));
    },
  });

  const isOpen = decision.status === "open";
  const interactive = isOpen && !submitting;
  const canSubmit = isOpen && selected.length > 0 && !submitting && !answer.isPending;

  return (
    <View className="px-4" testID="decision-card">
      <Card>
        <View className="flex-row items-start gap-2">
          <Ionicons
            name="help-circle-outline"
            size={16}
            color={theme.brand}
            style={{ marginTop: 2 }}
          />
          <View className="flex-1">
            <View className="flex-row flex-wrap items-center gap-2">
              <Text className="text-xs font-medium text-muted-foreground">
                {t("decisions.title", "Decision card")}
              </Text>
              {decision.status === "answered" && (
                <Text className="rounded-full bg-secondary px-2 py-0.5 text-xs text-secondary-foreground">
                  {t("decisions.status_answered", "Answered")}
                </Text>
              )}
              {decision.status === "cancelled" && (
                <Text className="rounded-full border border-border px-2 py-0.5 text-xs text-muted-foreground">
                  {t("decisions.status_cancelled", "Cancelled")}
                </Text>
              )}
            </View>
            <Text className="mt-1 text-sm font-medium">{decision.question}</Text>
            <Text className="mt-0.5 text-xs text-muted-foreground">
              {creatorName} · {timeAgo(decision.created_at)}
            </Text>
          </View>
        </View>

        <View
          className="mt-3 gap-1.5"
          accessibilityLabel={decision.question}
        >
          {decision.options.map((opt, idx) => {
            const isSelected = isOpen
              ? selected.includes(idx)
              : decision.selected_indices.includes(idx);
            return (
              <Pressable
                key={idx}
                testID={`decision-option-${idx}`}
                accessibilityRole={decision.multi_select ? "checkbox" : "radio"}
                accessibilityState={{ checked: isSelected, disabled: !interactive }}
                disabled={!interactive}
                onPress={() => toggle(idx)}
                className={cn(
                  "flex-row items-start gap-2 rounded-md border px-3 py-2",
                  isSelected ? "border-brand bg-accent/40" : "border-border",
                  !isOpen && "opacity-70",
                )}
              >
                <View
                  className={cn(
                    "mt-0.5 h-4 w-4 shrink-0 border",
                    decision.multi_select ? "rounded-sm" : "rounded-full",
                    isSelected ? "border-brand bg-brand" : "border-muted-foreground/50",
                  )}
                />
                <Text className="flex-1">{opt.label}</Text>
                {recommended.has(idx) && (
                  <Text className="shrink-0 rounded-full border border-border px-2 py-0.5 text-xs text-muted-foreground">
                    {t("decisions.recommended", "Recommended")}
                  </Text>
                )}
              </Pressable>
            );
          })}
        </View>

        {isOpen && (
          <View className="mt-3 flex-row items-center gap-2">
            <Button
              size="sm"
              disabled={!canSubmit}
              onPress={() => {
                setSubmitting(true);
                answer.mutate();
              }}
              testID="decision-submit"
            >
              <Text>
                {answer.isPending || submitting
                  ? t("decisions.submitting", "Submitting…")
                  : decision.multi_select
                    ? t("decisions.submit_multi", "Submit selection")
                    : t("decisions.submit_single", "Submit answer")}
              </Text>
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={cancel.isPending || submitting}
              onPress={() => cancel.mutate()}
              testID="decision-cancel"
            >
              <Text>{t("decisions.cancel_card", "Cancel card")}</Text>
            </Button>
          </View>
        )}

        {decision.status === "answered" && (
          <View className="mt-3 flex-row items-center gap-2">
            <Ionicons
              name="checkmark-done-outline"
              size={14}
              color={theme.mutedForeground}
            />
            <Text className="text-xs text-muted-foreground">
              {answeredName ?? ""}
              {decision.answered_at ? ` · ${timeAgo(decision.answered_at)}` : ""}
            </Text>
          </View>
        )}
      </Card>
    </View>
  );
}
