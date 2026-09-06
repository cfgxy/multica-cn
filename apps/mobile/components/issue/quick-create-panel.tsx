/**
 * Smart-mode (agent quick-create) panel — the mobile counterpart of web's
 * AgentCreatePanel (`packages/views/modals/quick-create-issue.tsx`): one
 * prompt + a "Created by" agent/squad pick, server derives title and
 * description, submit goes to POST /api/issues/quick-create.
 *
 * Semantic parity with web (apps/mobile/CLAUDE.md §Behavioral parity):
 *   - Actor visibility: `visibleQuickCreateActors` (same rules as web's
 *     panel filters), seed chain draft → last-actor → first visible agent
 *     (`resolveQuickCreateActor`).
 *   - Daemon CLI version gate: same pure checks as web
 *     (`@multica/core/runtimes/cli-version`), fields gate applies only when
 *     an explicit priority/due date is set.
 *   - Structured submit failures surfaced in-flow: issue_limit_reached /
 *     agent_unavailable / daemon_version_unsupported.
 *   - Shared fields (project / priority / due date) live in the same
 *     new-issue draft store the manual form reads, so picks carry across a
 *     mode switch — web's unified-draft semantics.
 *
 * Deliberate mobile divergences (UI/interaction layer only):
 *   - No keep-open "create another" toggle: the mobile screen closes on
 *     submit like the manual form does.
 *   - The typed prompt is local to this panel — a mode switch carries the
 *     shared fields (project/priority/due) but not the prompt text. Web
 *     copies the prompt into the manual description on switch; mobile v1
 *     doesn't.
 *   - No prompt attachments: the quick-create upload pipeline is a web
 *     editor affordance; mobile v1 submits prompt text only.
 *   - Success closes the screen without a toast (the manual form does the
 *     same); web shows a "sent" toast in its long-lived dialog.
 */
import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useQuery } from "@tanstack/react-query";
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { Stack, router } from "expo-router";
import { ApiError } from "@/data/api";
import {
  checkQuickCreateCliVersion,
  checkQuickCreateFieldsCliVersion,
  readRuntimeCliVersion,
} from "@multica/core/runtimes/cli-version";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { AutosizeTextArea } from "@/components/ui/autosize-textarea";
import { Text } from "@/components/ui/text";
import { SubmitIssueButton } from "@/components/issue/submit-issue-button";
import { QuickCreateAttributeRow } from "@/components/issue/quick-create-attribute-row";
import { runtimeListOptions } from "@/data/queries/runtimes";
import { memberListOptions } from "@/data/queries/members";
import { agentListOptions } from "@/data/queries/agents";
import { squadListOptions } from "@/data/queries/squads";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useQuickCreateIssue } from "@/data/mutations/issues";
import { useNewIssueDraftStore } from "@/data/stores/new-issue-draft-store";
import { useQuickCreatePrefsStore } from "@/data/stores/quick-create-prefs-store";
import {
  buildQuickCreateBody,
  resolveQuickCreateActor,
  visibleQuickCreateActors,
} from "@/lib/quick-create";
import { useActorLookup } from "@/data/use-actor-name";
import { useT } from "@/lib/use-t";

