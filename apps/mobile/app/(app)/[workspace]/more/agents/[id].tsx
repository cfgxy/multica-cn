/**
 * Agent detail (`more/agents/[id]`, RUYI-76 ②) — upgraded in place by
 * RUYI-346 into the design's A2 three-tab screen: 概览 / 活跃 / 设置.
 *
 *   - 概览   profile card (avatar / name / description / access badge /
 *           archived banner) + instructions preview + DM / Assign Work +
 *           the explicit Edit Profile row + key facts (runtime + online
 *           status, model, concurrency cap, thinking, owner, presence).
 *           RUYI-624: the three editor entries are distinct — Edit Profile
 *           row → edit-profile (profile fields), instructions preview →
 *           the dedicated edit-instructions window, the facts card's
 *           execution rows → run-config (web's execution section). Before
 *           the split every entry routed to edit-profile, so runtime /
 *           model / concurrency had no direct mobile editor and the
 *           half-height profile sheet read as an instructions-only box.
 *   - 活跃   two sections (RUYI-538): 进行中 — the per-agent active-run
 *           list (`selectAgentActiveTasks`) with a per-row left-swipe
 *           cancel (the inbox archive gesture; fires THIS row's task id
 *           only, replacing the batch cancel-tasks button) — and 运行历史
 *           — the paginated terminal-run history (`selectAgentRunHistory`,
 *           initial 10 + 20 per tap) with failed-run retry and the same
 *           status badge as the issue runs sheet.
 *   - 设置   grouped navigation rows into the skills / access / custom args /
 *           env / webhooks sub-screens plus the capability rows with the
 *           same visibility conditions as web's agent overview pane
 *           (RUYI-418 B3): MCP (`providerSupportsMcpConfig` or unbound),
 *           MCP Apps (composio feature flag + owner), Integrations (any of
 *           the five IM listings reports `configured`), Routing (openclaw
 *           runtimes only). Env stays `canManage`-gated like web — it is
 *           the only row backed by a secret-bearing endpoint.
 *
 * Data: `agentDetailOptions` is the primary source (deep-link safe and the
 * cache mutations keep it warm); presence / snapshot / issues / members /
 * runtimes are the shared workspace queries the prefetch warms, with the
 * agents list as the paint-from-push fallback while the detail fetch is in
 * flight. Archive and restore live in the header more-menu (system_key
 * agents hide the menu — they cannot be archived, mirroring web's
 * agent-row-actions); an archived agent renders read-only behind a restore
 * banner. A 403 from the detail fetch renders the forbidden state (S3) —
 * distinct copy from the not-found state, same layout.
 */
import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, SectionList, View } from "react-native";
import { useQueries, useQuery } from "@tanstack/react-query";
import * as Haptics from "expo-haptics";
import { Ionicons } from "@expo/vector-icons";
import { Stack, router, useLocalSearchParams } from "expo-router";
import { providerSupportsMcpConfig } from "@multica/core/agents/mcp-support";
import { canAssignAgentToIssue } from "@multica/core/permissions";
import { COMPOSIO_MCP_APPS_FLAG } from "@multica/core/feature-flags";
import type { Agent, AgentTask } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { InstructionsPreview } from "@/components/ui/instructions-preview";
import { ActionSheetModal, useActionSheet } from "@/components/ui/action-sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { AgentPresenceLine } from "@/components/agents/agent-presence-line";
import { SwipeableAgentTaskRow } from "@/components/agents/swipeable-agent-task-row";
import { AgentRunHistoryRow } from "@/components/agents/agent-run-history-row";
import { AccessScopeBadge } from "@/components/agents/access-scope-badge";
import { ApiError } from "@/data/api";
import { agentDetailOptions, agentListOptions } from "@/data/queries/agents";
import { appConfigOptions } from "@/data/queries/billing";
import {
  dingTalkInstallationsOptions,
  larkInstallationsOptions,
  slackInstallationsOptions,
  telegramInstallationsOptions,
  wecomInstallationsOptions,
} from "@/data/queries/integrations";
import { issueDetailOptions } from "@/data/queries/issues";
import { agentTaskSnapshotOptions } from "@/data/queries/agent-task-snapshot";
import { agentTasksOptions } from "@/data/queries/agent-tasks";
import { runtimeListOptions } from "@/data/queries/runtimes";
import { runtimeModelsOptions } from "@/data/queries/runtime-models";
import { resolveThinkingLevels } from "@/lib/model-capability";
import { memberListOptions } from "@/data/queries/members";
import {
  useArchiveAgent,
  useCancelAgentTask,
  useRestoreAgent,
} from "@/data/mutations/agents";
import {
  canCancelAgentTask,
  selectAgentActiveTasks,
} from "@/lib/issue-agent-activity";
import {
  RUN_HISTORY_INITIAL,
  RUN_HISTORY_PAGE,
  selectAgentRunHistory,
} from "@/lib/agent-run-history";
import { useWorkspacePresenceMap } from "@/lib/use-agent-presence";
import { resolveAttachmentUrl } from "@/lib/attachment-url";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useChatAgentRequestStore } from "@/data/stores/chat-agent-request-store";
import { useNewIssueDraftStore } from "@/data/stores/new-issue-draft-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

