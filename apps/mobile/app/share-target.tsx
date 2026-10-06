/**
 * Share landing page (RUYI-463) — the in-app half of the Android share
 * intent flow. Reached only via ShareIntentNavigator, so it always has
 * files parked in `useSharedIntentStore`.
 *
 * Three steps, one screen:
 *   workspace → destination (Chat | Issue) → agent (chat only)
 * Confirming writes `setDestination` right before switching workspace and
 * replacing this screen with the destination route; the destination screen
 * takes the files one-shot from the store (chat tab effect / new-issue
 * panel mount effect) and enqueues them into the attachment zone.
 *
 * Dismissal (back out of step 1) unmounts without navigating → cleanup
 * cancels the store and deletes the cache copies (lib/share-discard) —
 * stale files must never leak into a later, unrelated composer.
 */
import { useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Pressable, ScrollView, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { useQuery } from "@tanstack/react-query";
import { canAssignAgentToIssue } from "@multica/core/permissions";
import { Text } from "@/components/ui/text";
import { CardPressable } from "@/components/ui/card";
import { IconButton } from "@/components/ui/icon-button";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { agentListOptions } from "@/data/queries/agents";
import { memberListOptions } from "@/data/queries/members";
import { workspaceListOptions } from "@/data/queries/workspaces";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import {
  useSharedIntentStore,
  type ShareDestination,
} from "@/data/stores/shared-intent-store";
import { isAgentRuntimeBound } from "@/lib/is-agent-runtime-bound";
import { discardSharedFiles } from "@/lib/share-discard";
import { THEME } from "@/lib/theme";
import { useColorScheme } from "@/lib/use-color-scheme";
import { useT } from "@/lib/use-t";

interface SelectedWorkspace {
  id: string;
  slug: string;
  name: string;
  description?: string | null;
}

type Step = "workspace" | "destination" | "agent";

export default function ShareTargetScreen() {
  const { t } = useT("common");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const userId = useAuthStore((s) => s.user?.id ?? null);
  const setCurrentWorkspace = useWorkspaceStore((s) => s.setCurrentWorkspace);
  const files = useSharedIntentStore((s) => s.files);

  const [step, setStep] = useState<Step>("workspace");
  const [workspace, setWorkspace] = useState<SelectedWorkspace | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const navigatedRef = useRef(false);

  // Back out without navigating → the share was dismissed: drop the parked
  // payload and its cache copies so nothing leaks into a later composer.
  useEffect(() => {
    return () => {
      if (navigatedRef.current) return;
      const { files: parked, cancel } = useSharedIntentStore.getState();
      cancel();
      if (parked.length > 0) void discardSharedFiles(parked);
    };
  }, []);

  const {
    data: workspaces,
    isLoading,
    error,
    refetch,
  } = useQuery(workspaceListOptions());

  // The selected workspace may differ from the active one, so the agent list
  // is fetched for the PICKED workspace: the slug rides the request itself
  // (RUYI-463 P1) — the fetch layer's mirror injection stays out of this
  // screen, matching the "never read the current-workspace mirrors here"
  // contract. Members use a scoped URL and never needed the mirror.
  const { data: agents = [] } = useQuery({
    ...agentListOptions(workspace?.id ?? "", {
      workspaceSlug: workspace?.slug,
    }),
    enabled: workspace != null,
  });
  const { data: members = [] } = useQuery({
    ...memberListOptions(workspace?.id ?? ""),
    enabled: workspace != null,
  });

  // Same filter as the chat tab's agent picker (archived out + server invoke
  // gate) plus runtime binding — an unbound agent can't receive the chat.
  const invocableAgents = useMemo(() => {
    if (!workspace) return [];
    const role = members.find((m) => m.user_id === userId)?.role ?? null;
    return agents.filter(
      (a) =>
        !a.archived_at &&
        isAgentRuntimeBound(a) &&
        canAssignAgentToIssue(a, { userId, role }).allowed,
    );
  }, [agents, members, userId, workspace]);

  const stepTitles: Record<Step, string> = {
    workspace: t("mobile.share.select_workspace", "Select a workspace"),
    destination: t("mobile.share.select_destination", "Choose a destination"),
    agent: t("mobile.share.select_agent", "Choose an agent"),
  };

  const goBack = () => {
    if (step === "workspace") {
      router.back(); // unmount cleanup cancels the payload
      return;
    }
    if (step === "agent") {
      setStep("destination");
      return;
    }
    setStep("workspace");
  };

  const navigateTo = async (
    destination: ShareDestination,
    buildRoute: (slug: string) => `/${string}`,
  ) => {
    if (!workspace || submitting) return;
    setSubmitting(true);
    navigatedRef.current = true;
    // Destination first: the chat tab consumes it reactively even while this
    // screen is still on top; the store survives the workspace transition.
    useSharedIntentStore.getState().setDestination(destination);
    try {
      await setCurrentWorkspace(workspace.id, workspace.slug);
      router.replace(buildRoute(workspace.slug));
    } finally {
      setSubmitting(false);
    }
  };

  const confirmChat = (agentId: string) =>
    navigateTo({ kind: "chat", agentId }, (slug) => `/${slug}/chat`);
  const confirmIssue = () =>
    navigateTo({ kind: "issue" }, (slug) => `/${slug}/new-issue`);

  return (
    <SafeAreaView className="flex-1 bg-background">
      <View className="flex-row items-center gap-1 px-2 py-1">
        <IconButton
          name="chevron-back"
          iconSize={22}
          onPress={goBack}
          accessibilityLabel={t("mobile.share.close", "Close")}
        />
        <Text className="text-lg font-semibold text-foreground" numberOfLines={1}>
          {stepTitles[step]}
        </Text>
      </View>

      {files.length > 0 && step !== "workspace" ? (
        <Text className="px-4 pb-1 text-sm text-muted-foreground">
          {t("mobile.share.attached_count", "{{count}} files ready to attach", {
            count: files.length,
          })}
        </Text>
      ) : null}

      <ScrollView
        contentContainerClassName="px-4 py-3 gap-3"
        keyboardShouldPersistTaps="handled"
      >
        {step === "workspace" ? (
          <WorkspaceStep
            workspaces={workspaces}
            isLoading={isLoading}
            error={error}
            onRetry={() => refetch()}
            onPick={(ws) => {
              setWorkspace(ws);
              setStep("destination");
            }}
          />
        ) : null}

        {step === "destination" && workspace ? (
          <View className="gap-3">
            <CardPressable
              onPress={() => setStep("agent")}
              disabled={invocableAgents.length === 0 || submitting}
              className={invocableAgents.length === 0 ? "opacity-50" : undefined}
            >
              <View className="flex-row items-center gap-3">
                <Ionicons name="chatbubbles-outline" size={24} color={theme.primary} />
                <View className="flex-1">
                  <Text className="text-base font-semibold text-foreground">
                    {t("mobile.share.dest_chat", "Chat")}
                  </Text>
                  <Text className="text-xs text-muted-foreground mt-0.5">
                    {invocableAgents.length === 0
                      ? t("mobile.share.no_agent", "No invocable agents in this workspace.")
                      : t("mobile.share.dest_chat_desc", "Send the files to an agent")}
                  </Text>
                </View>
                <Ionicons
                  name="chevron-forward"
                  size={18}
                  color={theme.mutedForeground}
                />
              </View>
            </CardPressable>

            <CardPressable onPress={confirmIssue} disabled={submitting}>
              <View className="flex-row items-center gap-3">
                <Ionicons
                  name="document-text-outline"
                  size={24}
                  color={theme.primary}
                />
                <View className="flex-1">
                  <Text className="text-base font-semibold text-foreground">
                    {t("mobile.share.dest_issue", "Issue")}
                  </Text>
                  <Text className="text-xs text-muted-foreground mt-0.5">
                    {t("mobile.share.dest_issue_desc", "Create a new issue with the files")}
                  </Text>
                </View>
                <Ionicons
                  name="chevron-forward"
                  size={18}
                  color={theme.mutedForeground}
                />
              </View>
            </CardPressable>

            {submitting ? (
              <View className="py-4 items-center">
                <ActivityIndicator />
              </View>
            ) : null}
          </View>
        ) : null}

        {step === "agent" && workspace
          ? invocableAgents.map((agent) => (
              <CardPressable
                key={agent.id}
                onPress={() => confirmChat(agent.id)}
                disabled={submitting}
              >
                <View className="flex-row items-center gap-3">
                  <ActorAvatar type="agent" id={agent.id} size={32} showPresence />
                  <View className="flex-1">
                    <Text
                      className="text-sm font-medium text-foreground"
                      numberOfLines={1}
                    >
                      {agent.name}
                    </Text>
                    {agent.description ? (
                      <Text
                        className="text-xs text-muted-foreground mt-0.5"
                        numberOfLines={1}
                      >
                        {agent.description}
                      </Text>
                    ) : null}
                  </View>
                </View>
              </CardPressable>
            ))
          : null}
      </ScrollView>
    </SafeAreaView>
  );
}

function WorkspaceStep({
  workspaces,
  isLoading,
  error,
  onRetry,
  onPick,
}: {
  workspaces?: SelectedWorkspace[];
  isLoading: boolean;
  error: unknown;
  onRetry: () => void;
  onPick: (ws: SelectedWorkspace) => void;
}) {
  const { t } = useT("common");
  if (isLoading) {
    return (
      <View className="py-8 items-center">
        <ActivityIndicator />
      </View>
    );
  }
  if (error) {
    return (
      <View className="gap-3">
        <Text className="text-sm text-destructive">
          {t("mobile.share.load_failed", "Failed to load workspaces: {{reason}}", {
            reason: error instanceof Error ? error.message : String(error),
          })}
        </Text>
        <Pressable onPress={onRetry} className="py-2">
          <Text className="text-sm text-primary">
            {t("mobile.share.retry", "Retry")}
          </Text>
        </Pressable>
      </View>
    );
  }
  if (!workspaces || workspaces.length === 0) {
    return (
      <Text className="text-sm text-muted-foreground">
        {t(
          "mobile.share.no_workspaces",
          "You don't belong to any workspaces yet.",
        )}
      </Text>
    );
  }
  return (
    <>
      {workspaces.map((ws) => (
        <CardPressable key={ws.id} onPress={() => onPick(ws)}>
          <Text className="text-base font-semibold text-foreground">
            {ws.name}
          </Text>
          <Text className="text-xs text-muted-foreground mt-1">/{ws.slug}</Text>
          {ws.description ? (
            <Text className="text-sm text-muted-foreground mt-2" numberOfLines={2}>
              {ws.description}
            </Text>
          ) : null}
        </CardPressable>
      ))}
    </>
  );
}
