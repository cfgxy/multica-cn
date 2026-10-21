/**
 * Decision batch bar (RUYI-471) — RN port of
 * packages/views/issues/components/decision-batch-bar.tsx.
 *
 * Shows one card at a time while keeping the existing batch request contract.
 * Single-card answering stays on DecisionCard untouched.
 *
 * Semantics mirror the DOM bar exactly: single-select rows replace the pick,
 * multi-select rows toggle sorted, cards without picks stay out of the
 * request (partial answers are server-supported), per-card failures never
 * roll the batch back — answered halves patch the cache, the rest triggers
 * a refetch plus a readable alert.
 */
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Alert, Pressable, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import {
  decisionOptionDisplayLabel,
  upsertDecisionInCache,
} from "@multica/core/issues/decisions";
import { issueKeys } from "@multica/core/issues/queries";
import type { IssueDecision } from "@multica/core/types";
import { api } from "@/data/api";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Text } from "@/components/ui/text";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";
import { cn } from "@/lib/utils";

const OPTION_LETTERS = ["A", "B", "C", "D"] as const;

export function DecisionBatchBar({
  issueId,
  open,
}: {
  issueId: string;
  open: IssueDecision[];
}) {
  const { t } = useT("issues");
  const qc = useQueryClient();
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const [picks, setPicks] = useState<Record<string, number[]>>({});
  const [submitting, setSubmitting] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const currentIndex = Math.min(activeIndex, Math.max(0, open.length - 1));
  const decision = open[currentIndex];

  const toggle = (decision: IssueDecision, idx: number) => {
    if (submitting) return;
    setPicks((prev) => {
      const cur = prev[decision.id] ?? [];
      let next: number[];
      if (cur.includes(idx)) {
        next = cur.filter((i) => i !== idx);
      } else {
        next = decision.multi_select ? [...cur, idx].sort((a, b) => a - b) : [idx];
      }
      return { ...prev, [decision.id]: next };
    });
  };

  const canSubmit =
    open.some((d) => (picks[d.id]?.length ?? 0) > 0) && !submitting;

  const submit = async () => {
    setSubmitting(true);
    try {
      const answers = open
        .filter((d) => (picks[d.id]?.length ?? 0) > 0)
        .map((d) => ({ decision_id: d.id, selected_indices: picks[d.id]! }));
      const resp = await api.answerIssueDecisionsBatch(issueId, answers);
      for (const r of resp.results) {
        if (r.status === "answered" && r.decision) {
          upsertDecisionInCache(qc, issueId, r.decision);
        }
      }
      if (resp.results.some((r) => r.status !== "answered")) {
        // Per-card failures never roll the batch back — refetch so losing
        // cards flip to whoever won the race, then say the submit failed.
        qc.invalidateQueries({ queryKey: issueKeys.decisions(issueId) });
        Alert.alert(t("decisions.answer_failed", "Failed to submit answer"));
      }
      setPicks({});
    } catch {
      qc.invalidateQueries({ queryKey: issueKeys.decisions(issueId) });
      Alert.alert(t("decisions.answer_failed", "Failed to submit answer"));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <View className="px-4" testID="decision-batch-bar">
      <Card>
        <View className="flex-row items-center gap-2">
          <Ionicons
            name="list-outline"
            size={16}
            color={theme.brand}
          />
          <Text className="text-xs font-medium text-muted-foreground">
            {t("decisions.batch_title", "Batch answer")}
          </Text>
          <Text
            className="rounded-full bg-secondary px-2 py-0.5 text-xs text-secondary-foreground"
            testID="decision-batch-count"
          >
            {open.length}
          </Text>
        </View>

        {decision && <View className="mt-3" testID={`decision-batch-card-${decision.id}`}>
          <View className="flex-row items-start justify-between gap-3">
            <Text className="flex-1 text-sm font-medium">{decision.question}</Text>
            <Text className="text-xs text-muted-foreground">{currentIndex + 1} / {open.length}</Text>
          </View>
          {decision.context && <View className="mt-2 gap-1">
            <Text className="text-xs font-semibold">{t("decisions.context_title", "Decision context")}</Text>
            {decision.context.why_now ? <Text className="text-xs text-muted-foreground">{t("decisions.context_why_now", "Why now")}: {decision.context.why_now}</Text> : null}
            {decision.context.facts?.length ? <Text className="text-xs font-medium">{t("decisions.context_facts", "Confirmed facts and evidence")}</Text> : null}
            {decision.context.facts?.map((fact, index) => <Text key={`${fact.statement}-${index}`} className="text-xs text-muted-foreground">{fact.statement}{fact.evidence?.length ? `\n${fact.evidence.join("\n")}` : ""}</Text>)}
            {decision.context.impact ? <Text className="text-xs text-muted-foreground">{t("decisions.context_impact", "Impact")}: {decision.context.impact}</Text> : null}
            {decision.context.recommendation_reason ? <Text className="text-xs text-muted-foreground">{t("decisions.context_recommendation", "Recommendation rationale")}: {decision.context.recommendation_reason}</Text> : null}
            {decision.context.unknowns?.length ? <Text className="text-xs font-medium">{t("decisions.context_unknowns", "Unknowns")}</Text> : null}
            {decision.context.unknowns?.map((unknown, index) => <Text key={`${unknown}-${index}`} className="text-xs text-muted-foreground">{unknown}</Text>)}
          </View>}
          <View className="mt-2 gap-1.5">
            {decision.options.map((opt, idx) => {
              const isSelected = (picks[decision.id] ?? []).includes(idx);
              return (
                <Pressable key={idx} testID={`decision-batch-option-${decision.id}-${idx}`} accessibilityRole={decision.multi_select ? "checkbox" : "radio"} accessibilityState={{ checked: isSelected, disabled: submitting }} disabled={submitting} onPress={() => toggle(decision, idx)} className={cn("flex-row items-center gap-1.5 rounded-md border px-2.5 py-2", isSelected ? "border-brand bg-accent/40" : "border-border")}>
                  <Text className={cn("text-xs font-medium", isSelected ? "text-brand" : "text-muted-foreground")}>{OPTION_LETTERS[idx] ?? idx + 1}</Text>
                  <Text className="flex-1 text-xs">{decisionOptionDisplayLabel(opt.label)}</Text>
                </Pressable>
              );
            })}
          </View>
        </View>}

        <View className="mt-3 flex-row items-center justify-between gap-2">
          <Button variant="outline" size="sm" disabled={currentIndex === 0 || submitting} onPress={() => setActiveIndex((index) => Math.max(0, index - 1))} testID="decision-batch-previous">
            <Ionicons name="chevron-back" size={16} /><Text>{t("decisions.previous", "Previous")}</Text>
          </Button>
          {currentIndex < open.length - 1 ? (
            <Button size="sm" disabled={submitting} onPress={() => setActiveIndex((index) => Math.min(open.length - 1, index + 1))} testID="decision-batch-next">
              <Text>{t("decisions.next", "Next")}</Text><Ionicons name="chevron-forward" size={16} />
            </Button>
          ) : (
            <Button size="sm" disabled={!canSubmit} onPress={() => void submit()} testID="decision-batch-submit">
              <Text>{submitting ? t("decisions.submitting", "Submitting…") : t("decisions.batch_submit", "Submit all")}</Text>
            </Button>
          )}
        </View>
      </Card>
    </View>
  );
}
