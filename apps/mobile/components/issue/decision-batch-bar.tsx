/**
 * Decision batch bar (RUYI-471) — RN port of
 * packages/views/issues/components/decision-batch-bar.tsx.
 *
 * Rendered above the first open card when an issue has two or more. One
 * pass of picks, one submit, one shared echo comment — the compact text
 * "1A 2B" the server numbers cards by is this bar's order (created_at ASC).
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
import { upsertDecisionInCache } from "@multica/core/issues/decisions";
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

        <View className="mt-3 gap-3">
          {open.map((decision, cardIdx) => (
            <View key={decision.id} testID={`decision-batch-card-${decision.id}`}>
              <View className="flex-row">
                <Text className="text-sm font-medium text-muted-foreground">{cardIdx + 1}. </Text>
                <Text className="flex-1 text-sm font-medium">{decision.question}</Text>
              </View>
              <View
                className="mt-1.5 flex-row flex-wrap gap-1.5"
                accessibilityLabel={decision.question}
              >
                {decision.options.map((opt, idx) => {
                  const isSelected = (picks[decision.id] ?? []).includes(idx);
                  return (
                    <Pressable
                      key={idx}
                      testID={`decision-batch-option-${decision.id}-${idx}`}
                      accessibilityRole={decision.multi_select ? "checkbox" : "radio"}
                      accessibilityState={{ checked: isSelected, disabled: submitting }}
                      disabled={submitting}
                      onPress={() => toggle(decision, idx)}
                      className={cn(
                        "flex-row items-center gap-1.5 rounded-md border px-2.5 py-1.5",
                        isSelected ? "border-brand bg-accent/40" : "border-border",
                      )}
                    >
                      <Text
                        className={cn(
                          "text-xs font-medium",
                          isSelected ? "text-brand" : "text-muted-foreground",
                        )}
                      >
                        {OPTION_LETTERS[idx] ?? idx + 1}
                      </Text>
                      <Text className="text-xs">{opt.label}</Text>
                    </Pressable>
                  );
                })}
              </View>
            </View>
          ))}
        </View>

        <View className="mt-3">
          <Button
            size="sm"
            disabled={!canSubmit}
            onPress={() => void submit()}
            testID="decision-batch-submit"
          >
            <Text>
              {submitting
                ? t("decisions.submitting", "Submitting…")
                : t("decisions.batch_submit", "Submit all")}
            </Text>
          </Button>
        </View>
      </Card>
    </View>
  );
}
