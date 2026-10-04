/**
 * Edit agent profile (`more/agents/[id]/edit-profile`, RUYI-346 A2 sub-screen)
 * — formSheet per SHEET_OPTIONS. Fields mirror web's profile editor PUT
 * (UpdateAgentRequest): name / description / instructions / model / runtime /
 * concurrency cap. Access scope is intentionally absent — on web it lives in
 * the separate set-access dialog and is owner-only; P0 mobile keeps that split.
 *
 * Explicit Save button (mobile form paradigm). Runtime choices are the
 * online runtimes usable by the current user (`isRuntimeUsableForUser`,
 * web create-form parity); the agent's current runtime stays selectable
 * even if it went offline so the field never shows a lie.
 *
 * RUYI-425 §4.2: adds the optional voice slot (voice_runtime_id). The patch
 * is tri-state — omitted when untouched, "" to unbind, an id to bind — and
 * the pickers exclude each other's selection (`agentSlotChoices`).
 *
 * Model is a free-text field in P0: web's model catalog discovery endpoint
 * is not surfaced on mobile yet (adaptation noted in the delivery report).
 */
import { useEffect, useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, TextInput, View } from "react-native";
// RN 0.83 edge-to-edge 下 Android 的窗口 resize 失效，避让统一走
// keyboard-controller（behavior="padding" 两端一致），见 RUYI-30。
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { router, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import {
  agentSlotChoices,
  runtimeCredentialStatus,
} from "@multica/core/runtimes";
import type { UpdateAgentRequest } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { agentDetailOptions } from "@/data/queries/agents";
import { runtimeListOptions } from "@/data/queries/runtimes";
import { useUpdateAgent } from "@/data/mutations/agents";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useT } from "@/lib/use-t";

