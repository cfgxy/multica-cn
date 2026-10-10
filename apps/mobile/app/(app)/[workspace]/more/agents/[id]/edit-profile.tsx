/**
 * Edit agent profile (`more/agents/[id]/edit-profile`, RUYI-346 A2 /
 * RUYI-418 B2) — formSheet per SHEET_OPTIONS, re-scoped in RUYI-624 to the
 * profile fields only: avatar / name / description plus the mobile-only
 * voice slot (RUYI-425 §4.2). Mirrors web's profile section PUT
 * (UpdateAgentRequest name/description/avatar).
 *
 * RUYI-624 split: instructions moved to the dedicated edit-instructions
 * window (same RUYI-541 pattern as squads) and the execution fields
 * (runtime / model / thinking / tier / concurrency / session context /
 * subagents) moved to run-config — matching web's section split and giving
 * the detail page's facts-card rows a focused editor. This form used to
 * carry everything; the half-height sheet opened on the instructions
 * textarea, which owners read as "every entry opens the same instruction
 * box".
 *
 * The patch is built by lib/agent-profile-patch's buildProfilePatch with
 * the execution fields pinned to the saved Agent — only changed fields land
 * in the PUT, so the split editors can never clobber each other's fields.
 *
 * The voice patch is tri-state — omitted when untouched, "" to unbind, an
 * id to bind — and the pickers exclude each other's selection
 * (`agentSlotChoices`); the voice picker reads the saved text-runtime
 * binding for the exclusion, so a run-config edit the server hasn't echoed
 * back yet may briefly offer the just-taken runtime. Self-healing on the
 * next fetch, same as the pre-split form.
 *
 * Access scope is intentionally absent — on web it lives in the separate
 * set-access dialog and is owner-only; mobile keeps that split (S5 screen).
 */
