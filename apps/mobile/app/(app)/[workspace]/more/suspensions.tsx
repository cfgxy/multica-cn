/**
 * 挂起中心 (RUYI-690) — workspace suspension aggregation, the mobile parity
 * of web's /suspensions page, reached from the More dropdown (phones have no
 * sidebar).
 *
 * One row per suspension; status pills switch the server window (挂起中 is
 * the default view, 全部 is the audit window — same four windows as web's
 * SuspensionCenterPage). Row line 1 = issue identifier + title, line 2 =
 * wait target · resume condition (the server's workspace aggregate carries
 * the issue display fields, so no per-row detail fetch). Active rows carry
 * one-tap 恢复 / 取消等待: the response's refreshed issue snapshot patches
 * the mobile issue-detail cache in place and the suspensions subtree
 * invalidates, so the current window rehydrates from its own query.
 *
 * Buttons sit OUTSIDE the row Pressable (RN nested-touchable rule) — the
 * pressable area is the text block, the actions render underneath it.
 */
import { useState } from "react";
import {
  ActivityIndicator,
  Pressable,
  RefreshControl,
  ScrollView,
  View,
} from "react-native";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { router } from "expo-router";
import { issueKeys as coreIssueKeys } from "@multica/core/issues/queries";
import type { Issue, WorkspaceIssueSuspension } from "@multica/core/types";
import { Button } from "@/components/ui/button";
import { Text } from "@/components/ui/text";
import { api } from "@/data/api";
import { issueKeys } from "@/data/queries/issue-keys";
import { workspaceSuspensionsOptions } from "@/data/queries/suspensions";
import { useWorkspaceStore } from "@/data/workspace-store";
import { cn } from "@/lib/utils";
import { useT } from "@/lib/use-t";

const STATUS_FILTERS = ["active", "resumed", "cancelled", "all"] as const;
type StatusFilter = (typeof STATUS_FILTERS)[number];

export default function SuspensionsPage() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { t } = useT("suspensions");
  const [status, setStatus] = useState<StatusFilter>("active");

  const { data, isLoading, error, refetch, isRefetching } = useQuery(
    workspaceSuspensionsOptions(wsId ?? null, status),
  );
  const rows = data?.suspensions ?? [];

  if (isLoading) {
    return (
      <View className="flex-1 items-center justify-center bg-background">
        <ActivityIndicator />
      </View>
    );
  }

  if (error) {
    return (
      <View className="flex-1 bg-background px-4 gap-3 pt-4">
        <Text className="text-sm text-destructive">
          {t("page.load_failed", "Failed to load suspensions")}
        </Text>
        <Button variant="outline" onPress={() => refetch()}>
          <Text>{t("common:mobile.common.retry", "Retry")}</Text>
        </Button>
      </View>
    );
  }

  return (
    <ScrollView
      className="flex-1 bg-background"
      contentContainerClassName="pb-6"
      refreshControl={
        <RefreshControl refreshing={isRefetching} onRefresh={() => refetch()} />
      }
      showsVerticalScrollIndicator={false}
    >
      <View className="flex-row flex-wrap gap-2 px-4 py-3">
        {STATUS_FILTERS.map((f) => (
          <Pressable
            key={f}
            onPress={() => setStatus(f)}
            className={cn(
              "rounded-full px-3 py-1",
              status === f ? "bg-primary" : "bg-secondary",
            )}
          >
            <Text
              className={cn(
                "text-xs",
                status === f ? "text-primary-foreground" : "text-foreground",
              )}
            >
              {t(`filter.${f}`, f)}
            </Text>
          </Pressable>
        ))}
      </View>

      {rows.length === 0 ? (
        <View className="items-center justify-center gap-2 px-6 py-16">
          <Text className="text-sm text-muted-foreground text-center">
            {t("page.empty_title", "No suspended issues")}
          </Text>
          <Text className="text-xs text-muted-foreground text-center">
            {t(
              "page.empty_description",
              "Issues suspended with a waiting reason will be aggregated here, so paused work stays visible until it resumes.",
            )}
          </Text>
        </View>
      ) : (
        rows.map((s) => <SuspensionRow key={s.id} s={s} wsSlug={wsSlug} />)
      )}
    </ScrollView>
  );
}

function SuspensionRow({
  s,
  wsSlug,
}: {
  s: WorkspaceIssueSuspension;
  wsSlug: string | null;
}) {
  const { t } = useT("suspensions");
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((st) => st.currentWorkspaceId);
  const [error, setError] = useState<string | null>(null);

  const release = useMutation({
    mutationFn: async (mode: "resume" | "cancel") =>
      mode === "resume"
        ? api.resumeIssueSuspension(s.issue_id, s.id)
        : api.cancelIssueSuspension(s.issue_id, s.id),
    onSuccess: (result) => {
      setError(null);
      qc.setQueryData<Issue | undefined>(
        issueKeys.detail(wsId, s.issue_id),
        (prev) =>
          prev ? { ...prev, suspended_at: result.issue.suspended_at ?? null } : prev,
      );
      qc.invalidateQueries({ queryKey: coreIssueKeys.suspensionsAll() });
    },
    onError: (e) => setError(e instanceof Error ? e.message : String(e)),
  });

  return (
    <View className="border-b border-border">
      <Pressable
        onPress={() =>
          wsSlug &&
          router.push({
            pathname: "/[workspace]/issue/[id]",
            params: { workspace: wsSlug, id: s.issue_id },
          })
        }
        className="gap-1 px-4 py-3 active:bg-secondary"
      >
        <View className="flex-row items-center gap-2">
          <Text className="w-16 shrink-0 text-xs text-muted-foreground">
            {s.issue_identifier}
          </Text>
          <Text
            className="shrink text-sm text-foreground"
            numberOfLines={1}
          >
            {s.issue_title}
          </Text>
        </View>
        <Text className="text-xs text-muted-foreground" numberOfLines={2}>
          {t("row.waiting_on", "Waiting on")}: {s.waiting_on_summary}
          {s.resume_condition
            ? ` · ${t("row.condition", "Resume condition")}: ${s.resume_condition}`
            : ""}
        </Text>
      </Pressable>
      {s.status === "active" ? (
        <View className="flex-row gap-2 px-4 pb-3">
          <Button
            variant="outline"
            size="sm"
            disabled={release.isPending}
            onPress={() => release.mutate("resume")}
          >
            <Text>{t("row.resume", "Resume")}</Text>
          </Button>
          <Button
            variant="ghost"
            size="sm"
            disabled={release.isPending}
            onPress={() => release.mutate("cancel")}
          >
            <Text>{t("row.cancel", "Stop waiting")}</Text>
          </Button>
        </View>
      ) : null}
      {error ? (
        <Text className="px-4 pb-3 text-xs text-destructive">{error}</Text>
      ) : null}
    </View>
  );
}
