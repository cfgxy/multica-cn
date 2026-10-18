/**
 * Issue suspension UI (RUYI-690) — the detail-screen banner and the tiny
 * list-row chip, mobile parity of web's suspension-banner.tsx /
 * suspension-badge.tsx.
 *
 * The banner reads the ACTIVE suspension from the shared suspensions cache
 * (core `issueKeys` — the same entries web/desktop write) for the wait
 * target, resume condition and reason, and offers one-tap 恢复 / 取消等待.
 * Release goes through the mobile fetch wrapper; on success the returned
 * issue snapshot patches the mobile issue-detail cache in place
 * (suspended_at clears → the banner unmounts without a refetch round-trip —
 * mobile has no suspension realtime handler yet), and the suspensions
 * subtree invalidates so an open 挂起中心 rehydrates.
 *
 * SuspensionChip is the `issue.suspended_at` marker for virtualized list
 * rows (IssueRow): a violet pill naming the state, no catalog dependency —
 * suspended_at is orthogonal to lifecycle status by design.
 */
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { View } from "react-native";
import { issueKeys as coreIssueKeys } from "@multica/core/issues/queries";
import type { Issue } from "@multica/core/types";
import { Button } from "@/components/ui/button";
import { Text } from "@/components/ui/text";
import { api } from "@/data/api";
import { issueKeys } from "@/data/queries/issue-keys";
import { issueSuspensionsOptions } from "@/data/queries/suspensions";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useT } from "@/lib/use-t";

export function SuspensionBanner({ issue }: { issue: Issue }) {
  const { t } = useT("issues");
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const [error, setError] = useState<string | null>(null);
  const { data: suspensions } = useQuery(issueSuspensionsOptions(issue.id));
  const active = suspensions?.find((s) => s.status === "active");

  const release = useMutation({
    mutationFn: async (mode: "resume" | "cancel") => {
      if (!active) throw new Error("no active suspension");
      return mode === "resume"
        ? api.resumeIssueSuspension(issue.id, active.id)
        : api.cancelIssueSuspension(issue.id, active.id);
    },
    onSuccess: (result) => {
      setError(null);
      qc.setQueryData<Issue | undefined>(issueKeys.detail(wsId, issue.id), (prev) =>
        prev ? { ...prev, suspended_at: result.issue.suspended_at ?? null } : prev,
      );
      qc.invalidateQueries({ queryKey: coreIssueKeys.suspensionsAll() });
    },
    onError: (e) => setError(e instanceof Error ? e.message : String(e)),
  });

  if (!issue.suspended_at) return null;

  return (
    <View className="gap-2 rounded-lg border border-violet-500/30 bg-violet-500/10 p-3">
      <Text className="text-xs font-semibold text-violet-600 dark:text-violet-400">
        {t("suspension.banner.active_title", "Suspended")}
      </Text>
      {active ? (
        <>
          <Text className="text-xs text-muted-foreground" numberOfLines={2}>
            {t("suspension.banner.waiting_on", "Waiting on")}: {active.waiting_on_summary}
          </Text>
          {active.resume_condition ? (
            <Text className="text-xs text-muted-foreground" numberOfLines={2}>
              {t("suspension.banner.condition", "Resume condition")}: {active.resume_condition}
            </Text>
          ) : null}
          {active.reason ? (
            <Text className="text-xs text-muted-foreground" numberOfLines={2}>
              {t("suspension.banner.reason_label", "Reason")}: {active.reason}
            </Text>
          ) : null}
          <View className="flex-row gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={release.isPending}
              onPress={() => release.mutate("resume")}
            >
              <Text>{t("suspension.banner.resume", "Resume")}</Text>
            </Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={release.isPending}
              onPress={() => release.mutate("cancel")}
            >
              <Text>{t("suspension.banner.cancel", "Stop waiting")}</Text>
            </Button>
          </View>
        </>
      ) : (
        <Text className="text-xs text-muted-foreground">
          {t(
            "suspension.tooltip",
            "Waiting on an external condition — comment triggers hold until resumed",
          )}
        </Text>
      )}
      {error ? <Text className="text-xs text-destructive">{error}</Text> : null}
    </View>
  );
}

export function SuspensionChip() {
  const { t } = useT("issues");
  return (
    <View className="flex-row items-center shrink-0 rounded-full bg-violet-500/10 px-1.5 py-0.5">
      <Text className="text-[10px] text-violet-600 dark:text-violet-400">
        {t("suspension.badge", "Suspended")}
      </Text>
    </View>
  );
}