import { useEffect, useState } from "react";
import { Alert, Pressable, ScrollView, TextInput, View } from "react-native";
// RN 0.83 edge-to-edge 下 Android 的窗口 resize 失效，避让统一走
// keyboard-controller（behavior="padding" 两端一致），见 RUYI-30。
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { router, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { agentSlotChoices, runtimeCredentialStatus } from "@multica/core/runtimes";
import {
  AGENT_SESSION_COMPACT_PCT_DEFAULT,
  AGENT_SESSION_MAX_CONTEXT_TOKENS_DEFAULT,
} from "@multica/core/agents/constants";
import { allowsSubagents } from "@multica/core/agents/subagent-tools";
import { Text } from "@/components/ui/text";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { agentDetailOptions } from "@/data/queries/agents";
import { runtimeListOptions } from "@/data/queries/runtimes";
import { useUpdateAgent } from "@/data/mutations/agents";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { ActionSheetModal } from "@/components/ui/action-sheet";
import { useAvatarUploader } from "@/lib/avatar";
import { resolveAttachmentUrl } from "@/lib/attachment-url";
import { useT } from "@/lib/use-t";
import { buildProfilePatch } from "@/lib/agent-profile-patch";

export default function EditAgentProfile() {
  const insets = useSafeAreaInsets();
  const { id } = useLocalSearchParams<{ id: string }>();
  const agentId = typeof id === "string" ? id : "";
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const me = useAuthStore((s) => s.user);
  const { t } = useT("agents");
  const update = useUpdateAgent(agentId);
  const { uploading, showAvatarSheet, modalProps } = useAvatarUploader();

  const { data: agent } = useQuery(agentDetailOptions(wsId, agentId));
  const { data: runtimes, isLoading: runtimesLoading } = useQuery(
    runtimeListOptions(wsId),
  );

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [avatarUrl, setAvatarUrl] = useState("");
  const [voiceRuntimeId, setVoiceRuntimeId] = useState("");
  const [seeded, setSeeded] = useState(false);

  useEffect(() => {
    if (!agent || !agent.id || seeded) return;
    setName(agent.name);
    setDescription(agent.description);
    setAvatarUrl(agent.avatar_url ?? "");
    setVoiceRuntimeId(agent.voice_runtime_id ?? "");
    setSeeded(true);
  }, [agent, seeded]);

  // Voice slot choices: realtime-voice-capable, minus the agent's saved text
  // slot binding, plus the current voice binding even when offline.
  const voiceChoices = (() => {
    if (!agent) return [];
    const usable = agentSlotChoices(runtimes, "realtime_voice", {
      currentUserId: me?.id ?? null,
      excludeRuntimeId: agent.runtime_id,
      keepRuntimeId: voiceRuntimeId,
    });
    if (voiceRuntimeId && !usable.some((r) => r.id === voiceRuntimeId)) {
      const current = (runtimes ?? []).find((r) => r.id === voiceRuntimeId);
      if (current) return [current, ...usable];
    }
    return usable;
  })();

  // The patch carries only fields whose value actually changed; the
  // execution fields are pinned to the saved Agent so buildProfilePatch
  // never emits them (single-sourced PUT semantics shared with the split
  // editors). Untouched here means "as fetched" — this form saves in one
  // shot and navigates back.
  const patch = agent?.id
    ? buildProfilePatch(agent, {
        name,
        description,
        instructions: agent.instructions,
        avatarUrl,
        model: agent.model,
        runtimeId: agent.runtime_id,
        voiceRuntimeId,
        thinking: agent.thinking_level ?? "",
        tier: agent.service_tier ?? "",
        concurrency: String(agent.max_concurrent_tasks || 1),
        maxContext: String(
          agent.session_max_context_tokens ??
            AGENT_SESSION_MAX_CONTEXT_TOKENS_DEFAULT,
        ),
        compactPct: String(
          agent.session_compact_pct ?? AGENT_SESSION_COMPACT_PCT_DEFAULT,
        ),
        subagents: allowsSubagents(agent.runtime_config),
      })
    : null;
  const dirty = !!patch && Object.keys(patch).length > 0;
  const canSave = !!patch && dirty && !update.isPending;

  const onSave = () => {
    if (!canSave || !patch) return;
    update.mutate(patch, {
      onSuccess: () => router.back(),
      onError: (err) => {
        Alert.alert(
          t("detail.update_failed_toast", "Failed to update agent"),
          err instanceof Error ? err.message : undefined,
        );
      },
    });
  };

  const onPickAvatar = async () => {
    const next = await showAvatarSheet(avatarUrl);
    if (next !== null) setAvatarUrl(next);
  };

  return (
    <KeyboardAvoidingView className="flex-1 bg-background" behavior="padding">
      {/* formSheet 自绘头部（SHEET_OPTIONS headerShown: false）；顶部让出系统状态栏（RUYI-563）。 */}
      <View
        className="flex-row items-center px-4 pb-2 border-b border-border"
        style={{ paddingTop: insets.top + 12 }}
      >
        <Text className="flex-1 text-lg font-semibold text-foreground">
          {t("mobile.detail.edit_profile", "Edit Profile")}
        </Text>
        <Pressable
          onPress={onSave}
          disabled={!canSave}
          className={`px-2 py-1 ${canSave ? "" : "opacity-40"}`}
          accessibilityRole="button"
          accessibilityLabel={t("tab_body.common.save", "Save")}
        >
          <Text className="text-base text-brand font-semibold">
            {update.isPending
              ? t("create_dialog.creating", "Creating...")
              : t("tab_body.common.save", "Save")}
          </Text>
        </Pressable>
      </View>

      <ScrollView
        className="flex-1"
        contentContainerClassName="px-4 pt-4 pb-8 gap-4"
        keyboardShouldPersistTaps="handled"
      >
        <View className="flex-row items-center gap-3">
          <Pressable
            onPress={onPickAvatar}
            disabled={uploading || update.isPending}
            className="active:opacity-70"
            accessibilityRole="imagebutton"
            accessibilityLabel={t("mobile.edit.avatar_change", "Change avatar")}
          >
            <ActorAvatar
              type="agent"
              id={agentId}
              avatarUrl={avatarUrl ? resolveAttachmentUrl(avatarUrl) : null}
              size={64}
            />
          </Pressable>
          <Text className="flex-1 text-sm text-muted-foreground">
            {t("mobile.edit.avatar_hint", "Tap the avatar to change it")}
          </Text>
        </View>

        <Field label={t("create_dialog.name_label", "Name")}>
          <TextInput
            value={name}
            onChangeText={setName}
            placeholder={t("create_dialog.name_placeholder", "e.g. Deep Research Agent")}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            className="text-base text-foreground bg-secondary/50 rounded-md px-3 py-2"
            editable={!update.isPending}
          />
        </Field>

        <Field label={t("create_dialog.description_label", "Description")}>
          <TextInput
            value={description}
            onChangeText={setDescription}
            placeholder={t("create_dialog.description_placeholder", "What does this agent do?")}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            multiline
            className="text-base text-foreground bg-secondary/50 rounded-md px-3 py-2 min-h-16 text-top"
            textAlignVertical="top"
            editable={!update.isPending}
          />
        </Field>

        <Field label={t("create_dialog.voice_label", "Voice")}>
          {runtimesLoading ? (
            <Text className="text-sm text-muted-foreground py-1">
              {t("create_dialog.runtime_loading", "Loading runtimes...")}
            </Text>
          ) : (
            <View className="rounded-md border border-border overflow-hidden">
              {/* 首项固定：不配置语音（提交空串解绑，§4.2） */}
              <Pressable
                onPress={() => setVoiceRuntimeId("")}
                className={`flex-row items-center gap-3 px-3 py-2.5 active:bg-secondary ${
                  voiceChoices.length > 0 ? "border-b border-border" : ""
                }`}
                accessibilityRole="radio"
                accessibilityState={{ selected: voiceRuntimeId === "" }}
              >
                <View
                  className={`size-4 rounded-full border items-center justify-center ${
                    voiceRuntimeId === "" ? "border-brand" : "border-muted-foreground"
                  }`}
                >
                  {voiceRuntimeId === "" ? (
                    <View className="size-2 rounded-full bg-brand" />
                  ) : null}
                </View>
                <Text className="flex-1 text-sm text-foreground">
                  {t("create_dialog.voice_none", "No voice")}
                </Text>
              </Pressable>
              {voiceChoices.length === 0 ? (
                <Text className="text-sm text-muted-foreground px-3 py-2.5">
                  {t("create_dialog.voice_empty", "No voice-capable instance")}
                </Text>
              ) : (
                voiceChoices.map((r, i) => {
                  const selected = r.id === voiceRuntimeId;
                  const badge = runtimeCredentialStatus(r);
                  return (
                    <Pressable
                      key={r.id}
                      onPress={() => setVoiceRuntimeId(r.id)}
                      className={`flex-row items-center gap-3 px-3 py-2.5 active:bg-secondary ${
                        i > 0 ? "border-t border-border" : ""
                      }`}
                      accessibilityRole="radio"
                      accessibilityState={{ selected }}
                    >
                      <View
                        className={`size-4 rounded-full border items-center justify-center ${
                          selected ? "border-brand" : "border-muted-foreground"
                        }`}
                      >
                        {selected ? (
                          <View className="size-2 rounded-full bg-brand" />
                        ) : null}
                      </View>
                      <Text className="flex-1 text-sm text-foreground" numberOfLines={1}>
                        {r.custom_name || r.name}
                      </Text>
                      {r.status !== "online" ? (
                        <Text className="text-xs text-muted-foreground">
                          {t("availability.offline", "Offline")}
                        </Text>
                      ) : (
                        <CredentialBadge status={badge} />
                      )}
                    </Pressable>
                  );
                })
              )}
            </View>
          )}
        </Field>
        <ActionSheetModal {...modalProps} />
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

// RUYI-425 §4.2 voice credential badge states. The agent form never edits
// credentials — it only reflects the instance's stored key state.
function CredentialBadge({
  status,
}: {
  status: "not_configured" | "configured" | "invalid" | "unreachable";
}) {
  const { t } = useT("agents");
  if (status === "configured") {
    return (
      <Text className="text-xs text-success">
        {t("create_dialog.credential_configured", "Configured ✓")}
      </Text>
    );
  }
  if (status === "invalid") {
    return (
      <Text className="text-xs text-destructive">
        {t("create_dialog.credential_invalid", "Key invalid")}
      </Text>
    );
  }
  if (status === "unreachable") {
    return (
      <Text className="text-xs text-warning">
        {t("create_dialog.credential_unreachable", "Can't verify")}
      </Text>
    );
  }
  return (
    <Text className="text-xs text-muted-foreground">
      {t("create_dialog.credential_not_configured", "Not configured")}
    </Text>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <View className="gap-1.5">
      <Text className="text-xs uppercase tracking-wider text-muted-foreground">
        {label}
      </Text>
      {children}
    </View>
  );
}