export function QuickCreatePanel() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const userId = useAuthStore((s) => s.user?.id);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: squads = [] } = useQuery(squadListOptions(wsId));
  const { data: runtimes = [] } = useQuery(runtimeListOptions(wsId));

  const memberRole = useMemo(
    () => members.find((m) => m.user_id === userId)?.role ?? null,
    [members, userId],
  );

  const visible = useMemo(
    () => visibleQuickCreateActors(agents, squads, { userId, memberRole }),
    [agents, squads, userId, memberRole],
  );

  const draftActor = useNewIssueDraftStore((s) => s.smartActor);
  const setSmartActor = useNewIssueDraftStore((s) => s.setSmartActor);
  const lastActor = useQuickCreatePrefsStore((s) => s.lastActor);
  const setLastActor = useQuickCreatePrefsStore((s) => s.setLastActor);
  const priority = useNewIssueDraftStore((s) => s.priority);
  const dueDate = useNewIssueDraftStore((s) => s.dueDate);
  const project = useNewIssueDraftStore((s) => s.project);
  // Draft-store reset ownership lives in the new-issue screen shell (once
  // per visit) so a smart↔manual mode flip preserves in-progress picks —
  // web's unified-draft semantics.

  // Re-seed whenever the visible sets resolve or the draft empties — same
  // chain web runs in seedActor + its re-seed effect. Persisting the
  // resolution into the store keeps the actor formSheet route (a sibling
  // Stack screen) in sync with what the panel would submit.
  useEffect(() => {
    const resolved = resolveQuickCreateActor(
      [draftActor, lastActor],
      visible.agents,
      visible.squads,
    );
    if (
      resolved &&
      (draftActor?.type !== resolved.type || draftActor?.id !== resolved.id)
    ) {
      setSmartActor(resolved);
    }
  }, [draftActor, lastActor, visible, setSmartActor]);

  const actorName = useActorLookup();
  const actor = useMemo(
    () => resolveQuickCreateActor([draftActor], visible.agents, visible.squads),
    [draftActor, visible],
  );

  const [prompt, setPrompt] = useState("");

  // Daemon CLI version gate — same pure checks web runs pre-submit (the
  // server re-validates as the trust boundary). The fields gate only
  // applies when the user set an explicit priority or due date, mirroring
  // web's usesExplicitFields switch. A squad pick routes to its leader on
  // the backend, so the leader's runtime gates the squad — same as web's
  // selectedAgent resolution.
  const effectiveAgent = useMemo(() => {
    if (!actor) return undefined;
    if (actor.type === "agent") {
      return visible.agents.find((a) => a.id === actor.id);
    }
    const squad = visible.squads.find((s) => s.id === actor.id);
    if (!squad) return undefined;
    return visible.agents.find((a) => a.id === squad.leader_id);
  }, [actor, visible]);
  const selectedRuntime = useMemo(
    () =>
      effectiveAgent?.runtime_id
        ? runtimes.find((r) => r.id === effectiveAgent.runtime_id)
        : undefined,
    [runtimes, effectiveAgent],
  );
  const runtimeCliVersion = readRuntimeCliVersion(selectedRuntime?.metadata);
  const baseVersionCheck = useMemo(
    () => checkQuickCreateCliVersion(runtimeCliVersion),
    [runtimeCliVersion],
  );
  const fieldsVersionCheck = useMemo(
    () => checkQuickCreateFieldsCliVersion(runtimeCliVersion),
    [runtimeCliVersion],
  );
  const usesExplicitFields = priority !== "none" || dueDate !== null;
  const versionCheck = usesExplicitFields ? fieldsVersionCheck : baseVersionCheck;
  const versionBlocked =
    baseVersionCheck.state !== "ok" ||
    (usesExplicitFields && fieldsVersionCheck.state !== "ok");

  const quickCreate = useQuickCreateIssue();
  const isSubmitting = quickCreate.isPending;

  const { t } = useT("modals");
  const { t: tCommon } = useT("common");

  const canSubmit =
    !isSubmitting &&
    !versionBlocked &&
    prompt.trim().length > 0 &&
    actor !== null;

  const onSubmit = useCallback(async () => {
    if (!actor || versionBlocked) return;
    const trimmed = prompt.trim();
    if (trimmed.length === 0) return;
    try {
      await quickCreate.mutateAsync(
        buildQuickCreateBody({
          actor,
          prompt: trimmed,
          projectId: project?.id ?? null,
          priority,
          dueDate,
        }),
      );
      setLastActor(actor);
      router.back();
    } catch (err) {
      // Structured 4xx bodies mirror web's in-modal error mapping; the
      // server is the authoritative trust boundary for every gate.
      const code =
        err instanceof ApiError &&
        err.body &&
        typeof err.body === "object" &&
        "code" in err.body
          ? String((err.body as { code: unknown }).code)
          : null;
      if (code === "issue_limit_reached") {
        Alert.alert(
          t("create_issue.issue_limit.title", "This workspace has reached its issue limit"),
        );
        return;
      }
      if (code === "agent_unavailable") {
        const reason =
          err instanceof ApiError &&
          err.body &&
          typeof err.body === "object" &&
          "reason" in err.body
            ? String((err.body as { reason: unknown }).reason)
            : null;
        Alert.alert(
          reason ||
            t(
              "create_issue.agent.error_agent_unavailable_fallback",
              "Agent is unavailable. Pick another agent.",
            ),
        );
        return;
      }
      if (code === "daemon_version_unsupported") {
        Alert.alert(
          t("create_issue.agent.error_daemon_version", "This agent's daemon CLI ({{current}}) is below the required {{min}}. Update the daemon to continue.", {
            current: versionCheck.current,
            min: versionCheck.min,
          }),
        );
        return;
      }
      Alert.alert(
        err instanceof Error && err.message
          ? err.message
          : t("create_issue.agent.error_unknown", "Failed to submit. Try again."),
      );
    }
  }, [
    actor,
    versionBlocked,
    prompt,
    project,
    priority,
    dueDate,
    quickCreate,
    setLastActor,
    t,
    versionCheck,
  ]);

  const headerRight = useCallback(
    () => (
      <SubmitIssueButton
        disabled={!canSubmit}
        loading={isSubmitting}
        onPress={onSubmit}
      />
    ),
    [canSubmit, isSubmitting, onSubmit],
  );

  const openActorPicker = useCallback(() => {
    if (!wsSlug) return;
    router.push({
      pathname: "/[workspace]/new-issue-picker/actor",
      params: { workspace: wsSlug },
    });
  }, [wsSlug]);

  return (
    <>
      <Stack.Screen
        options={{
          title: t("create_issue.sr_agent", "Quick create issue"),
          headerRight,
        }}
      />
      <KeyboardAvoidingView
        className="flex-1 bg-background"
        behavior="padding"
      >
        <ScrollView
          className="flex-1"
          contentContainerClassName="px-4 pt-4 pb-6 gap-4"
          keyboardShouldPersistTaps="handled"
        >
          {/* "Created by" row — opens the agent/squad formSheet picker. Same
              list the picker route renders from, so the label always matches
              what a submit would use. */}
          <Pressable
            onPress={openActorPicker}
            className="flex-row items-center gap-2 py-1 active:opacity-60"
            accessibilityLabel={t("create_issue.agent.select_agent_aria", "Select agent")}
          >
            <Text className="text-sm text-muted-foreground">
              {t("create_issue.agent.created_by", "Created by")}
            </Text>
            {actor ? (
              <View className="flex-row items-center gap-1.5">
                <ActorAvatar type={actor.type} id={actor.id} size={20} />
                <Text className="text-sm font-medium text-foreground">
                  {actorName.getName(actor.type, actor.id)}
                </Text>
              </View>
            ) : (
              <Text className="text-sm text-muted-foreground">
                {t("create_issue.agent.pick_an_agent", "Pick an agent…")}
              </Text>
            )}
            <View className="flex-1" />
            <Ionicons
              name="chevron-forward"
              size={16}
              color="#a1a1aa"
            />
          </Pressable>

          {actor && versionBlocked && (
            <View className="rounded-lg border border-warning/30 bg-warning/5 px-3 py-2">
              <Text className="text-xs text-warning">
                {versionCheck.state === "missing"
                  ? t("create_issue.agent.version_missing", "This agent's daemon doesn't report a CLI version. Create with agent needs a daemon running CLI {{min}} or newer.", { min: versionCheck.min })
                  : t("create_issue.agent.version_below", "This agent's daemon CLI is {{current}} — Create with agent needs ≥ {{min}}. Update the daemon to continue.", {
                      current: versionCheck.current,
                      min: versionCheck.min,
                    })}
              </Text>
            </View>
          )}

          <AutosizeTextArea
            value={prompt}
            onChangeText={setPrompt}
            placeholder={t(
              "create_issue.agent.prompt_placeholder",
              'Tell the agent what to do, e.g. "let Bohan fix the inbox loading slowness"',
            )}
            className="text-lg leading-6"
            minHeight={96}
            maxHeight={240}
            editable={!isSubmitting}
            autoFocus
          />

          <QuickCreateAttributeRow />

          <Text className="text-xs text-muted-foreground">
            {tCommon("mobile.create_issue.smart_hint", "The agent drafts the title and description — you'll get an inbox notification when it's done.")}
          </Text>
        </ScrollView>
      </KeyboardAvoidingView>
    </>
  );
}
