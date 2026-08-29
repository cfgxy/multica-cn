/**
 * Single agent-run detail (RUYI-33) — the mobile counterpart of web's
 * `AgentTranscriptDialog`, pushed as a stacked sheet from the runs list.
 *
 * Data: the `task-messages` endpoint (same cache the chat trace uses) for
 * the transcript, and the issue tasks cache (already loaded by the runs
 * sheet) for the AgentTask header fields. Live runs keep growing:
 * `task:message` WS frames append into the same cache via
 * `appendTaskMessage`, so an open detail updates without refetching —
 * that is the spec's enhancement item, layered on top of the mandatory
 * manual pull-to-refresh.
 *
 * Redaction: every rendered or copied string is produced by
 * `lib/run-detail.ts` (redactSecrets at every exit) — the component never
 * touches raw transcript fields.
 */
import { useMemo, useState } from "react";
import { RefreshControl, ScrollView, View } from "react-native";
import * as Clipboard from "expo-clipboard";
import { useLocalSearchParams } from "expo-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { AgentTask } from "@multica/core/types";
import { ModalCloseButton } from "@/components/ui/modal-close-button";
import { Text } from "@/components/ui/text";
import { RunDetailTimeline } from "@/components/issue/run-detail-timeline";
import { appendTaskMessage } from "@/data/realtime/chat-ws-updaters";
import { taskMessagesOptions } from "@/data/queries/chat";
import { issueTasksOptions } from "@/data/queries/issues";
import { useWSSubscriptions } from "@/lib/use-ws-subscriptions";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useActorLookup } from "@/data/use-actor-name";
import { useT } from "@/lib/use-t";
import {
  buildRunStepViews,
  cancelReasonLabel,
  formatRunDuration,
  runCopyAllText,
  runDurationMs,
  runOutcomeSummary,
  runStatusLabel,
} from "@/lib/run-detail";
import {
  failureReasonLabel,
  isKnownFailureReason,
} from "@/lib/failure-reason-label";

export default function RunDetailRoute() {
  const { id: issueId, taskId } = useLocalSearchParams<{
    id: string;
    taskId: string;
  }>();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const qc = useQueryClient();
  const { t } = useT("issues");

  // Transcript — workspace-agnostic cache keyed on taskId only.
  const { data: messages, isLoading, refetch, isRefetching } = useQuery(
    taskMessagesOptions(taskId),
  );

  // Header fields come from the issue tasks cache the runs sheet loaded.
  const { data: tasks = [] } = useQuery(issueTasksOptions(wsId, issueId));
  const task = useMemo(
    () => tasks.find((tk) => tk.id === taskId),
    [tasks, taskId],
  );

  // Enhancement: live append while the detail is open. Server publishes
  // `task:message` for issue tasks too (payload carries task_id); gate
  // per-record so background traffic never touches this cache.
  useWSSubscriptions(
    (ws) => [
      ws.on("task:message", (payload) => {
        if (payload.task_id !== taskId) return;
        appendTaskMessage(qc, payload);
      }),
    ],
    [taskId, qc],
  );

  const items = useMemo(() => messages ?? [], [messages]);
  const views = useMemo(() => buildRunStepViews(items), [items]);
  const outcome = useMemo(() => runOutcomeSummary(items), [items]);
  const durationMs = task ? runDurationMs(task) : null;
  const agentLabel = useAgentName(task);

  const [copiedAll, setCopiedAll] = useState(false);
  const copyAll = () => {
    void Clipboard.setStringAsync(runCopyAllText(items));
    setCopiedAll(true);
  };

  return (
    <View className="flex-1">
      <View className="flex-row items-center gap-2 px-4 pt-4 pb-2">
        <Text className="flex-1 text-base font-semibold text-foreground" numberOfLines={1}>
          {t("mobile.run_detail.title", "Run details")}
        </Text>
        {items.length > 0 ? (
          <Text
            accessibilityRole="button"
            onPress={copyAll}
            className="text-sm font-medium text-brand active:opacity-70"
          >
            {copiedAll
              ? t("mobile.run_detail.copied", "Copied")
              : t("mobile.run_detail.copy_all", "Copy all")}
          </Text>
        ) : null}
        <ModalCloseButton />
      </View>
      <ScrollView
        showsVerticalScrollIndicator={false}
        refreshControl={
          <RefreshControl refreshing={isRefetching} onRefresh={() => void refetch()} />
        }
      >
        <View className="px-4 gap-3 pb-6">
          {task ? <RunHeader task={task} agentLabel={agentLabel} durationMs={durationMs} /> : null}
          {task && (task.status === "failed" || task.status === "cancelled") ? (
            <FailurePanel task={task} />
          ) : null}
          {outcome ? <OutcomeSummary outcome={outcome} /> : null}
          {isLoading ? (
            <Text className="text-sm text-muted-foreground">
              {t("mobile.run_detail.loading", "Loading…")}
            </Text>
          ) : items.length === 0 ? (
            <EmptyState queued={task?.status === "queued"} />
          ) : (
            <View className="gap-1">
              <Text className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                {t("mobile.run_detail.timeline_heading", "Timeline")}
              </Text>
              <RunDetailTimeline views={views} />
            </View>
          )}
        </View>
      </ScrollView>
    </View>
  );
}

