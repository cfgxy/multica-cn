/**
 * Single row inside the agent-runs formSheet route
 * (`app/(app)/[workspace]/issue/[id]/runs.tsx`). Same component for active
 * and past tasks —
 * the trailing Cancel button is conditional on `status in {queued,
 * dispatched, running}`, and the status badge / colour swaps based on the
 * AgentTask.status enum.
 *
 * Tapping a past row is a no-op in v1 — the transcript-detail screen is
 * explicitly out of scope per /Users/qingnaiyuan/.claude/plans/
 * ok-plan-linked-taco.md.
 */
import { Alert, Pressable, View } from "react-native";
import type { AgentTask } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { useCancelTask } from "@/data/mutations/issues";
import { useActorLookup } from "@/data/use-actor-name";
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

export function RunRow({ task, issueId }: Props) {
  const { getName } = useActorLookup();
  const { t } = useT("issues");
  const isActive = ACTIVE_STATUSES.includes(task.status);
  const summary = task.trigger_summary?.trim() || fallbackSummary(task, t);
  // Past tasks use completed_at when present (server fills it for terminal
  // statuses); active tasks fall back to created_at so the user sees how
  // long it's been waiting.
  const timestamp = task.completed_at || task.created_at;

  return (
    <View className="flex-row items-start gap-3 py-2">
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
    </View>
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
  if (task.status === "failed" && task.failure_reason) {
    const reasonEn = FAILURE_REASON_LABEL[task.failure_reason];
    if (reasonEn) {
      // wire 值里的点号会被 i18next 当成嵌套层级分隔符，换成双下划线，与
      // 资源文件里的写法对齐（同 lib/failure-reason-label.ts）。
      const seg = task.failure_reason.replace(/\./g, "__");
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

// Short badge copy — deliberately terser than lib/failure-reason-label.ts,
// which backs a full-width chat bubble; this one shares a single line with the
// status word and a timestamp.
//
// Keyed by the raw wire value, not a closed enum: `failure_reason` is an open
// string that grows as classifier rules land. It held only the six
// pre-MUL-1949 coarse values until MUL-5370, so every refined `agent_error.*`
// the backend has written since fell through and the badge read just "Failed".
// An unrecognised reason still does — a compact badge is the one place where
// web's raw-wire-value fallback would overflow the row.
const FAILURE_REASON_LABEL: Record<string, string> = {
  queued_expired: "Queue expired",
  runtime_offline: "Runtime offline",
  runtime_recovery: "Runtime recovery",
  timeout: "Timeout",
  iteration_limit: "Iteration limit",
  agent_blocked: "Needs input",
  api_invalid_request: "Request rejected",
  skill_bundle_unavailable: "Skill download failed",
  runtime_cli_timeout: "Runtime CLI timeout",

  "agent_error.provider_auth_or_access": "Auth failed",
  "agent_error.provider_quota_limit": "Quota exhausted",
  "agent_error.provider_capacity_or_rate_limit": "Rate limited",
  "agent_error.provider_server_error": "Provider error",
  "agent_error.provider_network": "Network error",
  "agent_error.process_failure": "Process crashed",
  "agent_error.empty_or_unparseable_output": "No usable output",
  "agent_error.agent_timeout": "Agent timeout",
  "agent_error.context_overflow": "Context overflow",
  "agent_error.missing_config": "Config missing",
  "agent_error.model_not_found_or_unavailable": "Model unavailable",
  "agent_error.runtime_version_unsupported": "CLI unsupported",
  "agent_error.runtime_missing_executable": "CLI not installed",
  "agent_error.unknown": "Agent error",

  agent_error: "Agent error",
  codex_semantic_inactivity: "Codex inactivity",
  manual: "Manual",
};
