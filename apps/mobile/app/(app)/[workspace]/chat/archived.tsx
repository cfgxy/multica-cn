/**
 * Archived chats sub-view (RUYI-533) — the mobile counterpart of web's
 * archived view in chat-thread-list.tsx, reached from the "Archived" entry
 * at the bottom of the chat tab list. Entry pattern matches the RUYI-532
 * archived-inbox sub-view: the header (title + back) comes from the
 * Stack.Screen registration in [workspace]/_layout.tsx.
 *
 * Data source is the same flat `status=all` sessions cache the tab list
 * reads, split locally via `splitChatSessions` — web's single-cache design,
 * so the optimistic patch inside useSetChatSessionArchived flips `status`
 * in that one cache: unarchiving here drops the row at once and the main
 * list + entry count recover in the same frame. Rows keep the main list's
 * long-press surface, reduced to Unarchive / Delete (archived sessions are
 * read-only server-side — rename/pin are pointless there; delete needs the
 * same confirm as the main list).
 */
import { useCallback, useMemo } from "react";
import { Alert, FlatList, View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import * as Haptics from "expo-haptics";
import type { ChatSession } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  useActionSheet,
  ActionSheetModal,
} from "@/components/ui/action-sheet";
import { ChatSessionRow } from "@/components/chat/chat-session-row";
import { chatSessionsOptions, splitChatSessions } from "@/data/queries/chat";
import {
  useDeleteChatSession,
  useSetChatSessionArchived,
} from "@/data/mutations/chat";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { chatSessionDisplayTitle } from "@/lib/chat-session-title";
import { useT } from "@/lib/use-t";

export default function ArchivedChats() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { colorScheme } = useColorScheme();
  const { t } = useT("chat");
  const {
    data: rawSessions = [],
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery(chatSessionsOptions(wsId));
  const { archived: sessions } = useMemo(
    () => splitChatSessions(rawSessions),
    [rawSessions],
  );

  const deleteSession = useDeleteChatSession();
  const setArchived = useSetChatSessionArchived();
  const sheet = useActionSheet();

  // 不能复用 chat:window.untitled —— 那条 en 是 "New chat"，与「无标题
  // 会话」语义不同（同主列表）。
  const untitled = t("mobile.sessions.untitled", "Untitled chat");

  const openSession = useCallback(
    (sessionId: string) => {
      if (!wsSlug) return;
      router.push({
        pathname: "/[workspace]/chat/[sessionId]",
        params: { workspace: wsSlug, sessionId },
      });
    },
    [wsSlug],
  );

  const confirmDelete = useCallback(
    (session: ChatSession) => {
      Alert.alert(
        t("mobile.sessions.delete_title", "Delete this chat?"),
        chatSessionDisplayTitle(session.title, untitled),
        [
          { text: t("common:cancel", "Cancel"), style: "cancel" },
          {
            text: t("common:delete", "Delete"),
            style: "destructive",
            onPress: () => deleteSession.mutate(session.id),
          },
        ],
        { cancelable: true },
      );
    },
    [deleteSession, t, untitled],
  );

  const showSessionActions = useCallback(
    (session: ChatSession) => {
      Haptics.selectionAsync().catch(() => {});

      const actions: (
        | { kind: "unarchive" }
        | { kind: "delete" }
        | { kind: "cancel" }
      )[] = [];
      const options: string[] = [];
      const push = (label: string, action: (typeof actions)[number]) => {
        options.push(label);
        actions.push(action);
      };

      push(t("header.unarchive", "Unarchive chat"), { kind: "unarchive" });
      push(t("header.delete", "Delete chat"), { kind: "delete" });
      push(t("common:cancel", "Cancel"), { kind: "cancel" });

      sheet.show({
        options,
        cancelButtonIndex: options.length - 1,
        destructiveButtonIndex: options.length - 2,
        title: chatSessionDisplayTitle(session.title, untitled),
        onSelect: (i) => {
          const action = actions[i];
          if (!action || action.kind === "cancel") return;
          switch (action.kind) {
            case "unarchive":
              setArchived.mutate({ sessionId: session.id, archived: false });
              return;
            case "delete":
              confirmDelete(session);
              return;
          }
        },
      });
    },
    [confirmDelete, setArchived, sheet, t, untitled],
  );

  return (
    <View className="flex-1 bg-background">
      {isLoading ? (
        <View testID="chat-archived-loading" className="px-4 pt-4 gap-4">
          {Array.from({ length: 6 }).map((_, row) => (
            <View key={row} className="flex-row gap-3">
              <Skeleton className="size-9 rounded-full" />
              <View className="flex-1 gap-2 pt-1">
                <Skeleton className="h-3.5 w-3/4" />
                <Skeleton className="h-3 w-1/2" />
              </View>
            </View>
          ))}
        </View>
      ) : isError ? (
        <View className="flex-1 items-center justify-center gap-3 px-6">
          <Text className="text-sm text-muted-foreground text-center">
            {error instanceof Error
              ? error.message
              : t("common:mobile.common.unknown_error", "unknown error")}
          </Text>
          <Button variant="outline" onPress={() => refetch()}>
            <Text>{t("common:mobile.common.retry", "Retry")}</Text>
          </Button>
        </View>
      ) : sessions.length === 0 ? (
        // Unreachable through normal navigation (the entry only shows when
        // the pool is non-empty) — this is the drained-after-restore frame.
        <View testID="chat-archived-empty" className="flex-1 items-center justify-center px-8 gap-3">
          <Ionicons
            name="archive-outline"
            size={42}
            color={THEME[colorScheme].mutedForeground}
          />
          <Text className="text-base font-medium text-foreground text-center">
            {t("list.archived_title", "Archived")}
          </Text>
        </View>
      ) : (
        <FlatList
          data={sessions}
          keyExtractor={(session) => session.id}
          showsVerticalScrollIndicator={false}
          ItemSeparatorComponent={() => (
            <View className="h-px bg-border ml-16" />
          )}
          contentContainerClassName="pb-6"
          renderItem={({ item: session }) => (
            <ChatSessionRow
              session={session}
              untitled={untitled}
              onPress={() => openSession(session.id)}
              onLongPress={() => showSessionActions(session)}
            />
          )}
        />
      )}

      <ActionSheetModal {...sheet.modalProps} />
    </View>
  );
}
