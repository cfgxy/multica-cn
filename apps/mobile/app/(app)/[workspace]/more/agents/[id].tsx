/**
 * Agent detail (`more/agents/[id]`, RUYI-76 ②) — upgraded in place by
 * RUYI-346 into the design's A2 three-tab screen: 概览 / 活跃 / 设置.
 *
 *   - 概览   profile card (avatar / name / description / access badge /
 *           archived banner) + key facts (runtime + online status, model,
 *           concurrency cap, owner, presence) + the edit entry.
 *   - 活跃   the per-agent active-run list (running first, then queued —
 *           `selectAgentActiveTasks`) with a row-level cancel affordance.
 *           The only cancel endpoint in P0 is agent-level cancel-tasks, so
 *           the confirm dialog is the web `cancel_dialog_*` copy that says
 *           exactly that ("cancel ALL of <name>'s tasks").
 *   - 设置   grouped navigation rows into the skills / env / webhooks
 *           sub-screens. MCP / integrations / runtime config / custom args
 *           / conversation starters are P1 (design §2) — rows simply don't
 *           exist yet.
 *
 * Data: `agentDetailOptions` is the primary source (deep-link safe and the
 * cache mutations keep it warm); presence / snapshot / issues / members /
 * runtimes are the shared workspace queries the prefetch warms, with the
 * agents list as the paint-from-push fallback while the detail fetch is in
 * flight. Archive and restore live in the header more-menu (system_key
 * agents hide the menu — they cannot be archived, mirroring web's
 * agent-row-actions); an archived agent renders read-only behind a restore
 * banner.
 */
