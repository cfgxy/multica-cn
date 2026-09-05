/**
 * Single row inside the agent-runs formSheet route
 * (`app/(app)/[workspace]/issue/[id]/runs.tsx`). Same component for active
 * and past tasks —
 * the trailing Cancel button is conditional on `status in {queued,
 * dispatched, running}`, and the status badge / colour swaps based on the
 * AgentTask.status enum.
 *
 * Tapping any non-queued row pushes the run-detail sheet (RUYI-33) —
 * the v1 no-op is gone. queued rows stay inert: the task never started, so
 * there is no transcript to show (same rule as web's picker).
 */
import { Alert, Pressable, View } from "react-native";
import { router } from "expo-router";
import { useWorkspaceStore } from "@/data/workspace-store";
import type { AgentTask } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { useCancelTask } from "@/data/mutations/issues";
import { useActorLookup } from "@/data/use-actor-name";
import { runFailureBadgeLabel } from "@/lib/run-failure-badge";
import { timeAgo } from "@/lib/time-ago";
import { useT } from "@/lib/use-t";

interface Props {
  task: AgentTask;
  issueId: string;
}

const ACTIVE_STATUSES: readonly AgentTask["status"][] = [
  "queued",
  "dispatched",
  "running",
];

/** queued has no messages yet, so it gets no detail entry (spec ②). */
const DETAIL_STATUSES: readonly AgentTask["status"][] = [
  "dispatched",
  "waiting_local_directory",
  "running",
  "completed",
  "failed",
  "cancelled",
];

export function RunRow({ task, issueId }: Props) {
  const { getName } = useActorLookup();
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { t } = useT("issues");
  const isActive = ACTIVE_STATUSES.includes(task.status);
  const summary = task.trigger_summary?.trim() || fallbackSummary(task, t);
  // Past tasks use completed_at when present (server fills it for terminal
  // statuses); active tasks fall back to created_at so the user sees how
  // long it's been waiting.
  const timestamp = task.completed_at || task.created_at;
  const detailAvailable = DETAIL_STATUSES.includes(task.status);

  return (
    <Pressable
      disabled={!detailAvailable}
      onPress={() => {
        if (!wsSlug) return;
        router.push({
          pathname: "/[workspace]/issue/[id]/runs/[taskId]",
          params: { workspace: wsSlug, id: issueId, taskId: task.id },
        });
      }}
      className={`flex-row items-start gap-3 py-2 ${
        detailAvailable ? "active:opacity-70" : ""
      }`}
    >
      <ActorAvatar type="agent" id={task.agent_id} size={28} showPresence />
      <View className="flex-1 gap-1">
        <Text
          className="text-sm text-foreground"
          numberOfLines={2}
        >
          <Text className="font-medium">{getName("agent", task.agent_id)}</Text>
          <Text className="text-muted-foreground"> · {summary}</Text>
        </Text>
        <View className="flex-row items-center gap-2">
          <StatusBadge task={task} />
          <Text className="text-xs text-muted-foreground">
            {timestamp ? timeAgo(timestamp) : ""}
          </Text>
        </View>
      </View>
      {isActive ? <CancelButton taskId={task.id} issueId={issueId} /> : null}
    </Pressable>
  );
}

function StatusBadge({ task }: { task: AgentTask }) {
  const { t } = useT("issues");
  const en = STATUS_LABEL[task.status];
  // 未知 status 原样透出（API Response Compatibility：服务端可能新增枚举
  // 值，装机版本必须能渲染而不是崩）。只有已知值才去查 i18n——拿未知值
  // 拼 key 会命中缺失分支，i18next 返回 key 本身，等于把内部串泄漏出去。
  const label = en == null ? task.status : t(`mobile.run_status.${task.status}`, en);
  const cls = STATUS_CLASS[task.status] ?? "text-muted-foreground";
  // For failed tasks, surface the failure_reason inline so users don't have
  // to drill in. Missing / empty / unrecognised stays as just "Failed".
  if (task.status === "failed") {
    // Local const so TS narrows the optional wire field for the .replace
    // below (runFailureBadgeLabel's truthy return doesn't narrow it).
    const reason = task.failure_reason;
    const reasonEn = runFailureBadgeLabel(reason);
    if (reason && reasonEn) {
      // wire 值里的点号会被 i18next 当成嵌套层级分隔符，换成双下划线，与
      // 资源文件里的写法对齐（同 lib/failure-reason-label.ts）。
      const seg = reason.replace(/\./g, "__");
      return (
        <Text className={`text-xs ${cls}`}>
          {label} · {t(`mobile.run_failure.${seg}`, reasonEn)}
        </Text>
      );
    }
  }
  return <Text className={`text-xs ${cls}`}>{label}</Text>;
}

function CancelButton({
  taskId,
  issueId,
}: {
  taskId: string;
  issueId: string;
}) {
  const { t } = useT("issues");
  const mutation = useCancelTask(issueId);

  const onPress = () => {
    // web 的 terminate_dialog 说的是「不可恢复」，mobile 说的是「跑完当前
    // 步骤后停」——语义不同，标题和正文用 mobile 专属键；两个按钮文案与 web
    // 一致，直接复用。
    Alert.alert(
      t("mobile.runs.cancel_title", "Cancel task?"),
      t(
        "mobile.runs.cancel_body",
        "The agent will stop after the current step.",
      ),
      [
        {
          text: t("terminate_dialog.keep", "Keep running"),
          style: "cancel",
        },
        {
          text: t("execution_log.cancel_task_tooltip", "Cancel task"),
          style: "destructive",
          onPress: () => mutation.mutate(taskId),
        },
      ],
    );
  };

  return (
    <Pressable
      onPress={onPress}
      disabled={mutation.isPending}
      className="px-3 py-1.5 rounded-md bg-secondary active:opacity-70"
    >
      <Text className="text-xs font-medium text-foreground">
        {t("common:cancel", "Cancel")}
      </Text>
    </Pressable>
  );
}

// 本文件是组件文件，按 mobile 的接线范式走 useT——`t` 由调用方（RunRow）
// 从 hook 取好后传进来，这个纯函数本身不持有 i18n 状态，仍可单独测试。
type TFn = ReturnType<typeof useT>["t"];

function fallbackSummary(task: AgentTask, t: TFn): string {
  switch (task.kind) {
    case "comment":
      return t("mobile.run_summary.comment", "Comment task");
    case "autopilot":
      return t("mobile.run_summary.autopilot", "Autopilot run");
    case "chat":
      return t("mobile.run_summary.chat", "Chat task");
    case "quick_create":
      return t("mobile.run_summary.quick_create", "Quick create");
    case "direct":
    default:
      return t("mobile.run_summary.direct", "Task");
  }
}

const STATUS_LABEL: Record<AgentTask["status"], string> = {
  queued: "Queued",
  dispatched: "Starting",
  waiting_local_directory: "Waiting for directory",
  running: "Running",
  completed: "Done",
  failed: "Failed",
  cancelled: "Cancelled",
};

const STATUS_CLASS: Record<AgentTask["status"], string> = {
  queued: "text-muted-foreground",
  dispatched: "text-brand",
  waiting_local_directory: "text-muted-foreground",
  running: "text-brand",
  completed: "text-muted-foreground",
  failed: "text-destructive",
  cancelled: "text-muted-foreground",
};
