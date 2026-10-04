/**
 * Workspace agents list — mobile's agent management surface (RUYI-76 ②,
 * upgraded in place by RUYI-346 from the read-only monitoring list to the
 * A1 screen of the design: scope segments + search + access badges +
 * long-press row actions + create).
 *
 * Data: agents / runtimes / agent-task-snapshot via `useWorkspacePresenceMap`
 * — the same three queries the workspace prefetch warms on entry and
 * `use-presence-realtime` keeps fresh, so this screen normally paints from
 * warm caches.
 *
 * Parity notes (apps/mobile/CLAUDE.md + design §7):
 * - Scope semantics mirror web's agents-page EXACTLY: counts come from the
 *   FULL set (search never affects them); `mine` = non-archived &
 *   owner_id === me; `all` = non-archived; `archived` ignores the ownership
 *   lens. `canManage` = workspace owner/admin OR agent owner.
 * - Presence semantics come from the SHARED pure derivation
 *   (`buildPresenceMap` in @multica/core/agents); rows still sort
 *   working → queued → idle (name asc in band) inside the non-archived
 *   scopes — the monitoring order this screen is known for (unchanged).
 * - Row actions via the cross-platform useActionSheet hook (iOS
 *   ActionSheetIOS, Android bottom modal) — 编辑 / 归档·恢复, archived rows
 *   render greyed like web's archived scope.
 */
import { useMemo, useState } from "react";
import { Alert, FlatList, Pressable, TextInput, View } from "react-native";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { useQuery } from "@tanstack/react-query";
import type { Agent } from "@multica/core/types";
import type { Workload } from "@multica/core/agents";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { AgentPresenceLine } from "@/components/agents/agent-presence-line";
import { AccessScopeBadge } from "@/components/agents/access-scope-badge";
import {
  ActionSheetModal,
  useActionSheet,
} from "@/components/ui/action-sheet";
import { agentListOptions } from "@/data/queries/agents";
import { memberListOptions } from "@/data/queries/members";
import { useArchiveAgent, useRestoreAgent } from "@/data/mutations/agents";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useWorkspacePresenceMap } from "@/lib/use-agent-presence";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";
import { cn } from "@/lib/utils";

type AgentsScope = "mine" | "all" | "archived";

const WORKLOAD_RANK: Record<Workload, number> = {
  working: 0,
  queued: 1,
  idle: 2,
};