function useAgentName(task: AgentTask | undefined): string | null {
  const { getName } = useActorLookup();
  if (!task) return null;
  return getName("agent", task.agent_id);
}

function RunHeader({
  task,
  agentLabel,
  durationMs,
}: {
  task: AgentTask;
  agentLabel: string | null;
  durationMs: number | null;
}) {
  const { t } = useT("issues");
  const cancelReason = cancelReasonLabel(task);
  return (
    <View className="gap-1">
      <View className="flex-row items-center gap-2">
        <StatusWord task={task} />
        {agentLabel ? (
          <Text className="text-sm font-medium text-foreground" numberOfLines={1}>
            {agentLabel}
          </Text>
        ) : null}
      </View>
      <View className="flex-row flex-wrap items-center gap-x-2 gap-y-0.5">
        {durationMs != null ? (
          <Text className="text-xs text-muted-foreground">
            {t("mobile.run_detail.took", {
              duration: formatRunDuration(durationMs),
            })}
          </Text>
        ) : null}
        {task.started_at ? (
          <Text className="text-xs text-muted-foreground">
            {t("mobile.run_detail.started_at", "Started")}{" "}
            {new Date(task.started_at).toLocaleString()}
          </Text>
        ) : null}
        {task.completed_at ? (
          <Text className="text-xs text-muted-foreground">
            {t("mobile.run_detail.completed_at", "Finished")}{" "}
            {new Date(task.completed_at).toLocaleString()}
          </Text>
        ) : null}
      </View>
      {cancelReason ? (
        <Text className="text-xs text-muted-foreground">{cancelReason}</Text>
      ) : null}
    </View>
  );
}

function StatusWord({ task }: { task: AgentTask }) {
  const label = runStatusLabel(task.status);
  const tone = STATUS_TONE[task.status] ?? "text-muted-foreground";
  return <Text className={`text-sm font-semibold ${tone}`}>{label}</Text>;
}

const STATUS_TONE: Record<AgentTask["status"], string> = {
  queued: "text-muted-foreground",
  dispatched: "text-brand",
  waiting_local_directory: "text-muted-foreground",
  running: "text-brand",
  completed: "text-muted-foreground",
  failed: "text-destructive",
  cancelled: "text-muted-foreground",
};

function FailurePanel({ task }: { task: AgentTask }) {
  const { t } = useT("issues");
  const failed = task.status === "failed";
  // Failed runs carry the classification inline (badge equivalent); cancelled
  // runs already show their reason in the header, the panel holds the
  // persisted error text.
  const label =
    failed && isKnownFailureReason(task.failure_reason)
      ? failureReasonLabel(task.failure_reason)
      : null;
  const hasError = !!task.error;
  if (!label && !hasError) return null;
  return (
    <View className="rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 gap-1">
      <Text className="text-xs font-semibold uppercase tracking-wide text-destructive">
        {failed
          ? t("mobile.run_detail.failure_heading", "Failure reason")
          : t("mobile.run_detail.cancel_reason_heading", "Cancellation reason")}
      </Text>
      {label ? <Text className="text-xs font-medium text-destructive">{label}</Text> : null}
      {hasError ? (
        <Text className="text-xs text-destructive" selectable>
          {task.error}
        </Text>
      ) : null}
    </View>
  );
}

function OutcomeSummary({
  outcome,
}: {
  outcome: { paths: string[]; addedLines: number; removedLines: number; commandCount: number };
}) {
  const { t } = useT("issues");
  const parts: string[] = [];
  if (outcome.paths.length > 0) {
    parts.push(
      t("mobile.run_detail.outcome_files", { count: outcome.paths.length }),
    );
  }
  if (outcome.addedLines > 0 || outcome.removedLines > 0) {
    parts.push(`+${outcome.addedLines} / −${outcome.removedLines}`);
  }
  if (outcome.commandCount > 0) {
    parts.push(
      t("mobile.run_detail.outcome_commands", { count: outcome.commandCount }),
    );
  }
  if (parts.length === 0) return null;
  return (
    <View className="flex-row flex-wrap gap-1.5">
      {parts.map((part) => (
        <View
          key={part}
          className="rounded-full bg-secondary px-2.5 py-1"
        >
          <Text className="text-xs tabular-nums text-secondary-foreground">{part}</Text>
        </View>
      ))}
    </View>
  );
}

function EmptyState({ queued }: { queued: boolean }) {
  const { t } = useT("issues");
  return (
    <View className="items-center gap-1 py-10">
      <Text className="text-sm text-muted-foreground">
        {queued
          ? t("mobile.run_detail.empty_queued", "This run hasn't started yet.")
          : t("mobile.run_detail.empty", "No events were recorded for this run.")}
      </Text>
    </View>
  );
}