import { useCallback, useMemo, useState } from "react";
import { Alert, FlatList, Pressable, ScrollView, View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import * as Haptics from "expo-haptics";
import { Ionicons } from "@expo/vector-icons";
import { Stack, router, useLocalSearchParams } from "expo-router";
import type { Agent } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { ActionSheetModal, useActionSheet } from "@/components/ui/action-sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { AgentPresenceLine } from "@/components/agents/agent-presence-line";
import { AgentTaskRow } from "@/components/agents/agent-task-row";
import { AccessScopeBadge } from "@/components/agents/access-scope-badge";
import { agentDetailOptions, agentListOptions } from "@/data/queries/agents";
import { issueListOptions } from "@/data/queries/issues";
import { agentTaskSnapshotOptions } from "@/data/queries/agent-task-snapshot";
import { runtimeListOptions } from "@/data/queries/runtimes";
import { memberListOptions } from "@/data/queries/members";
import {
  useArchiveAgent,
  useCancelAgentTasks,
  useRestoreAgent,
} from "@/data/mutations/agents";
import { selectAgentActiveTasks } from "@/lib/issue-agent-activity";
import { useWorkspacePresenceMap } from "@/lib/use-agent-presence";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

type DetailTab = "overview" | "active" | "settings";

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
  const cancelTasks = useCancelAgentTasks(agentId);

  const {
    data: fetched,
    isLoading,
    isError,
  } = useQuery(agentDetailOptions(wsId, agentId));
  // `id: ""` is the schema fallback sentinel — treat it as "not found".
  const detailAgent: Agent | null = fetched && fetched.id ? fetched : null;

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

  // Title lookup only — an error here must NOT blank the run list.
  const { data: issues } = useQuery(issueListOptions(wsId));
  const titleById = useMemo(() => {
    const map = new Map<string, string>();
    for (const issue of issues ?? []) map.set(issue.id, issue.title);
    return map;
  }, [issues]);

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

  const canManage =
    !!agent && (isWorkspaceAdmin || (!!me && agent.owner_id === me.id));
  const isArchived = !!agent?.archived_at;
  const isSystem = !!agent?.system_key;

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

  const confirmCancelAll = useCallback(() => {
    if (!agent || activeTasks.length === 0) return;
    Alert.alert(
      t("row_actions.cancel_dialog_title", "Cancel all of \"{{name}}\"'s tasks?", {
        name: agent.name,
      }),
      [
        t("row_actions.cancel_dialog_impact_other", "This will cancel {{summary}} tasks.", {
          summary: String(activeTasks.length),
        }),
        t(
          "row_actions.cancel_dialog_irreversible",
          "Cancelled tasks cannot be resumed.",
        ),
      ].join(""),
      [
        { text: t("row_actions.cancel_dialog_keep", "Keep them"), style: "cancel" },
        {
          text: t("row_actions.cancel_dialog_confirm", "Cancel all tasks"),
          style: "destructive",
          onPress: () => cancelTasks.mutate(),
        },
      ],
    );
  }, [agent, activeTasks.length, cancelTasks, t]);

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
          name="help-circle-outline"
          size={42}
          color={THEME[colorScheme].mutedForeground}
        />
        <Text className="text-base font-medium text-foreground text-center">
          {t("detail.not_found_title", "Agent not found")}
        </Text>
        <Text className="text-sm text-muted-foreground text-center">
          {t(
            "detail.not_found_default",
            "This agent may have been archived or deleted.",
          )}
        </Text>
      </View>
    );
  }

  const a = agent;

  const factRows: { label: string; value: React.ReactNode }[] = [
    {
      label: t("inspector.prop_runtime", "Runtime"),
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
      value: (
        <Text className="text-sm text-foreground" numberOfLines={1}>
          {a.model || t("pickers.model_default", "Default")}
        </Text>
      ),
    },
    {
      label: t("inspector.prop_concurrency", "Concurrency"),
      value: (
        <Text className="text-sm text-foreground">{a.max_concurrent_tasks}</Text>
      ),
    },
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

      <Tabs
        value={tab}
        onValueChange={(v) => setTab(v as DetailTab)}
        className="flex-1"
      >
        <TabsList className="mx-4 mt-2">
          <TabsTrigger value="overview">
            {t("tabs.overview", "Overview")}
          </TabsTrigger>
          <TabsTrigger value="active">
            {t("mobile.detail.tab_active", "Active")}
            {activeTasks.length > 0 ? ` (${activeTasks.length})` : ""}
          </TabsTrigger>
          <TabsTrigger value="settings">
            {t("tabs.settings", "Settings")}
          </TabsTrigger>
        </TabsList>

        {/* --- 概览 --- */}
        <TabsContent value="overview" className="flex-1">
          <ScrollView className="flex-1" contentContainerClassName="pb-8">
            <View className="flex-row items-center gap-3 px-4 pt-4 pb-4">
              <ActorAvatar type="agent" id={a.id} size={56} />
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

            {canManage && !isArchived ? (
              <Pressable
                onPress={() => {
                  if (!wsSlug) return;
                  router.push({
                    pathname: "/[workspace]/more/agents/[id]/edit-profile",
                    params: { workspace: wsSlug, id: a.id },
                  });
                }}
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

            {/* 关键事实组 */}
            <View className="mx-4 rounded-lg border border-border overflow-hidden">
              {factRows.map((row, i) => (
                <View
                  key={row.label}
                  className={`flex-row items-center gap-3 px-4 py-2.5 ${
                    i > 0 ? "border-t border-border" : ""
                  }`}
                >
                  <Text className="w-20 text-xs text-muted-foreground shrink-0">
                    {row.label}
                  </Text>
                  <View className="flex-1 min-w-0">{row.value}</View>
                </View>
              ))}
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
          <FlatList
            className="flex-1"
            data={activeTasks}
            keyExtractor={(task) => task.id}
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
            renderItem={({ item }) => (
              <View className="flex-row items-center">
                <View className="flex-1 min-w-0">
                  <AgentTaskRow
                    task={item}
                    issueTitle={
                      item.issue_id
                        ? (titleById.get(item.issue_id) ?? null)
                        : null
                    }
                    wsSlug={wsSlug}
                  />
                </View>
                {/* 行级取消：P0 只有 agent 级 cancel-tasks 端点，确认弹窗
                    文案明确「取消全部 task」（镜像 web cancel_dialog_*）。 */}
                {canManage && !isArchived ? (
                  <Pressable
                    onPress={confirmCancelAll}
                    disabled={cancelTasks.isPending}
                    className="px-3 py-2 mr-1"
                    hitSlop={4}
                    accessibilityRole="button"
                    accessibilityLabel={t(
                      "row_actions.cancel_all_tasks",
                      "Cancel all tasks",
                    )}
                  >
                    <Text className="text-sm text-destructive">
                      {t("row_actions.cancel_all_tasks", "Cancel all tasks")}
                    </Text>
                  </Pressable>
                ) : null}
              </View>
            )}
          />
        </TabsContent>

        {/* --- 设置 --- */}
        <TabsContent value="settings" className="flex-1">
          <ScrollView
            className="flex-1"
            contentContainerClassName="px-4 pt-3 pb-8"
          >
            <View className="rounded-lg border border-border overflow-hidden">
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
            {/* P1 行（MCP / 集成 / 运行时配置 / 自定义参数 / 开场白）按设计
                §2 直接隐藏，功能到位后再补导航行。 */}
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