export default function AgentsPage() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const me = useAuthStore((s) => s.user);
  const { colorScheme } = useColorScheme();
  const { t } = useT("agents");
  const actionSheet = useActionSheet();
  const archiveAgent = useArchiveAgent();
  const restoreAgent = useRestoreAgent();
  const [scope, setScope] = useState<AgentsScope>("mine");
  const [search, setSearch] = useState("");

  const { data: agents, isLoading: agentsLoading } = useQuery(
    agentListOptions(wsId),
  );
  const { byAgent, loading: presenceLoading } = useWorkspacePresenceMap(wsId);
  const { data: members } = useQuery(memberListOptions(wsId));

  const isWorkspaceAdmin = useMemo(() => {
    if (!me) return false;
    const mine = members?.find((m) => m.user_id === me.id);
    return mine?.role === "owner" || mine?.role === "admin";
  }, [members, me]);

  // Counts from the FULL set — search and scope never affect them (web
  // agents-page parity, design §7).
  const scopeCounts = useMemo(() => {
    let mine = 0;
    let all = 0;
    let archived = 0;
    for (const a of agents ?? []) {
      if (a.archived_at) {
        archived++;
        continue;
      }
      all++;
      if (me && a.owner_id === me.id) mine++;
    }
    return { mine, all, archived } as Record<AgentsScope, number>;
  }, [agents, me]);

  const visible = useMemo(() => {
    const inScope = (agents ?? []).filter((a) => {
      if (scope === "archived") return !!a.archived_at;
      if (a.archived_at) return false;
      if (scope === "mine") return !!me && a.owner_id === me.id;
      return true;
    });
    const q = search.trim().toLowerCase();
    const filtered = q
      ? inScope.filter((a) => a.name.toLowerCase().includes(q))
      : inScope;
    if (scope === "archived") {
      // Archived scope: stable alphabetical, no presence banding (web sorts
      // archived rows by name too).
      return filtered.sort((a, b) => a.name.localeCompare(b.name));
    }
    // Working first, then queued, then idle — the monitoring order the
    // screen exists for (unchanged). Name asc inside each band.
    return filtered.sort((a, b) => {
      const rankA = WORKLOAD_RANK[byAgent.get(a.id)?.workload ?? "idle"];
      const rankB = WORKLOAD_RANK[byAgent.get(b.id)?.workload ?? "idle"];
      if (rankA !== rankB) return rankA - rankB;
      return a.name.localeCompare(b.name);
    });
  }, [agents, scope, search, me, byAgent]);

  const loading = (agentsLoading || presenceLoading) && visible.length === 0;

  const showRowActions = (agent: Agent) => {
    const canManage =
      isWorkspaceAdmin || (!!me && agent.owner_id === me.id);
    if (!canManage) {
      // 镜像 web agent-row-actions：「隐藏而非禁用」——无权限者不弹菜单。
      return;
    }
    const isArchived = !!agent.archived_at;
    const isSystem = !!agent.system_key;
    // 内建智能体（system_key）不可归档/恢复，镜像 web row-actions 只留编辑。
    // `mobile.row.edit` is a mobile-only key (web has no row-level edit
    // label); cancel reuses `create_dialog.cancel` to stay in-namespace.
    const options = isArchived
      ? [
          t("detail.restore", "Restore"),
          t("create_dialog.cancel", "Cancel"),
        ]
      : isSystem
        ? [
            t("mobile.row.edit", "Edit"),
            t("create_dialog.cancel", "Cancel"),
          ]
        : [
            t("mobile.row.edit", "Edit"),
            t("row_actions.archive", "Archive"),
            t("create_dialog.cancel", "Cancel"),
          ];
    actionSheet.show({
      options,
      cancelButtonIndex: options.length - 1,
      title: agent.name,
      onSelect: (index) => {
        if (!wsSlug) return;
        if (!isArchived && !isSystem && index === 0) {
          router.push({
            pathname: "/[workspace]/more/agents/[id]/edit-profile",
            params: { workspace: wsSlug, id: agent.id },
          });
          return;
        }
        if (isArchived && index === 0) {
          restoreAgent.mutate(agent.id);
          return;
        }
        if (!isArchived && !isSystem && index === 1) {
          Alert.alert(
            t("row_actions.archive_dialog_title", 'Archive "{{name}}"?', {
              name: agent.name,
            }),
            t(
              "row_actions.archive_dialog_description",
              "The agent won't be assignable or mentionable, and any active tasks will be cancelled. All history is preserved and you can restore it later.",
            ),
            [
              { text: t("create_dialog.cancel", "Cancel"), style: "cancel" },
              {
                text: t("row_actions.archive", "Archive"),
                style: "destructive",
                onPress: () => archiveAgent.mutate(agent.id),
              },
            ],
          );
        }
      },
    });
  };

  if (loading) {
    return (
      <View className="flex-1 items-center justify-center bg-background">
        <Text className="text-sm text-muted-foreground">
          {t("page.list_loading", "Loading agents…")}
        </Text>
      </View>
    );
  }

  return (
    <View className="flex-1 bg-background">
      <ActionSheetModal {...actionSheet.modalProps} />
      {/* Scope segments + counts — three fixed scopes, counts from the full
          set (web parity). Segmented control hand-rolled: ui/tabs is built
          for content panes (it renders TabsContent), a row of three pills
          matches the issue list's scope pattern better. */}
      <View className="flex-row gap-2 px-4 pt-2 pb-1">
        {(["mine", "all", "archived"] as const).map((s) => {
          const active = scope === s;
          const label =
            s === "mine"
              ? t("scope.mine", "Mine")
              : s === "all"
                ? t("scope.all", "All")
                : t("scope.archived", "Archived");
          return (
            <Pressable
              key={s}
              onPress={() => setScope(s)}
              accessibilityRole="button"
              accessibilityState={{ selected: active }}
              className={cn(
                "flex-row items-center gap-1 rounded-full px-3 py-1.5",
                active ? "bg-primary" : "bg-muted",
              )}
            >
              <Text
                className={cn(
                  "text-xs font-medium",
                  active ? "text-primary-foreground" : "text-foreground",
                )}
              >
                {label}
              </Text>
              <Text
                className={cn(
                  "text-xs",
                  active ? "text-primary-foreground/80" : "text-muted-foreground",
                )}
              >
                {scopeCounts[s]}
              </Text>
            </Pressable>
          );
        })}
      </View>
      {/* Search — P0 replaces web's filter dimensions (design §3 裁剪行)。 */}
      <View className="flex-row items-center gap-2 mx-4 my-2 rounded-lg bg-muted px-3 py-2">
        <Ionicons
          name="search"
          size={16}
          color={THEME[colorScheme].mutedForeground}
        />
        <TextInput
          value={search}
          onChangeText={setSearch}
          placeholder={t("page.search_placeholder", "Search agents…")}
          placeholderTextColor={THEME[colorScheme].mutedForeground}
          className="flex-1 text-sm text-foreground"
          autoCorrect={false}
          autoCapitalize="none"
        />
      </View>
      <FlatList
        className="flex-1"
        data={visible}
        keyExtractor={(agent) => agent.id}
        ItemSeparatorComponent={() => <View className="h-px bg-border ml-16" />}
        contentContainerClassName="pb-6"
        ListEmptyComponent={
          <View className="flex-1 items-center justify-center px-8 gap-2 pt-24">
            <Ionicons
              name="people-outline"
              size={42}
              color={THEME[colorScheme].mutedForeground}
            />
            <Text className="text-base font-medium text-foreground text-center">
              {search.trim()
                ? scope === "archived"
                  ? t(
                      "no_matches.search_archived",
                      "No archived agents match \"{{query}}\".",
                      { query: search.trim() },
                    )
                  : t("no_matches.title", "No matches")
                : scope === "archived"
                  ? t("no_matches.no_archived", "No archived agents yet.")
                  : t("empty.title", "No agents yet")}
            </Text>
            {!search.trim() && scope !== "archived" ? (
              <Text className="text-sm text-muted-foreground text-center">
                {t(
                  "empty.description",
                  "Create an agent and assign it issues, like any teammate. Local agents run on your machine; cloud agents run on Multica's runtime.",
                )}
              </Text>
            ) : null}
            {!search.trim() && scope === "mine" ? (
              <Pressable
                onPress={() => {
                  if (!wsSlug) return;
                  router.push({
                    pathname: "/[workspace]/more/agents/new",
                    params: { workspace: wsSlug },
                  });
                }}
                className="mt-2 rounded-lg bg-primary px-4 py-2"
              >
                <Text className="text-sm font-medium text-primary-foreground">
                  {t("page.new_agent", "New agent")}
                </Text>
              </Pressable>
            ) : null}
          </View>
        }
        renderItem={({ item: agent }) => {
          const detail = byAgent.get(agent.id);
          const isArchived = !!agent.archived_at;
          return (
            <Pressable
              onPress={() => {
                if (!wsSlug) return;
                router.push({
                  pathname: "/[workspace]/more/agents/[id]",
                  params: { workspace: wsSlug, id: agent.id },
                });
              }}
              onLongPress={() => showRowActions(agent)}
              className={cn(
                "flex-row items-center gap-3 bg-background active:bg-secondary px-4 py-3",
                isArchived && "opacity-50",
              )}
              accessibilityLabel={agent.name}
            >
              <ActorAvatar type="agent" id={agent.id} size={40} />
              <View className="flex-1 min-w-0 gap-1">
                <Text
                  className="text-sm font-medium text-foreground"
                  numberOfLines={1}
                >
                  {agent.name}
                </Text>
                {detail && !isArchived ? (
                  <AgentPresenceLine detail={detail} />
                ) : null}
              </View>
              <AccessScopeBadge agent={agent} />
              <Ionicons
                name="chevron-forward"
                size={16}
                color={THEME[colorScheme].mutedForeground}
              />
            </Pressable>
          );
        }}
      />
    </View>
  );
}
