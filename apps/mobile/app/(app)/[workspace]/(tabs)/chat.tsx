/**
 * Chat tab root — the session list (RUYI-496 two-level IA).
 *
 * Layout:
 *   View ─ Header(title: "Chats", right: new-chat +)
 *        ─ loading skeleton / error+retry / empty state / session rows
 *        ─ Archived entry footer (below the last row, or the empty state)
 *
 * Previously this tab WAS the chat screen (single-screen IA, auto-hydrated
 * to the most recent session on entry); the chat surface now lives at
 * `app/(app)/[workspace]/chat/[sessionId].tsx` and is reached from here —
 * tap a row for its session, `+` for a blank new chat (multi-agent picks
 * the agent first via AgentPickerSheet, same as the old header flow). The
 * `chat-sessions` formSheet + its picker store retired with the old IA —
 * this list is the session switcher now, and Back from a detail screen
 * lands here with scroll position intact (tab screens stay mounted).
 *
 * Data: `chatSessionsOptions` — the same server-side per-user-filtered
 * session query web/desktop use (AC7: no local assembly, no cross-user
 * cache) — split locally via `splitChatSessions` into the active list and
 * the archived pool (RUYI-533, mirroring web's chat-thread-list.tsx): only
 * active sessions render here, archived ones live in the Archived sub-view
 * pushed from the footer entry, so the two views are exclusive by
 * construction. Rows keep the inbox list's visual anatomy
 * (components/chat/chat-session-row.tsx). Live updates ride the
 * workspace-level `useChatSessionsRealtime` subscription in the workspace
 * layout; nothing list-specific to clean up here.
 *
 * Long-press row actions (rename / pin / archive / delete) reuse the
 * action-sheet pattern from the retired chat-sessions sheet (RUYI-51).
 */
import { useCallback, useMemo, useState } from "react";
import { Alert, FlatList, Pressable, View } from "react-native";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import * as Haptics from "expo-haptics";
import { useQuery } from "@tanstack/react-query";
import type { ChatSession } from "@multica/core/types";
import { canAssignAgentToIssue } from "@multica/core/permissions";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { Header } from "@/components/ui/header";
import { IconButton } from "@/components/ui/icon-button";
import {
  chatSessionsOptions,
  splitChatSessions,
} from "@/data/queries/chat";
import {
  useDeleteChatSession,
  useSetChatSessionArchived,
  useSetChatSessionPinned,
} from "@/data/mutations/chat";
import { agentListOptions } from "@/data/queries/agents";
import { memberListOptions } from "@/data/queries/members";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import {
  useActionSheet,
  ActionSheetModal,
} from "@/components/ui/action-sheet";
import { AgentPickerSheet } from "@/components/chat/agent-picker-sheet";
import { ChatSessionRow } from "@/components/chat/chat-session-row";
import { chatSessionDisplayTitle } from "@/lib/chat-session-title";
import { Skeleton } from "@/components/ui/skeleton";
import { useT } from "@/lib/use-t";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";