type DetailTab = "overview" | "active" | "settings";

/** Sentinel row so the 运行历史 section header can carry an empty-state
 *  line without a second FlatList. Discriminated by the historyEmpty flag. */
interface HistoryEmptyRow {
  id: "__history_empty__";
  historyEmpty: true;
}
const HISTORY_EMPTY: HistoryEmptyRow = {
  id: "__history_empty__",
  historyEmpty: true,
};

export default function AgentDetailPage() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const agentId = typeof id === "string" ? id : "";
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const me = useAuthStore((s) => s.user);
  const { colorScheme } = useColorScheme();
  const { t } = useT("agents");
  const actionSheet = useActionSheet();
  const [tab, setTab] = useState<DetailTab>("overview");

  const archiveAgent = useArchiveAgent();
  const restoreAgent = useRestoreAgent();
  const cancelTask = useCancelAgentTask();

  const {
    data: fetched,
    isLoading,
    isError,
    error,
  } = useQuery(agentDetailOptions(wsId, agentId));
  // `id: ""` is the schema fallback sentinel — treat it as "not found".
  const detailAgent: Agent | null = fetched && fetched.id ? fetched : null;
  // S3: a 403 (no permission to see this agent) gets its own copy —
  // distinct from the not-found fallback below.
  const isForbidden = error instanceof ApiError && error.status === 403;

  // List cache as paint-from-push fallback: navigating from the list has the
  // row warm before the detail fetch resolves; detail wins once present.
  const { data: listAgents } = useQuery(agentListOptions(wsId));
  const agent = useMemo(
    () => detailAgent ?? listAgents?.find((a) => a.id === agentId) ?? null,
    [detailAgent, listAgents, agentId],
  );

  const { byAgent } = useWorkspacePresenceMap(wsId);
  const presence = agent ? byAgent.get(agent.id) : undefined;

  const { data: snapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));
  const activeTasks = useMemo(
    () => (agent ? selectAgentActiveTasks(snapshot, agent.id) : []),
    [snapshot, agent],
  );

  // Full per-agent task list (RUYI-538 ②) — the terminal side feeds the run
  // history section; the active side stays on the presence snapshot above so
  // the tab badge paints before this fetch resolves.
  const { data: agentTaskList = [] } = useQuery(
    agentTasksOptions(wsId, agentId),
  );
  const runHistory = useMemo(
    () => (agent ? selectAgentRunHistory(agentTaskList) : []),
    [agentTaskList, agent],
  );

  // Client-side pagination over the server's full terminal list (web
  // activity-tab parity: initial window, then +PAGE per tap — the endpoint
  // has no cursor). Reset when the screen rebinds to another agent.
  const [historyLimit, setHistoryLimit] = useState(RUN_HISTORY_INITIAL);
  useEffect(() => {
    setHistoryLimit(RUN_HISTORY_INITIAL);
  }, [agentId]);
  const visibleHistory = useMemo(
    () => runHistory.slice(0, historyLimit),
    [runHistory, historyLimit],
  );
  const hasMoreHistory = runHistory.length > visibleHistory.length;

  // Title lookup, per-issue detail queries — an issue beyond the 100-row
  // list window still resolves here (RUYI-538 ④ root cause: the old
  // issueListOptions lookup fell back to 「任务不可见」 exactly there). An
  // error must NOT blank the rows; unresolved titles fall back to the
  // issue's short id, mirroring web's activity tab.
  const issueIds = useMemo(() => {
    const ids = new Set<string>();
    for (const task of activeTasks) if (task.issue_id) ids.add(task.issue_id);
    for (const task of visibleHistory) if (task.issue_id) ids.add(task.issue_id);
    return [...ids];
  }, [activeTasks, visibleHistory]);
  const issueTitleQueries = useQueries({
    queries: issueIds.map((issueId) => issueDetailOptions(wsId, issueId)),
  });
  const titleById = useMemo(() => {
    const map = new Map<string, string>();
    issueIds.forEach((issueId, i) => {
      const title = issueTitleQueries[i]?.data?.title;
      if (title) map.set(issueId, title);
    });
    return map;
  }, [issueIds, issueTitleQueries]);

  const { data: members } = useQuery(memberListOptions(wsId));
  const isWorkspaceAdmin = useMemo(() => {
    if (!me) return false;
    const mine = members?.find((m) => m.user_id === me.id);
    return mine?.role === "owner" || mine?.role === "admin";
  }, [members, me]);
  const owner = useMemo(
    () =>
      agent?.owner_id
        ? members?.find((m) => m.user_id === agent.owner_id) ?? null
        : null,
    [members, agent],
  );

  const { data: runtimes } = useQuery(runtimeListOptions(wsId));
  const runtime = useMemo(
    () =>
      agent?.runtime_id
        ? runtimes?.find((r) => r.id === agent.runtime_id) ?? null
        : null,
    [runtimes, agent],
  );

  // RUYI-538 ①: thinking-effort display vocabulary from the same daemon
  // catalog the editor uses (labels are per-CLI, never normalised). Only
  // resolved for an online runtime; shown in the facts card when the model
  // exposes levels or a value is persisted (web ThinkingPropRow visibility).
  const thinkingModelsQuery = useQuery(
    runtimeModelsOptions(
      runtime?.status === "online" ? (agent?.runtime_id ?? null) : null,
    ),
  );
  const thinkingLevels = useMemo(
    () =>
      resolveThinkingLevels(
        thinkingModelsQuery.data?.models,
        agent?.model ?? "",
        runtime?.provider ?? "",
      ),
    [thinkingModelsQuery.data, agent?.model, runtime?.provider],
  );

  // Capability-row visibility — same predicates as web agent-overview-pane:
  // composio needs the server feature flag + viewer-is-owner; integrations
  // need any of the five IM listings to report `configured`.
  const { data: appConfig } = useQuery(appConfigOptions());
  const composioMcpEnabled =
    appConfig?.feature_flags?.[COMPOSIO_MCP_APPS_FLAG] === true;
  const { data: larkListing } = useQuery(larkInstallationsOptions(wsId));
  const { data: slackListing } = useQuery(slackInstallationsOptions(wsId));
  const { data: dingtalkListing } = useQuery(dingTalkInstallationsOptions(wsId));
  const { data: wecomListing } = useQuery(wecomInstallationsOptions(wsId));
  const { data: telegramListing } = useQuery(telegramInstallationsOptions(wsId));
  const integrationsConfigured =
    larkListing?.configured === true ||
    slackListing?.configured === true ||
    dingtalkListing?.configured === true ||
    wecomListing?.configured === true ||
    telegramListing?.configured === true;
  const isOwner = !!agent && !!me && agent.owner_id === me.id;
  const showMcpRow = runtime
    ? providerSupportsMcpConfig(runtime.provider)
    : true;
  const showComposioRow = composioMcpEnabled && isOwner;
  const showRoutingRow = runtime?.provider === "openclaw";

  const canManage =
    !!agent && (isWorkspaceAdmin || (!!me && agent.owner_id === me.id));
  const isArchived = !!agent?.archived_at;
  const isSystem = !!agent?.system_key;

  // A7 DM / Assign Work gate — web shares one invocation gate for both
  // (MUL-3963, canAssignAgentToIssue). While membership is resolving the
  // decision is undetermined, so DM stays disabled rather than alerting a
  // false "no access" at a real member.
  const myRole = useMemo(
    () =>
      me
        ? members?.find((m) => m.user_id === me.id)?.role ?? null
        : null,
    [members, me],
  );
  const permissionsReady = !!me && !!members;
  const canInvoke = useMemo(
    () =>
      !!agent &&
      canAssignAgentToIssue(agent, {
        userId: me?.id ?? null,
        role: myRole,
      }).allowed,
    [agent, me, myRole],
  );

  // A7: DM hands off to the chat tab through the one-shot request store;
  // Assign Work seeds the quick-create smart actor and opens new-issue.
  const requestAgent = useChatAgentRequestStore((s) => s.requestAgent);
  const setSmartActor = useNewIssueDraftStore((s) => s.setSmartActor);
  const handleDm = useCallback(() => {
    if (!agent || !wsSlug) return;
    if (!canInvoke) {
      Alert.alert(
        t(
          "detail.dm_no_permission_toast",
          "You don't have access to chat with this agent.",
        ),
      );
      return;
    }
    if (!runtime) {
      Alert.alert(
        t(
          "detail.runtime_required_toast",
          "Bind a runtime before running this agent.",
        ),
      );
      return;
    }
    requestAgent(agent.id);
    router.push(`/${wsSlug}/chat/new`);
  }, [agent, wsSlug, canInvoke, runtime, requestAgent, t]);

  const handleAssign = useCallback(() => {
    if (!agent || !wsSlug) return;
    if (!runtime) {
      Alert.alert(
        t(
          "detail.runtime_required_toast",
          "Bind a runtime before running this agent.",
        ),
      );
      return;
    }
    setSmartActor({ type: "agent", id: agent.id });
    router.push(`/${wsSlug}/new-issue`);
  }, [agent, wsSlug, runtime, setSmartActor, t]);

  const confirmArchive = useCallback(() => {
    if (!agent) return;
    Alert.alert(
      t("detail.archive_dialog_title", "Archive agent?"),
      t("detail.archive_dialog_description", "\"{{name}}\" will be archived…", {
        name: agent.name,
      }),
      [
        { text: t("detail.archive_dialog_cancel", "Cancel"), style: "cancel" },
        {
          text: t("detail.archive_dialog_confirm", "Archive"),
          style: "destructive",
          onPress: () => {
            archiveAgent.mutate(agent.id, {
              onSuccess: () => {
                Haptics.notificationAsync(
                  Haptics.NotificationFeedbackType.Success,
                ).catch(() => {});
              },
            });
          },
        },
      ],
    );
  }, [agent, archiveAgent, t]);

  const restore = useCallback(() => {
    if (!agent) return;
    restoreAgent.mutate(agent.id, {
      onSuccess: () => {
        Haptics.notificationAsync(
          Haptics.NotificationFeedbackType.Success,
        ).catch(() => {});
      },
    });
  }, [agent, restoreAgent]);

  const showMoreMenu = useCallback(() => {
    if (!agent || isSystem) return;
    const options = isArchived
      ? [
          t("detail.restore", "Restore"),
          t("detail.archive_dialog_cancel", "Cancel"),
        ]
      : [
          t("detail.more_archive", "Archive Agent"),
          t("detail.archive_dialog_cancel", "Cancel"),
        ];
    actionSheet.show({
      options,
      cancelButtonIndex: options.length - 1,
      title: agent.name,
      onSelect: (index) => {
        if (isArchived && index === 0) restore();
        if (!isArchived && index === 0) confirmArchive();
      },
    });
  }, [agent, isArchived, isSystem, actionSheet, confirmArchive, restore, t]);

  const headerRight = useCallback(() => {
    if (!agent || isSystem || !canManage) return null;
    return (
      <Pressable onPress={showMoreMenu} className="px-3 py-1" hitSlop={8}>
        <Ionicons
          name="ellipsis-horizontal"
          size={22}
          color={THEME[colorScheme].foreground}
        />
      </Pressable>
    );
  }, [agent, isSystem, canManage, showMoreMenu, colorScheme]);

  // RUYI-624 split: each overview entry opens its own editor. Edit Profile
  // row → edit-profile (avatar / name / description / voice); instructions
  // block → the dedicated edit-instructions window (RUYI-541 pattern);
  // facts-card execution rows (runtime / model / thinking / concurrency) →
  // run-config, web's execution-section mirror. Before the split every
  // entry routed to edit-profile, whose half-height sheet opens on the
  // instructions textarea — owners read it as "every entry opens the same
  // instruction box" and runtime/model/concurrency had no direct editor.
  const openEditProfile = useCallback(() => {
    if (!agent || !wsSlug) return;
    router.push({
      pathname: "/[workspace]/more/agents/[id]/edit-profile",
      params: { workspace: wsSlug, id: agent.id },
    });
  }, [agent, wsSlug]);

  const openRunConfig = useCallback(() => {
    if (!agent || !wsSlug) return;
    router.push({
      pathname: "/[workspace]/more/agents/[id]/run-config",
      params: { workspace: wsSlug, id: agent.id },
    });
  }, [agent, wsSlug]);

  const openInstructions = useCallback(() => {
    if (!agent || !wsSlug) return;
    router.push({
      pathname: "/[workspace]/more/agents/[id]/edit-instructions",
      params: { workspace: wsSlug, id: agent.id },
    });
  }, [agent, wsSlug]);

  if (isLoading) {
    return (
      <View className="flex-1 items-center justify-center bg-background">
        <Text className="text-sm text-muted-foreground">
          {t("page.list_loading", "Loading agents…")}
        </Text>
      </View>
    );
  }

  if (!agent || isError) {
    return (
      <View className="flex-1 items-center justify-center bg-background px-8 gap-3">
        <Ionicons
          name={isForbidden ? "lock-closed-outline" : "help-circle-outline"}
          size={42}
          color={THEME[colorScheme].mutedForeground}
        />
        <Text className="text-base font-medium text-foreground text-center">
          {isForbidden
            ? t("mobile.detail.forbidden_title", "No access to this agent")
            : t("detail.not_found_title", "Agent not found")}
        </Text>
        <Text className="text-sm text-muted-foreground text-center">
          {isForbidden
            ? t(
                "mobile.detail.forbidden_body",
                "You don't have permission to view this agent. Ask its owner to share it with you.",
              )
            : t(
                "detail.not_found_default",
                "This agent may have been archived or deleted.",
              )}
        </Text>
      </View>
    );
  }

  const a = agent;
  const factsEditable = canManage && !isArchived;

  // Thinking row mirrors web's ThinkingPropRow visibility: render only when
  // the model exposes levels or a value is persisted. Unknown-but-set
  // values render the raw token so the operator sees what's saved (same
  // rule as the editor picker).
  const showThinkingRow = thinkingLevels.length > 0 || !!a.thinking_level;
  const thinkingEntry =
    a.thinking_level
      ? (thinkingLevels.find((l) => l.value === a.thinking_level) ?? null)
      : null;
  const thinkingLabel = a.thinking_level
    ? (thinkingEntry?.label ?? a.thinking_level)
    : t("pickers.thinking_default", "Follow CLI config");

  const factRows: {
    label: string;
    value: React.ReactNode;
    /** RUYI-624: execution rows tap through to the run-config editor. */
    editable?: boolean;
  }[] = [
    {
      label: t("inspector.prop_runtime", "Runtime"),
      editable: true,
      value: runtime ? (
        <Text className="text-sm text-foreground" numberOfLines={1}>
          {runtime.custom_name || runtime.name}
          <Text className="text-muted-foreground">
            {" · "}
            {runtime.status === "online"
              ? t("availability.online", "Online")
              : t("availability.offline", "Offline")}
          </Text>
        </Text>
      ) : (
        <Text className="text-sm text-muted-foreground">
          {t("mobile.detail.fact_runtime_unbound", "No runtime")}
        </Text>
      ),
    },
    {
      label: t("inspector.prop_model", "Model"),
      editable: true,
      value: (
        <Text className="text-sm text-foreground" numberOfLines={1}>
          {a.model || t("pickers.model_default", "Default")}
        </Text>
      ),
    },
    {
      label: t("mobile.detail.fact_concurrency", "Concurrency cap"),
      editable: true,
      value: (
        <Text className="text-sm text-foreground">{a.max_concurrent_tasks}</Text>
      ),
    },
    // RUYI-538 ①: reasoning-effort surfaced next to the model it qualifies,
    // with the editor one tap away via the card tap-through below.
    ...(showThinkingRow
      ? [
          {
            label: t("mobile.detail.fact_thinking", "Thinking"),
            editable: true,
            value: (
              <Text className="text-sm text-foreground" numberOfLines={1}>
                {thinkingLabel}
              </Text>
            ),
          },
        ]
      : []),
    {
      label: t("inspector.prop_owner", "Owner"),
      value: (
        <Text className="text-sm text-foreground" numberOfLines={1}>
          {owner?.name ?? "—"}
        </Text>
      ),
    },
  ];

  return (
    <View className="flex-1 bg-background">
      <Stack.Screen options={{ headerRight }} />
      <ActionSheetModal {...actionSheet.modalProps} />

      {/* 归档横幅：只读语义的可见信号 + 原位恢复（web detail 同布局）。 */}
      {isArchived ? (
        <View className="flex-row items-center gap-2 bg-muted px-4 py-2.5 border-b border-border">
          <Ionicons
            name="archive-outline"
            size={16}
            color={THEME[colorScheme].mutedForeground}
          />
          <Text className="flex-1 text-xs text-muted-foreground">
            {t(
              "detail.archived_banner",
              "This agent is archived. It cannot be assigned or mentioned.",
            )}
          </Text>
          {canManage ? (
            <Pressable onPress={restore} className="px-2 py-1" hitSlop={6}>
              <Text className="text-sm font-medium text-brand">
                {t("detail.restore", "Restore")}
              </Text>
            </Pressable>
          ) : null}
        </View>
      ) : null}

      {/* A8：未绑定 runtime 的引导横幅（可管理者可见，直达编辑页 runtime 段）。 */}
      {!a.runtime_id && canManage && !isArchived ? (
        <View className="flex-row items-center gap-2 bg-warning/10 px-4 py-2.5 border-b border-border">
          <Ionicons
            name="alert-circle-outline"
            size={16}
            color={THEME[colorScheme].warning}
          />
          <Text className="flex-1 text-xs text-foreground">
            {t(
              "mobile.detail.runtime_unbound_banner",
              "This agent has no runtime bound yet — bind one to start running it.",
            )}
          </Text>
          <Pressable
            onPress={openRunConfig}
            className="px-2 py-1"
            hitSlop={6}
          >
            <Text className="text-sm font-medium text-brand">
              {t("mobile.detail.bind_runtime", "Bind runtime")}
            </Text>
          </Pressable>
        </View>
      ) : null}

      <Tabs
        value={tab}
        onValueChange={(v) => setTab(v as DetailTab)}
        className="flex-1"
      >
        <TabsList className="mx-4 mt-2">
          <TabsTrigger value="overview">
            <Text>{t("tabs.overview", "Overview")}</Text>
          </TabsTrigger>
          <TabsTrigger value="active">
            <Text>
              {t("mobile.detail.tab_active", "Active")}
              {activeTasks.length > 0 ? ` (${activeTasks.length})` : ""}
            </Text>
          </TabsTrigger>
          <TabsTrigger value="settings">
            <Text>{t("tabs.settings", "Settings")}</Text>
          </TabsTrigger>
        </TabsList>

        {/* --- 概览 --- */}
        <TabsContent value="overview" className="flex-1">
          <ScrollView className="flex-1" contentContainerClassName="pb-8">
            <View className="flex-row items-center gap-3 px-4 pt-4 pb-4">
              <ActorAvatar
                type="agent"
                id={a.id}
                avatarUrl={a.avatar_url ? resolveAttachmentUrl(a.avatar_url) : null}
                size={56}
              />
              <View className="flex-1 min-w-0 gap-1.5">
                <Text
                  className="text-lg font-semibold text-foreground"
                  numberOfLines={1}
                >
                  {a.name}
                </Text>
                {a.description ? (
                  <Text
                    className="text-sm text-muted-foreground"
                    numberOfLines={3}
                  >
                    {a.description}
                  </Text>
                ) : null}
                <View className="flex-row">
                  <AccessScopeBadge agent={a} />
                </View>
              </View>
            </View>

            {/* RUYI-541/RUYI-624: instructions 缩略预览——完整查看与编辑
                在专用 edit-instructions 窗口（同小队编辑器模式）：managers
                点按编辑，读者点按只读查看全文。此前该入口（连同编辑资料行
                与事实卡）全部落到 edit-profile，半屏 sheet 顶部是指令输入
                框，Owner 读感即「所有入口都弹同一个指令框」。 */}
            <View className="mx-4 mb-3 gap-1.5">
              <Text className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
                {t("tabs.instructions", "Instructions")}
              </Text>
              <InstructionsPreview
                text={a.instructions}
                emptyHint={t("mobile.detail.instructions_empty", "No instructions yet")}
                numberOfLines={4}
                onTap={
                  !isArchived
                    ? openInstructions
                    : undefined
                }
                canEdit={canManage && !isArchived}
                accessibilityLabel={
                  canManage
                    ? t("mobile.detail.instructions_edit", "Edit instructions")
                    : t("tabs.instructions", "Instructions")
                }
              />
            </View>

            {/* A7：DM / Assign Work —— web 详情头同一对动作。chat 共享
                invocation 门（MUL-3963）；runtime 未绑定点了如实提示，
                与 web 的 toast 语义一致。 */}
            {!isArchived ? (
              <View className="flex-row gap-2 mx-4 mb-3">
                <Pressable
                  onPress={handleDm}
                  disabled={!permissionsReady}
                  className={`flex-row items-center justify-center gap-1.5 flex-1 rounded-lg border border-border py-2.5 ${
                    permissionsReady ? "active:bg-secondary" : "opacity-50"
                  }`}
                  accessibilityRole="button"
                  accessibilityLabel={t("detail.dm", "DM")}
                >
                  <Ionicons
                    name="chatbubble-ellipses-outline"
                    size={16}
                    color={THEME[colorScheme].foreground}
                  />
                  <Text className="text-sm font-medium text-foreground">
                    {t("detail.dm", "DM")}
                  </Text>
                </Pressable>
                {canInvoke ? (
                  <Pressable
                    onPress={handleAssign}
                    className="flex-row items-center justify-center gap-1.5 flex-1 rounded-lg bg-primary py-2.5 active:opacity-80"
                    accessibilityRole="button"
                    accessibilityLabel={t("detail.assign_work", "Assign work")}
                  >
                    <Ionicons
                      name="add"
                      size={16}
                      color={THEME[colorScheme].primaryForeground}
                    />
                    <Text className="text-sm font-medium text-primary-foreground">
                      {t("detail.assign_work", "Assign work")}
                    </Text>
                  </Pressable>
                ) : null}
              </View>
            ) : null}

            {canManage && !isArchived ? (
              <Pressable
                onPress={openEditProfile}
                className="flex-row items-center gap-3 mx-4 mb-3 rounded-lg border border-border px-4 py-3 active:bg-secondary"
                accessibilityRole="button"
                accessibilityLabel={t("mobile.detail.edit_profile", "Edit Profile")}
              >
                <Ionicons
                  name="pencil-outline"
                  size={18}
                  color={THEME[colorScheme].mutedForeground}
                />
                <Text className="flex-1 text-sm text-foreground">
                  {t("mobile.detail.edit_profile", "Edit Profile")}
                </Text>
                <Ionicons
                  name="chevron-forward"
                  size={16}
                  color={THEME[colorScheme].mutedForeground}
                />
              </Pressable>
            ) : null}

            {/* 关键事实组：执行行（运行时/模型/思考强度/并发上限）逐行可点
                直达 run-config 编辑器（web 概览事实行即执行配置编辑入口的
                移动端等价物，RUYI-624）；所有者与状态行只读。上面的 Edit
                Profile 行保留为资料编辑的显式入口。 */}
            <View className="mx-4 rounded-lg border border-border overflow-hidden">
              {factRows.map((row, i) => {
                const rowContent = (
                  <>
                    <Text className="w-20 text-xs text-muted-foreground shrink-0">
                      {row.label}
                    </Text>
                    <View className="flex-1 min-w-0">{row.value}</View>
                  </>
                );
                const rowClass = `flex-row items-center gap-3 px-4 py-2.5 ${
                  i > 0 ? "border-t border-border" : ""
                }`;
                return row.editable && factsEditable ? (
                  <Pressable
                    key={row.label}
                    onPress={openRunConfig}
                    className={`${rowClass} active:bg-secondary`}
                    accessibilityRole="button"
                    accessibilityLabel={`${row.label} — ${t("inspector.section_execution", "Execution")}`}
                  >
                    {rowContent}
                  </Pressable>
                ) : (
                  <View key={row.label} className={rowClass}>
                    {rowContent}
                  </View>
                );
              })}
              {presence && !isArchived ? (
                <View className="flex-row items-center gap-3 px-4 py-2.5 border-t border-border">
                  <Text className="w-20 text-xs text-muted-foreground shrink-0">
                    {t("mobile.detail.fact_status", "Status")}
                  </Text>
                  <View className="flex-1">
                    <AgentPresenceLine detail={presence} />
                  </View>
                </View>
              ) : null}
            </View>
          </ScrollView>
        </TabsContent>

        {/* --- 活跃 --- */}
        <TabsContent value="active" className="flex-1">
          <SectionList<AgentTask | HistoryEmptyRow>
            className="flex-1"
            sections={
              activeTasks.length === 0 && runHistory.length === 0
                ? []
                : [
                    {
                      key: "active",
                      title: t("mobile.detail.tab_active", "Active"),
                      data: activeTasks,
                    },
                    {
                      key: "history",
                      title: t("mobile.detail.section_history", "Run history"),
                      data:
                        runHistory.length === 0
                          ? [HISTORY_EMPTY]
                          : visibleHistory,
                    },
                  ]
            }
            keyExtractor={(item) => item.id}
            renderSectionHeader={({ section }) => (
              <View className="px-4 pt-3 pb-1 bg-background">
                <Text className="text-xs font-medium text-muted-foreground">
                  {section.title}
                </Text>
              </View>
            )}
            ItemSeparatorComponent={() => (
              <View className="h-px bg-border ml-4" />
            )}
            contentContainerClassName="pb-6"
            ListEmptyComponent={
              <View className="flex-1 items-center justify-center px-8 gap-2 pt-24">
                <Ionicons
                  name="moon-outline"
                  size={42}
                  color={THEME[colorScheme].mutedForeground}
                />
                <Text className="text-sm text-muted-foreground text-center">
                  {t("mobile.tasks.empty", "No active runs right now.")}
                </Text>
              </View>
            }
            ListFooterComponent={
              hasMoreHistory ? (
                <Pressable
                  onPress={() =>
                    setHistoryLimit((limit) => limit + RUN_HISTORY_PAGE)
                  }
                  className="items-center py-3 active:opacity-70"
                  accessibilityRole="button"
                  accessibilityLabel={t(
                    "mobile.run_history.load_more",
                    "Load more",
                  )}
                >
                  <Text className="text-sm text-brand">
                    {t("mobile.run_history.load_more", "Load more")}
                  </Text>
                </Pressable>
              ) : null
            }
            renderItem={({ item, section }) => {
              if ("historyEmpty" in item) {
                return (
                  <Text className="px-4 py-3 text-sm text-muted-foreground">
                    {t("mobile.run_history.empty", "No runs yet.")}
                  </Text>
                );
              }
              if (section.key === "history") {
                return (
                  <AgentRunHistoryRow
                    task={item}
                    issueTitle={
                      item.issue_id
                        ? (titleById.get(item.issue_id) ?? null)
                        : null
                    }
                    wsSlug={wsSlug}
                  />
                );
              }
              return (
                <SwipeableAgentTaskRow
                  task={item}
                  issueTitle={
                    item.issue_id
                      ? (titleById.get(item.issue_id) ?? null)
                      : null
                  }
                  wsSlug={wsSlug}
                  cancellable={
                    canManage && !isArchived && canCancelAgentTask(item)
                  }
                  onCancel={() => cancelTask.mutate(item.id)}
                />
              );
            }}
          />
        </TabsContent>

        {/* --- 设置 --- */}
        <TabsContent value="settings" className="flex-1">
          <ScrollView
            className="flex-1"
            contentContainerClassName="px-4 pt-3 pb-8"
          >
            <View className="rounded-lg border border-border overflow-hidden">
              {/* 能力行可见性与 web agent-overview-pane 同判定（RUYI-418 B3）。 */}
              {showMcpRow ? (
                <SettingsRow
                  icon="hardware-chip-outline"
                  label={t("tabs.mcp_config", "MCP")}
                  onPress={() =>
                    wsSlug &&
                    router.push({
                      pathname: "/[workspace]/more/agents/[id]/mcp",
                      params: { workspace: wsSlug, id: a.id },
                    })
                  }
                />
              ) : null}
              {showComposioRow ? (
                <SettingsRow
                  icon="apps-outline"
                  label={t("tabs.composio_mcp", "MCP Apps")}
                  onPress={() =>
                    wsSlug &&
                    router.push({
                      pathname: "/[workspace]/more/agents/[id]/composio",
                      params: { workspace: wsSlug, id: a.id },
                    })
                  }
                />
              ) : null}
              {integrationsConfigured ? (
                <SettingsRow
                  icon="chatbubble-ellipses-outline"
                  label={t("tabs.integrations", "Integrations")}
                  onPress={() =>
                    wsSlug &&
                    router.push({
                      pathname: "/[workspace]/more/agents/[id]/integrations",
                      params: { workspace: wsSlug, id: a.id },
                    })
                  }
                />
              ) : null}
              <SettingsRow
                icon="sparkles-outline"
                label={t("tabs.skills", "Skills")}
                onPress={() =>
                  wsSlug &&
                  router.push({
                    pathname: "/[workspace]/more/agents/[id]/skills",
                    params: { workspace: wsSlug, id: a.id },
                  })
                }
              />
              <SettingsRow
                icon="shield-outline"
                label={t("tabs.access", "Access")}
                onPress={() =>
                  wsSlug &&
                  router.push({
                    pathname: "/[workspace]/more/agents/[id]/access",
                    params: { workspace: wsSlug, id: a.id },
                  })
                }
              />
              <SettingsRow
                icon="terminal-outline"
                label={t("tabs.custom_args", "Custom Args")}
                onPress={() =>
                  wsSlug &&
                  router.push({
                    pathname: "/[workspace]/more/agents/[id]/custom-args",
                    params: { workspace: wsSlug, id: a.id },
                  })
                }
              />
              {showRoutingRow ? (
                <SettingsRow
                  icon="git-network-outline"
                  label={t("tabs.runtime_config", "Routing")}
                  onPress={() =>
                    wsSlug &&
                    router.push({
                      pathname: "/[workspace]/more/agents/[id]/runtime-config",
                      params: { workspace: wsSlug, id: a.id },
                    })
                  }
                />
              ) : null}
              {/* env 是唯一携带密钥语义的端点（web 同规则：仅 canEdit 可见），
                  其余行对全部读者可见。 */}
              {canManage ? (
                <SettingsRow
                  icon="key-outline"
                  label={t("tabs.environment", "Environment")}
                  onPress={() =>
                    wsSlug &&
                    router.push({
                      pathname: "/[workspace]/more/agents/[id]/env",
                      params: { workspace: wsSlug, id: a.id },
                    })
                  }
                />
              ) : null}
              <SettingsRow
                icon="link-outline"
                label={t("tabs.webhooks", "Webhook")}
                onPress={() =>
                  wsSlug &&
                  router.push({
                    pathname: "/[workspace]/more/agents/[id]/webhooks",
                    params: { workspace: wsSlug, id: a.id },
                  })
                }
                last
              />
            </View>
          </ScrollView>
        </TabsContent>
      </Tabs>
    </View>
  );
}

function SettingsRow({
  icon,
  label,
  onPress,
  last,
}: {
  icon: React.ComponentProps<typeof Ionicons>["name"];
  label: string;
  onPress: () => void;
  last?: boolean;
}) {
  const { colorScheme } = useColorScheme();
  return (
    <Pressable
      onPress={onPress}
      className={`flex-row items-center gap-3 px-4 py-3 active:bg-secondary ${
        last ? "" : "border-b border-border"
      }`}
      accessibilityRole="button"
      accessibilityLabel={label}
    >
      <Ionicons
        name={icon}
        size={18}
        color={THEME[colorScheme].mutedForeground}
      />
      <Text className="flex-1 text-sm text-foreground">{label}</Text>
      <Ionicons
        name="chevron-forward"
        size={16}
        color={THEME[colorScheme].mutedForeground}
      />
    </Pressable>
  );
}
