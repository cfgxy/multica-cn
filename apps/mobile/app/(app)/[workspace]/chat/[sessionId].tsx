/**
 * Chat session detail — `/[workspace]/chat/[sessionId]` (RUYI-496).
 *
 * This is the chat screen that used to BE the Chat tab (single-screen IA).
 * RUYI-496 moved it off the tab root into a workspace-stack push screen so
 * the tab root is the session list; the message stream, composer, agent
 * session handling and header actions are carried over unchanged. Pushed
 * screens get the native iOS back button + swipe-to-dismiss for free, so
 * Back lands on the list with its scroll state intact (AC5) — same
 * navigation shape as issue/[id] (AC8).
 *
 * Route param: `sessionId` — a real session id, or the literal "new" for a
 * blank new chat (session ids are UUIDs, so the literal can't collide).
 * `?agentId=` preselects the agent for a new chat (list `+` flow,
 * RUYI-418 agent-request handoff).
 *
 * Differences from the old tab screen, all IA-driven:
 *   - No auto-hydrate: the list is the entry point, so nothing jumps to
 *     the most recent session on mount (AC1 lives in the list screen).
 *   - `activeSessionId` seeds from the route param instead of local
 *     state; after ensureSession creates one for a "new" chat the screen
 *     switches in place (draft already promoted), keeping the composer
 *     burst exactly as before.
 *   - The chat-sessions formSheet + picker store retired: switching
 *     sessions = Back → tap another row. The header title is display-only.
 *   - Session-deleted (via ⋯ menu or WS) routes back to the list instead
 *     of blanking in place.
 *   - The share-intent (RUYI-463) take and the agent-request (RUYI-418)
 *     consume move here — their entry points now land on "new".
 */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, View } from "react-native";
// RN 0.83 edge-to-edge 下 Android 的窗口 resize 失效，避让统一走
// keyboard-controller（behavior="padding" 两端一致），见 RUYI-30。
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { router, useLocalSearchParams } from "expo-router";
import { useFocusEffect, useIsFocused } from "@react-navigation/native";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { Agent, ChatMessage, ChatPendingTask } from "@multica/core/types";
import {
  enqueuePendingChatTask,
  hideQueuedChatMessages,
  removePendingChatTask,
} from "@multica/core/chat/pending";
import { canAssignAgentToIssue } from "@multica/core/permissions";
import { api } from "@/data/api";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { agentListOptions } from "@/data/queries/agents";
import { memberListOptions } from "@/data/queries/members";
import {
  chatKeys,
  chatMessagesOptions,
  chatSessionsOptions,
  pendingChatTaskOptions,
  taskMessagesOptions,
} from "@/data/queries/chat";
import {
  useCreateChatSession,
  useDeleteChatSession,
  useMarkChatSessionRead,
  useSetChatSessionArchived,
  useSetChatSessionPinned,
} from "@/data/mutations/chat";
import {
  DRAFT_NEW_SESSION,
  useChatDraftsStore,
} from "@/data/stores/chat-drafts-store";
import { useChatAgentRequestStore } from "@/data/stores/chat-agent-request-store";
import { useSharedIntentStore } from "@/data/stores/shared-intent-store";
import type { SharedFile } from "@/lib/share-payload";
import { useChatSessionRealtime } from "@/data/realtime/use-chat-session-realtime";
import {
  invalidatePendingTask,
  seedAcceptedPendingTask,
} from "@/data/realtime/chat-ws-updaters";
import { useWorkspaceAgentAvailability } from "@/lib/workspace-agent-availability";
import { sendFailureMessage } from "@/lib/dispatch-reason";
import { useAgentPresence } from "@/lib/use-agent-presence";
import { Header } from "@/components/ui/header";
import { ChatTitleButton } from "@/components/chat/chat-title-button";
import { ChatSessionActions } from "@/components/chat/chat-session-actions";
import { ChatMessageList } from "@/components/chat/chat-message-list";
import { ChatComposer } from "@/components/chat/chat-composer";
import { AgentPickerSheet } from "@/components/chat/agent-picker-sheet";
import { VoiceSessionOverlay } from "@/components/voice/voice-session-overlay";
import { NoAgentBanner } from "@/components/chat/no-agent-banner";
import { OfflineBanner } from "@/components/chat/offline-banner";
import { RuntimeRequiredBanner } from "@/components/chat/runtime-required-banner";
import { useChatSelectStore } from "@/data/chat-select-store";
import { isAgentRuntimeBound } from "@/lib/is-agent-runtime-bound";
import { useT } from "@/lib/use-t";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { IconButton } from "@/components/ui/icon-button";
import { chatSessionDisplayTitle } from "@/lib/chat-session-title";