export default function ChatTab() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const userId = useAuthStore((s) => s.user?.id);
  const { t } = useT("chat");

  // ── Server state ───────────────────────────────────────────────────────
  const {
    data: rawSessions = [],
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery(chatSessionsOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));

  // RUYI-533: one flat cache, two views — active rows here, archived rows
  // in the sub-view behind the footer entry.
  const { active: sessions, archived: archivedSessions } = useMemo(
    () => splitChatSessions(rawSessions),
    [rawSessions],
  );

  // ── Derived: which agents can this user actually start a chat with? ────
  const memberRole = useMemo(
    () => members.find((m) => m.user_id === userId)?.role ?? null,
    [members, userId],
  );
  const availableAgents = useMemo(
    () =>
      agents.filter(
        (a) =>
          !a.archived_at &&
          canAssignAgentToIssue(a, { userId: userId ?? null, role: memberRole })
            .allowed,
      ),
    [agents, userId, memberRole],
  );

  // ── Navigation ─────────────────────────────────────────────────────────
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

  const openArchived = useCallback(() => {
    if (!wsSlug) return;
    router.push({
      pathname: "/[workspace]/chat/archived",
      params: { workspace: wsSlug },
    });
  }, [wsSlug]);

  const openNewChat = useCallback(
    (agentId?: string) => {
      if (!wsSlug) return;
      router.push({
        pathname: "/[workspace]/chat/[sessionId]",
        params: agentId
          ? { workspace: wsSlug, sessionId: "new", agentId }
          : { workspace: wsSlug, sessionId: "new" },
      });
    },
    [wsSlug],
  );

  const [agentPickerOpen, setAgentPickerOpen] = useState(false);
  const handleNewChat = useCallback(() => {
    if (availableAgents.length > 1) {
      setAgentPickerOpen(true);
      return;
    }
    openNewChat();
  }, [availableAgents.length, openNewChat]);

  const handlePickAgent = useCallback(
    (agent: { id: string }) => {
      setAgentPickerOpen(false);
      openNewChat(agent.id);
    },
    [openNewChat],
  );

  // ── Row actions (long-press) ───────────────────────────────────────────
  const deleteSession = useDeleteChatSession();
  const setPinned = useSetChatSessionPinned();
  const setArchived = useSetChatSessionArchived();
  const sheet = useActionSheet();

  // 不能复用 chat:window.untitled —— 那条 en 是 "New chat"（web 侧指「新建
  // 会话」入口），与这里「无标题会话」的语义不同。
  const untitled = t("mobile.sessions.untitled", "Untitled chat");

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
      const archived = session.status === "archived";
      Haptics.selectionAsync().catch(() => {});

      const actions: (
        | { kind: "rename" }
        | { kind: "pin" }
        | { kind: "archive" }
        | { kind: "delete" }
        | { kind: "cancel" }
      )[] = [];
      const options: string[] = [];
      const push = (label: string, action: (typeof actions)[number]) => {
        options.push(label);
        actions.push(action);
      };

      push(t("header.rename", "Rename chat"), { kind: "rename" });
      push(session.pinned ? t("list.unpin", "Unpin") : t("list.pin", "Pin"), {
        kind: "pin",
      });
      push(
        archived
          ? t("header.unarchive", "Unarchive chat")
          : t("header.archive", "Archive chat"),
        { kind: "archive" },
      );
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
            case "rename":
              if (wsSlug) {
                router.push({
                  pathname: "/[workspace]/chat-rename",
                  params: { workspace: wsSlug, sessionId: session.id },
                });
              }
              return;
            case "pin":
              setPinned.mutate({
                sessionId: session.id,
                pinned: !session.pinned,
              });
              return;
            case "archive":
              setArchived.mutate({
                sessionId: session.id,
                archived: !archived,
              });
              return;
            case "delete":
              confirmDelete(session);
              return;
          }
        },
      });
    },
    [confirmDelete, setArchived, setPinned, sheet, t, untitled, wsSlug],
  );

  // ── Render ─────────────────────────────────────────────────────────────
  const untitledFallback = untitled;

  return (
    <View className="flex-1 bg-background">
      <Header
        title={t("mobile.sessions.title", "Chats")}
        right={
          <IconButton
            name="add"
            iconSize={24}
            onPress={handleNewChat}
            accessibilityLabel={t("window.new_chat_tooltip", "New chat")}
          />
        }
      />
      {isLoading ? (
        // Skeleton rows mirror ChatSessionRow's anatomy (36px avatar + two
        // text lines), same layout the inbox loading state uses.
        <View testID="chat-list-loading" className="px-4 pt-4 gap-4">
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
        // RUYI-533: the Archived entry stays reachable from the empty state —
        // exactly when a user goes looking for what they filed away (same
        // reasoning as the inbox entry, RUYI-532).
        <View className="flex-1">
          <View className="flex-1 items-center justify-center gap-3 px-6">
            <Text className="text-sm text-muted-foreground text-center">
              {t("mobile.sessions.empty", "No chats yet.")}
            </Text>
            <Button variant="outline" onPress={handleNewChat}>
              <Text>{t("window.new_chat_tooltip", "New chat")}</Text>
            </Button>
          </View>
          {archivedSessions.length > 0 ? (
            <ArchivedEntry
              count={archivedSessions.length}
              onPress={openArchived}
            />
          ) : null}
        </View>
      ) : (
        <FlatList
          data={sessions}
          keyExtractor={(session) => session.id}
          showsVerticalScrollIndicator={false}
          ItemSeparatorComponent={() => (
            <View className="h-px bg-border ml-16" />
          )}
          ListFooterComponent={
            archivedSessions.length > 0 ? (
              <ArchivedEntry
                count={archivedSessions.length}
                onPress={openArchived}
              />
            ) : null
          }
          contentContainerClassName="pb-6"
          renderItem={({ item: session }) => (
            <ChatSessionRow
              session={session}
              untitled={untitledFallback}
              onPress={() => openSession(session.id)}
              onLongPress={() => showSessionActions(session)}
            />
          )}
        />
      )}

      <AgentPickerSheet
        visible={agentPickerOpen}
        agents={availableAgents}
        currentAgentId={null}
        onPick={handlePickAgent}
        onClose={() => setAgentPickerOpen(false)}
      />

      <ActionSheetModal {...sheet.modalProps} />
    </View>
  );
}

// The entry into the Archived chats sub-view (RUYI-533) — same footer
// pattern as the archived-inbox entry (RUYI-532), which mirrors web's
// footer in inbox-list.tsx / chat-thread-list.tsx: archive glyph,
// localized title, count, chevron.
function ArchivedEntry({
  count,
  onPress,
}: {
  count: number;
  onPress: () => void;
}) {
  const { t } = useT("chat");
  const { colorScheme } = useColorScheme();
  const muted = THEME[colorScheme].mutedForeground;
  return (
    <Pressable
      testID="chat-archived-entry"
      onPress={onPress}
      accessibilityRole="button"
      accessibilityLabel={t("list.archived_title", "Archived")}
      className="flex-row items-center gap-3 px-4 py-3 border-t border-border active:bg-secondary"
    >
      <Ionicons name="archive-outline" size={20} color={muted} />
      <Text className="flex-1 text-sm font-medium text-muted-foreground">
        {t("list.archived_title", "Archived")}
      </Text>
      <Text className="text-sm text-muted-foreground tabular-nums">
        {count}
      </Text>
      <Ionicons name="chevron-forward" size={16} color={muted} />
    </Pressable>
  );
}
