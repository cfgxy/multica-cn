/**
 * Workspace squads list (RUYI-346 S1) — mobile squad management entry.
 * Search + row navigation + long-press Archive for managers; the scope
 * segments / sort dimensions of web's toolbar are out of P0 scope (design
 * §3 裁剪行, same call as the agents list).
 *
 * Manager rule mirrors the server (MUL-4223 canManageSquad): workspace
 * owner/admin OR squad creator. Archived squads render greyed with the
 * archived badge; archiving is a one-way move (no restore endpoint).
 */
import { useMemo, useState } from "react";
import { Alert, FlatList, Pressable, TextInput, View } from "react-native";
import { router, useLocalSearchParams } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { useQuery } from "@tanstack/react-query";
import type { Squad } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import {
  ActionSheetModal,
  useActionSheet,
} from "@/components/ui/action-sheet";
import { squadListOptions } from "@/data/queries/squads";
import { memberListOptions } from "@/data/queries/members";
import { useDeleteSquad } from "@/data/mutations/squads";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useActorLookup } from "@/data/use-actor-name";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";
import { cn } from "@/lib/utils";

export default function SquadsPage() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { workspace: wsSlug } = useLocalSearchParams<{ workspace: string }>();
  const me = useAuthStore((s) => s.user);
  const { colorScheme } = useColorScheme();
  const { t } = useT("squads");
  const { getName } = useActorLookup();
  const { show, modalProps } = useActionSheet();
  const [archiveTarget, setArchiveTarget] = useState<Squad | null>(null);
  // The mutation binds the id at hook level; archiveTarget is set before the
  // confirm Alert can fire, so the id is always real when mutate runs.
  const deleteSquad = useDeleteSquad(archiveTarget?.id ?? "");

  const { data: squads } = useQuery(squadListOptions(wsId));
  const { data: members } = useQuery(memberListOptions(wsId));

  const [search, setSearch] = useState("");

  const isWorkspaceAdmin = useMemo(() => {
    if (!me) return false;
    const mine = members?.find((m) => m.user_id === me.id);
    return mine?.role === "owner" || mine?.role === "admin";
  }, [members, me]);

  const canManageSquad = (squad: Squad) =>
    isWorkspaceAdmin || (!!me && squad.creator_id === me.id);

  const visible = useMemo(() => {
    const list = squads ?? [];
    const q = search.trim().toLowerCase();
    const filtered = q
      ? list.filter((s) => s.name.toLowerCase().includes(q))
      : list;
    // Active first (creation order preserved by the API), archived pinned
    // last — same shape as the agents list's archived scope.
    return [...filtered].sort((a, b) => {
      if (!!a.archived_at !== !!b.archived_at) return a.archived_at ? 1 : -1;
      return 0;
    });
  }, [squads, search]);

  const confirmArchive = (squad: Squad) => {
    setArchiveTarget(squad);
    Alert.alert(
      t("archive_dialog.title", "Archive this squad?"),
      t(
        "archive_dialog.description",
        '"{{name}}" will be archived. Issues currently assigned to this squad will be transferred to its leader. This can\'t be undone — create a new squad if you need the routing back.',
        { name: squad.name },
      ),
      [
        {
          text: t("archive_dialog.cancel", "Cancel"),
          style: "cancel",
          onPress: () => setArchiveTarget(null),
        },
        {
          text: t("archive_dialog.confirm", "Archive"),
          style: "destructive",
          onPress: () => {
            deleteSquad.mutate(undefined, {
              onError: () =>
                Alert.alert(t("toasts.archive_failed", "Failed to archive squad")),
            });
            setArchiveTarget(null);
          },
        },
      ],
    );
  };

  const longPressRow = (squad: Squad) => {
    if (!canManageSquad(squad)) return;
    show({
      title: t("page.row_menu", "Squad actions"),
      options: [
        t("archive_dialog.confirm", "Archive"),
        t("archive_dialog.cancel", "Cancel"),
      ],
      cancelButtonIndex: 1,
      destructiveButtonIndex: 0,
      onSelect: (index) => {
        if (index === 0) confirmArchive(squad);
      },
    });
  };

  return (
    <View className="flex-1 bg-background">
      {/* 头部（Stack 注册 title，body 补搜索与新建） */}
      <View className="flex-row items-center px-4 pt-2 pb-1">
        <Text className="flex-1 text-2xl font-bold text-foreground">
          {t("page.title", "Squads")}
        </Text>
        <Pressable
          onPress={() => {
            if (!wsSlug) return;
            router.push({
              pathname: "/[workspace]/more/squads/new",
              params: { workspace: wsSlug },
            });
          }}
          hitSlop={8}
          accessibilityRole="button"
          accessibilityLabel={t("page.new_button", "New Squad")}
        >
          <Ionicons
            name="add-circle-outline"
            size={26}
            color={THEME[colorScheme].foreground}
          />
        </Pressable>
      </View>
      <View className="flex-row items-center gap-2 mx-4 my-2 rounded-lg bg-muted px-3 py-2">
        <Ionicons
          name="search"
          size={16}
          color={THEME[colorScheme].mutedForeground}
        />
        <TextInput
          value={search}
          onChangeText={setSearch}
          placeholder={t("mobile.page.search_placeholder", "Search squads…")}
          placeholderTextColor={THEME[colorScheme].mutedForeground}
          className="flex-1 text-sm text-foreground"
          autoCorrect={false}
          autoCapitalize="none"
        />
      </View>
      <FlatList
        className="flex-1"
        data={visible}
        keyExtractor={(squad) => squad.id}
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
                ? t("page.no_matches", "No squads match")
                : t("page.empty_no_squads", "No squads yet. Create one to get started.")}
            </Text>
            {!search.trim() ? (
              <Pressable
                onPress={() => {
                  if (!wsSlug) return;
                  router.push({
                    pathname: "/[workspace]/more/squads/new",
                    params: { workspace: wsSlug },
                  });
                }}
                className="mt-2 rounded-lg bg-primary px-4 py-2"
              >
                <Text className="text-sm font-medium text-primary-foreground">
                  {t("page.new_button", "New Squad")}
                </Text>
              </Pressable>
            ) : null}
          </View>
        }
        renderItem={({ item: squad }) => {
          const isArchived = !!squad.archived_at;
          const leaderName = squad.leader_id
            ? getName("agent", squad.leader_id)
            : null;
          return (
            <Pressable
              onPress={() => {
                if (!wsSlug) return;
                router.push({
                  pathname: "/[workspace]/more/squads/[id]",
                  params: { workspace: wsSlug, id: squad.id },
                });
              }}
              onLongPress={() => longPressRow(squad)}
              className={cn(
                "flex-row items-center gap-3 px-4 py-3 active:bg-secondary",
                isArchived && "opacity-50",
              )}
            >
              <ActorAvatar type="squad" id={squad.id} size={40} />
              <View className="flex-1 min-w-0">
                <View className="flex-row items-center gap-2">
                  <Text
                    numberOfLines={1}
                    className="text-sm font-medium text-foreground"
                  >
                    {squad.name}
                  </Text>
                  {isArchived ? (
                    <View className="rounded bg-muted-foreground/10 px-1.5 py-0.5">
                      <Text className="text-[10px] text-muted-foreground">
                        {t("profile_card.archived", "Archived")}
                      </Text>
                    </View>
                  ) : null}
                </View>
                <Text numberOfLines={1} className="text-xs text-muted-foreground">
                  {leaderName
                    ? `${t("details.leader", "Leader")}: ${leaderName}`
                    : t("inspector.details_section", "Details")}
                </Text>
              </View>
              {typeof squad.member_count === "number" ? (
                <Text className="text-xs text-muted-foreground">
                  {squad.member_count}
                </Text>
              ) : null}
              <Ionicons
                name="chevron-forward"
                size={16}
                color={THEME[colorScheme].mutedForeground}
              />
            </Pressable>
          );
        }}
      />
      <ActionSheetModal {...modalProps} />
    </View>
  );
}