/** Literal route param for a blank new chat (session ids are UUIDs). */
export const NEW_CHAT_SESSION_ID = "new";

export default function ChatDetailScreen() {
  const qc = useQueryClient();
  const { t } = useT("chat");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const params = useLocalSearchParams<{
    sessionId: string;
    agentId?: string;
  }>();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const userId = useAuthStore((s) => s.user?.id);

  const isNew = params.sessionId === NEW_CHAT_SESSION_ID;
  const [activeSessionId, setActiveSessionId] = useState<string | null>(
    isNew ? null : params.sessionId,
  );
  const [selectedAgentId, setSelectedAgentId] = useState<string | null>(
    params.agentId ?? null,
  );
  const [agentPickerOpen, setAgentPickerOpen] = useState(false);
  // RUYI-449: 非空时经 VoiceSessionOverlay 对当前 agent 发起语音会话
  // （425 链路）；关闭即结束，不影响文本发送。
  const [voiceOpen, setVoiceOpen] = useState(false);

  // ── Server state ───────────────────────────────────────────────────────
  const { data: sessions = [] } = useQuery(chatSessionsOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));

  const { data: messages = [], isLoading: messagesLoading } = useQuery(
    chatMessagesOptions(activeSessionId),
  );
  const { data: pendingTask } = useQuery(
    pendingChatTaskOptions(activeSessionId),
  );
  const visibleMessages = hideQueuedChatMessages(messages, pendingTask);
  // Live execution trace for the in-flight task. `task:message` WS events
  // append rows to this same cache key via `appendTaskMessage`, so the
  // list/pill stay in sync without a polling fetch. `enabled` is gated by
  // `isTaskMessageTaskId` inside taskMessagesOptions — optimistic ids
  // never hit the network.
  const { data: liveTaskMessages = [] } = useQuery(
    taskMessagesOptions(pendingTask?.task_id),
  );

  // ── Derived ────────────────────────────────────────────────────────────
  const memberRole = useMemo(
    () => members.find((m) => m.user_id === userId)?.role ?? null,
    [members, userId],
  );

  // The picker must list only agents this user can actually TRIGGER — sending
  // a message enqueues a run, so it clears the server's invoke gate
  // (`canInvokeAgent`), which has no admin bypass. Shared rule, not a mobile
  // copy: a local mirror drifted from it and let admins pick a teammate's
  // personal agent only to be 403'd on send (MUL-6380 / GH #7180).
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

  const activeSession = useMemo(
    () => sessions.find((s) => s.id === activeSessionId) ?? null,
    [sessions, activeSessionId],
  );

  // Active agent: explicit selection wins; otherwise inherit from the
  // active session; otherwise pick the first available agent.
  const currentAgent: Agent | null = useMemo(() => {
    if (selectedAgentId) {
      return availableAgents.find((a) => a.id === selectedAgentId) ?? null;
    }
    if (activeSession) {
      return agents.find((a) => a.id === activeSession.agent_id) ?? null;
    }
    return availableAgents[0] ?? null;
  }, [selectedAgentId, availableAgents, activeSession, agents]);

  // A session outlives the permission that created it: the agent can be flipped
  // to personal, change owner, or drop this member from its allow-list, and the
  // server then refuses every send with `invocation_not_allowed` while still
  // serving the transcript (MUL-4525 — read uses the view gate, send re-runs the
  // invoke gate). `currentAgent` deliberately resolves an open session's agent
  // from the FULL list so the header stays honest, which means the picker filter
  // above cannot cover this case — judge the bound agent too (MUL-6380).
  const accessRevoked =
    currentAgent !== null &&
    !canAssignAgentToIssue(currentAgent, {
      userId: userId ?? null,
      role: memberRole,
    }).allowed;

  const availability = useWorkspaceAgentAvailability();
  const presenceDetail = useAgentPresence(wsId, currentAgent?.id);
  const presenceAvailability =
    presenceDetail === "loading" ? undefined : presenceDetail.availability;
  const isArchived = activeSession?.status === "archived";
  const runtimeBound =
    currentAgent !== null && isAgentRuntimeBound(currentAgent);
  const sending = !!pendingTask?.task_id;

  // ── Drafts ─────────────────────────────────────────────────────────────
  const draftKey = activeSessionId ?? DRAFT_NEW_SESSION;
  const draft = useChatDraftsStore((s) => s.drafts[draftKey] ?? "");
  const setDraft = useChatDraftsStore((s) => s.setDraft);
  const clearDraft = useChatDraftsStore((s) => s.clearDraft);
  const promoteNewDraft = useChatDraftsStore((s) => s.promoteNewDraft);

  // ── Realtime ───────────────────────────────────────────────────────────
  // Session deleted elsewhere (another device, workspace cleanup): there is
  // no in-place blank state anymore — unwind to the list (AC5).
  useChatSessionRealtime(activeSessionId, () => {
    router.back();
  });

  // Exit text-selection mode whenever this screen loses focus.
  useFocusEffect(
    useCallback(() => () => useChatSelectStore.getState().clear(), []),
  );

  // ── Auto markRead while viewing a session with unread state ──────────
  const isFocused = useIsFocused();
  const markRead = useMarkChatSessionRead();
  useEffect(() => {
    if (!isFocused) return;
    if (!activeSessionId) return;
    if (!activeSession?.has_unread) return;
    markRead.mutate(activeSessionId);
  }, [isFocused, activeSessionId, activeSession?.has_unread, markRead]);

  // ── Mutations ──────────────────────────────────────────────────────────
  const createSession = useCreateChatSession();
  const deleteSession = useDeleteChatSession();
  const setSessionPinned = useSetChatSessionPinned();
  const setSessionArchived = useSetChatSessionArchived();

  // ── Send burst ─────────────────────────────────────────────────────────
  const sessionPromiseRef = useRef<Promise<string | null> | null>(null);

  const ensureSession = useCallback(
    async (titleSeed: string): Promise<string | null> => {
      if (activeSessionId) return activeSessionId;
      if (!currentAgent) return null;
      if (sessionPromiseRef.current) return sessionPromiseRef.current;

      const promise = (async () => {
        try {
          const session = await createSession.mutateAsync({
            agent_id: currentAgent.id,
            title: titleSeed.slice(0, 50),
          });
          return session.id;
        } finally {
          sessionPromiseRef.current = null;
        }
      })();
      sessionPromiseRef.current = promise;
      return promise;
    },
    [activeSessionId, currentAgent, createSession],
  );

  const handleSend = useCallback(
    async (
      content: string,
      attachmentIds: string[] = [],
      options: { clearDraft?: boolean } = {},
    ) => {
      if (!currentAgent) return;
      // Invoke permission was revoked while this session was open — the server
      // would refuse before persisting anything. The composer is disabled in
      // this state; this is the belt-and-braces guard.
      if (accessRevoked) {
        Alert.alert(
          t(
            "mobile.send_blocked.no_permission_title",
            "No permission to run this agent",
          ),
          t(
            "mobile.send_blocked.no_permission_message",
            "You no longer have permission to run this agent, so the message was not sent. Ask its owner for access.",
          ),
        );
        return;
      }
      if (!runtimeBound) {
        Alert.alert(
          t("mobile.send_blocked.runtime_required_title", "Runtime required"),
          t(
            "mobile.send_blocked.runtime_required_message",
            "Bind a runtime to this agent on web or desktop before sending a message.",
          ),
        );
        return;
      }

      const isNewSession = !activeSessionId;
      let sessionId: string | null;
      try {
        sessionId = await ensureSession(content);
      } catch (err) {
        // Session create runs the same invoke gate as a send, so a permission
        // change refuses here too — and this is the only layer that sees the
        // reason code (MUL-6380).
        Alert.alert(
          t("mobile.send_blocked.failed_title", "Message not sent"),
          sendFailureMessage(err),
        );
        throw err;
      }
      if (!sessionId) return;

      const sentAt = new Date().toISOString();
      const optimistic: ChatMessage = {
        id: `optimistic-${Date.now()}`,
        chat_session_id: sessionId,
        role: "user",
        content,
        task_id: null,
        created_at: sentAt,
      };
      const optimisticTaskId = `optimistic-${optimistic.id}`;
      qc.setQueryData<ChatMessage[]>(chatKeys.messages(sessionId), (old) =>
        old ? [...old, optimistic] : [optimistic],
      );
      qc.setQueryData<ChatPendingTask>(chatKeys.pendingTask(sessionId), (old) =>
        enqueuePendingChatTask(
          old,
          {
            task_id: optimisticTaskId,
            status: "queued",
            created_at: sentAt,
            message_id: optimistic.id,
            content,
          },
          Boolean(old?.task_id),
        ),
      );
      if (isNewSession) {
        promoteNewDraft(sessionId);
        setActiveSessionId(sessionId);
      }

      try {
        const result = await api.sendChatMessage(sessionId, content, {
          attachmentIds: attachmentIds.length > 0 ? attachmentIds : undefined,
        });
        // Replace the local bubble before reconciling pending state. When the
        // server says this is a follow-up, its real message id lets the shared
        // queue filter hide it immediately instead of waiting for the refetch.
        qc.setQueryData<ChatMessage[]>(chatKeys.messages(sessionId), (old) =>
          old?.map((message) =>
            message.id === optimistic.id
              ? {
                  ...message,
                  id: result.message_id,
                  task_id: result.task_id,
                  created_at: result.created_at,
                }
              : message,
          ),
        );
        seedAcceptedPendingTask(qc, {
          chat_session_id: sessionId,
          task_id: result.task_id,
          created_at: result.created_at,
          message_id: result.message_id,
          content,
          optimistic_task_id: optimisticTaskId,
          supports_queue: result.supports_queue,
          queued: result.queued,
        });
        qc.invalidateQueries({ queryKey: chatKeys.messages(sessionId) });
        if (options.clearDraft !== false) {
          clearDraft(sessionId);
        }
      } catch (err) {
        qc.setQueryData<ChatMessage[]>(chatKeys.messages(sessionId), (old) =>
          old ? old.filter((m) => m.id !== optimistic.id) : old,
        );
        qc.setQueryData<ChatPendingTask>(
          chatKeys.pendingTask(sessionId),
          (old) => removePendingChatTask(old, optimisticTaskId),
        );
        // The composer restores the draft on a thrown rejection but says nothing
        // about it, so a revoked-permission 403 used to read as a silent no-op
        // (MUL-6380). Name the cause here: only this layer sees the error body.
        Alert.alert(
          t("mobile.send_blocked.failed_title", "Message not sent"),
          sendFailureMessage(err),
        );
        throw err;
      }
    },
    [
      activeSessionId,
      currentAgent,
      accessRevoked,
      runtimeBound,
      ensureSession,
      qc,
      promoteNewDraft,
      clearDraft,
      t,
    ],
  );

  // ── Cancel in-flight ───────────────────────────────────────────────────
  const handleStop = useCallback(() => {
    if (!pendingTask?.task_id || !activeSessionId) return;
    if (pendingTask.status === "queued") return;
    const taskId = pendingTask.task_id;
    const sessionId = activeSessionId;
    qc.setQueryData<ChatPendingTask>(chatKeys.pendingTask(sessionId), (old) =>
      removePendingChatTask(old, taskId),
    );
    void api
      .cancelTaskById(taskId)
      .catch(() => {
        // Silent — task may have already terminated server-side.
      })
      .finally(() => invalidatePendingTask(qc, sessionId));
  }, [pendingTask?.task_id, pendingTask?.status, activeSessionId, qc]);

  // ── New-chat agent selection (list "+" multi-agent flow) ──────────────
  const handleNewChat = useCallback(() => {
    if (availableAgents.length > 1) {
      setAgentPickerOpen(true);
      return;
    }
    setSelectedAgentId(null);
    setActiveSessionId(null);
  }, [availableAgents.length]);

  const handlePickAgent = useCallback((agent: Agent) => {
    setSelectedAgentId(agent.id);
    setActiveSessionId(null);
  }, []);

  // Same one-shot channel as before (RUYI-418 A7): "DM this agent" on the
  // agent detail screen lands here via /chat/new. The sender gates the
  // invocation permission, so the request is applied as-is; if the agent
  // isn't invocable the composer falls back to the no-agent banner.
  const agentRequest = useChatAgentRequestStore((s) => s.agentRequest);
  const consumeAgent = useChatAgentRequestStore((s) => s.consumeAgent);
  useEffect(() => {
    if (!agentRequest) return;
    setSelectedAgentId(agentRequest.id);
    setActiveSessionId(null);
    consumeAgent();
  }, [agentRequest, consumeAgent]);

  // RUYI-463: 系统分享 → Chat。落地页已选好 agent 并把
  // {files, destination} 写进 shared-intent-store；这里 one-shot take
  // 后切到该 agent 的新会话并把文件交给 composer 入队。
  //
  // 必须用 useFocusEffect 而非订阅 effect：落地页是 router.replace
  // 进来的，栈上可能同时存在新旧两个屏实例（旧的被压在栈下但仍然
  // mounted、仍然订阅着 store）。订阅 effect 会让旧实例抢走 one-shot
  // payload，文件注入进用户看不见的那个实例；焦点语义保证只有用户
  // 看得见的屏执行 take。takeFor 本身幂等（take 后置空），重复 focus
  // 不会重复注入。
  const [incomingSharedFiles, setIncomingSharedFiles] = useState<SharedFile[]>(
    [],
  );
  useFocusEffect(
    useCallback(() => {
      const taken = useSharedIntentStore.getState().takeFor("chat");
      if (!taken) return;
      setSelectedAgentId(taken.destination.agentId);
      setActiveSessionId(null);
      setIncomingSharedFiles(taken.files);
    }, []),
  );

  const handleDeleteActive = useCallback(() => {
    if (!activeSession) return;
    Alert.alert(
      t("session_history.delete_dialog.title", "Delete chat session"),
      chatSessionDisplayTitle(
        activeSession.title,
        t("session_history.untitled", "Untitled"),
      ),
      [
        {
          text: t("session_history.delete_dialog.cancel", "Cancel"),
          style: "cancel",
        },
        {
          text: t("session_history.delete_dialog.confirm", "Delete"),
          style: "destructive",
          onPress: () => {
            const id = activeSession.id;
            deleteSession.mutate(id);
            // Back to the list first — the invalidated sessions cache
            // drops the row while this screen unwinds (AC5).
            router.back();
          },
        },
      ],
      { cancelable: true },
    );
  }, [activeSession, deleteSession, t]);

  // ── Session-menu actions (RUYI-51) ─────────────────────────────────────
  // Rename opens a self-contained formSheet route (text input → form sheet
  // per apps/mobile CLAUDE.md Lesson 5); pin/archive mutate optimistically
  // and stay on the open session. Mobile divergence from web, documented:
  // web's header menu links to an agent profile page — mobile has no agent
  // detail screen yet (more/agents.tsx is a placeholder), so no "view
  // profile" item.
  const handleRenameActive = useCallback(() => {
    if (!activeSession || !wsSlug) return;
    router.push({
      pathname: "/[workspace]/chat-rename",
      params: { workspace: wsSlug, sessionId: activeSession.id },
    });
  }, [activeSession, wsSlug]);

  const handleTogglePinActive = useCallback(() => {
    if (!activeSession) return;
    setSessionPinned.mutate({
      sessionId: activeSession.id,
      pinned: !activeSession.pinned,
    });
  }, [activeSession, setSessionPinned]);

  const handleToggleArchiveActive = useCallback(() => {
    if (!activeSession) return;
    // Keep the archived session open: the composer already renders its
    // archived disabled state (isArchived → disabledReason), so the user
    // sees the result in place and can switch via the list.
    setSessionArchived.mutate({
      sessionId: activeSession.id,
      archived: !isArchived,
    });
  }, [activeSession, isArchived, setSessionArchived]);

  // ── Composer disabled-state ────────────────────────────────────────────
  const disabled =
    !currentAgent ||
    accessRevoked ||
    availability === "none" ||
    isArchived === true ||
    !runtimeBound;
  // 与上面 `disabled` 同集合、同顺序、同判据，无 undefined 出口：
  // `disabled === true` ⟺ `disabledReason !== undefined`。这五条是禁用态
  // 下 composer pill 上唯一实际可见的文案（覆盖掉 pillLabel）。
  const disabledReason = !currentAgent
    ? t("mobile.disabled_reason.no_agent", "No agent selected")
    : accessRevoked
      ? t(
          "mobile.disabled_reason.access_revoked",
          "You can no longer run this agent",
        )
      : availability === "none"
        ? t(
            "mobile.disabled_reason.no_agents_in_workspace",
            "No agents in this workspace",
          )
        : isArchived
          ? t("mobile.disabled_reason.archived", "This chat is archived")
          : !runtimeBound
            ? t(
                "mobile.disabled_reason.runtime_missing",
                "Agent needs a runtime",
              )
            : undefined;

  return (
    <View className="flex-1 bg-background">
      {/* Same header row as the old chat tab screen (zero visual drift),
          plus a back button — the screen is now a workspace-stack push, so
          Back / iOS swipe land on the session list (AC5). The title is
          display-only: switching sessions happens through the list. */}
      <Header
        left={
          <IconButton
            name="chevron-back"
            iconSize={26}
            onPress={() => router.back()}
            accessibilityLabel={t("common:mobile.common.back", "Back")}
          />
        }
        center={
          <ChatTitleButton
            currentSession={activeSession}
            currentAgent={currentAgent}
          />
        }
        right={
          <ChatSessionActions
            showNew={false}
            showMore={!!activeSession}
            isArchived={isArchived}
            isPinned={activeSession?.pinned === true}
            onRename={handleRenameActive}
            onTogglePin={handleTogglePinActive}
            onToggleArchive={handleToggleArchiveActive}
            onDelete={handleDeleteActive}
            onNewPress={handleNewChat}
          />
        }
      />
      {availability === "none" ? <NoAgentBanner /> : null}
      <KeyboardAvoidingView behavior="padding" className="flex-1">
        <ChatMessageList
          messages={visibleMessages}
          loading={messagesLoading}
          hasSessions={sessions.length > 0}
          agent={currentAgent}
          onPickPrompt={(text) => setDraft(draftKey, text)}
          onQuickAction={(action) =>
            handleSend(action.prompt, [], { clearDraft: false })
          }
          quickActionsDisabled={sending || disabled}
          pendingTask={pendingTask}
          liveTaskMessages={liveTaskMessages}
          availability={presenceAvailability}
        />
        {runtimeBound ? (
          <OfflineBanner
            agentName={currentAgent?.name}
            availability={presenceAvailability}
          />
        ) : currentAgent ? (
          <RuntimeRequiredBanner agentName={currentAgent.name} />
        ) : null}
        <ChatComposer
          value={draft}
          onChangeText={(next) => setDraft(draftKey, next)}
          onSend={handleSend}
          onStop={handleStop}
          sending={sending}
          allowStop={pendingTask?.status !== "queued"}
          disabled={disabled}
          disabledReason={disabledReason}
          incomingSharedFiles={incomingSharedFiles}
          onIncomingSharedFilesConsumed={() => setIncomingSharedFiles([])}
          renderVoiceWhenEmpty={
            runtimeBound && currentAgent !== null && !voiceOpen
              ? () => (
                  <IconButton
                    name="mic-outline"
                    iconSize={18}
                    color={theme.primaryForeground}
                    variant="default"
                    onPress={() => setVoiceOpen(true)}
                    hitSlop={12}
                    className="h-8 w-8 rounded-full"
                    accessibilityLabel={t("voice:button.start", "Start voice conversation")}
                  />
                )
              : undefined
          }
        />
      </KeyboardAvoidingView>

      <AgentPickerSheet
        visible={agentPickerOpen}
        agents={availableAgents}
        currentAgentId={currentAgent?.id ?? null}
        onPick={handlePickAgent}
        onClose={() => setAgentPickerOpen(false)}
      />

      <VoiceSessionOverlay
        agentId={voiceOpen && currentAgent !== null ? currentAgent.id : null}
        workspaceSlug={wsSlug ?? ""}
        onClose={() => setVoiceOpen(false)}
      />
    </View>
  );
}