export default function EditAgentProfile() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const agentId = typeof id === "string" ? id : "";
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const me = useAuthStore((s) => s.user);
  const { t } = useT("agents");
  const update = useUpdateAgent(agentId);

  const { data: agent } = useQuery(agentDetailOptions(wsId, agentId));
  const { data: runtimes, isLoading: runtimesLoading } = useQuery(
    runtimeListOptions(wsId),
  );

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [instructions, setInstructions] = useState("");
  const [model, setModel] = useState("");
  const [runtimeId, setRuntimeId] = useState("");
  const [voiceRuntimeId, setVoiceRuntimeId] = useState("");
  const [concurrency, setConcurrency] = useState("1");
  const [seeded, setSeeded] = useState(false);

  useEffect(() => {
    if (!agent || !agent.id || seeded) return;
    setName(agent.name);
    setDescription(agent.description);
    setInstructions(agent.instructions);
    setModel(agent.model);
    setRuntimeId(agent.runtime_id);
    setVoiceRuntimeId(agent.voice_runtime_id ?? "");
    setConcurrency(String(agent.max_concurrent_tasks || 1));
    setSeeded(true);
  }, [agent, seeded]);

  // Text slot choices: online + usable by me + text-capable, minus the voice
  // slot's pick — plus the agent's current runtime even when offline (so the
  // current binding is always visible/pickable, never a lie).
  const runtimeChoices = useMemo(() => {
    const usable = agentSlotChoices(runtimes, "text", {
      currentUserId: me?.id ?? null,
      excludeRuntimeId: voiceRuntimeId,
      keepRuntimeId: runtimeId,
    });
    if (runtimeId && !usable.some((r) => r.id === runtimeId)) {
      const current = (runtimes ?? []).find((r) => r.id === runtimeId);
      if (current) return [current, ...usable];
    }
    return usable;
  }, [runtimes, runtimeId, voiceRuntimeId, me]);

  // Voice slot choices: realtime-voice-capable, minus the text slot's pick,
  // plus the agent's current voice binding even when offline.
  const voiceChoices = useMemo(() => {
    const usable = agentSlotChoices(runtimes, "realtime_voice", {
      currentUserId: me?.id ?? null,
      excludeRuntimeId: runtimeId,
      keepRuntimeId: voiceRuntimeId,
    });
    if (voiceRuntimeId && !usable.some((r) => r.id === voiceRuntimeId)) {
      const current = (runtimes ?? []).find((r) => r.id === voiceRuntimeId);
      if (current) return [current, ...usable];
    }
    return usable;
  }, [runtimes, runtimeId, voiceRuntimeId, me]);

  const dirty = useMemo(() => {
    if (!agent || !seeded) return false;
    const nextConcurrency = parseInt(concurrency, 10);
    return (
      name.trim() !== agent.name ||
      description !== agent.description ||
      instructions !== agent.instructions ||
      model !== agent.model ||
      runtimeId !== agent.runtime_id ||
      voiceRuntimeId !== (agent.voice_runtime_id ?? "") ||
      (Number.isFinite(nextConcurrency) && nextConcurrency > 0
        ? nextConcurrency !== agent.max_concurrent_tasks
        : false)
    );
  }, [agent, seeded, name, description, instructions, model, runtimeId, voiceRuntimeId, concurrency]);

  const runtimeReady =
    !runtimesLoading && (runtimeChoices.length > 0 || runtimeId !== "");

  const canSave = !!agent && seeded && name.trim().length > 0 && dirty && runtimeReady && !update.isPending;

  const onSave = () => {
    if (!canSave || !agent) return;
    const parsed = parseInt(concurrency, 10);
    const patch: UpdateAgentRequest = {
      name: name.trim(),
      description,
      instructions,
      model,
      runtime_id: runtimeId,
      // Tri-state voice slot: omitted = untouched, "" = unbind, id = bind.
      ...(voiceRuntimeId !== (agent.voice_runtime_id ?? "")
        ? { voice_runtime_id: voiceRuntimeId }
        : {}),
      ...(Number.isFinite(parsed) && parsed > 0
        ? { max_concurrent_tasks: parsed }
        : {}),
    };
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

  return (
    <KeyboardAvoidingView className="flex-1 bg-background" behavior="padding">
      {/* formSheet 自绘头部（SHEET_OPTIONS headerShown: false） */}
      <View className="flex-row items-center px-4 pt-3 pb-2 border-b border-border">
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

        <Field label={t("create_dialog.instructions.label", "Instructions")}>
          <TextInput
            value={instructions}
            onChangeText={setInstructions}
            placeholder={t(
              "create_dialog.instructions.editor_placeholder",
              "Write what this agent should do, what to focus on, what to avoid…",
            )}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            multiline
            className="text-base text-foreground bg-secondary/50 rounded-md px-3 py-2 min-h-32"
            textAlignVertical="top"
            editable={!update.isPending}
          />
        </Field>

        <Field label={t("inspector.prop_model", "Model")}>
          <TextInput
            value={model}
            onChangeText={setModel}
            placeholder={t("mobile.edit.model_placeholder", "Leave empty for the runtime default")}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            autoCapitalize="none"
            autoCorrect={false}
            className="text-base text-foreground bg-secondary/50 rounded-md px-3 py-2"
            editable={!update.isPending}
          />
        </Field>

        <Field label={t("create_dialog.runtime_label", "Runtime")}>
          {runtimesLoading ? (
            <Text className="text-sm text-muted-foreground py-1">
              {t("create_dialog.runtime_loading", "Loading runtimes...")}
            </Text>
          ) : runtimeChoices.length === 0 ? (
            <Text className="text-sm text-muted-foreground py-1">
              {t("create_dialog.runtime_none", "No runtime available")}
            </Text>
          ) : (
            <View className="rounded-md border border-border overflow-hidden">
              {runtimeChoices.map((r, i) => {
                const selected = r.id === runtimeId;
                return (
                  <Pressable
                    key={r.id}
                    onPress={() => setRuntimeId(r.id)}
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
                    ) : null}
                  </Pressable>
                );
              })}
            </View>
          )}
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

        <Field label={t("inspector.prop_concurrency", "Concurrency")}>
          <TextInput
            value={concurrency}
            onChangeText={(v) => setConcurrency(v.replace(/[^0-9]/g, ""))}
            keyboardType="number-pad"
            className="text-base text-foreground bg-secondary/50 rounded-md px-3 py-2 w-24"
            editable={!update.isPending}
          />
        </Field>
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

// RUYI-425 §4.2 voice credential tri-state badge. The agent form never edits
// credentials — it only reflects the instance's stored key state.
function CredentialBadge({
  status,
}: {
  status: "not_configured" | "configured" | "invalid";
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
