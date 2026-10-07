/**
 * Edit agent profile (`more/agents/[id]/edit-profile`, RUYI-346 A2 /
 * RUYI-418 B2) — formSheet per SHEET_OPTIONS. Mirrors web's profile editor
 * PUT (UpdateAgentRequest): name / description / instructions / avatar /
 * runtime / model / thinking / service tier / concurrency cap / session
 * context gate / allow_subagents.
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
 * Model keeps a manual free-text input as the fallback (S4): the discovered
 * catalog renders above it only when the runtime is online and discovery
 * answered with models. Thinking / service-tier vocabularies derive from
 * the same catalog (web ThinkingPropRow / ServiceTierSettingField
 * semantics, mirrored in lib/model-capability.ts).
 *
 * Numeric fields follow web's BoundedNumberField: a draft outside its range
 * is NOT stored (revert semantics) — clamping would silently save a number
 * the operator never typed. Session max-context keeps 0 as the legal
 * "gate disabled" sentinel outside its range.
 *
 * Access scope is intentionally absent — on web it lives in the separate
 * set-access dialog and is owner-only; mobile keeps that split (S5 screen).
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
import {
  AGENT_MAX_CONCURRENT_TASKS_MAX,
  AGENT_MAX_CONCURRENT_TASKS_MIN,
  AGENT_SESSION_COMPACT_PCT_DEFAULT,
  AGENT_SESSION_COMPACT_PCT_MAX,
  AGENT_SESSION_COMPACT_PCT_MIN,
  AGENT_SESSION_MAX_CONTEXT_TOKENS_DEFAULT,
  AGENT_SESSION_MAX_CONTEXT_TOKENS_DISABLED,
  AGENT_SESSION_MAX_CONTEXT_TOKENS_MAX,
  AGENT_SESSION_MAX_CONTEXT_TOKENS_MIN,
  agentSessionEffectiveCompactThreshold,
} from "@multica/core/agents/constants";
import { allowsSubagents } from "@multica/core/agents/subagent-tools";
import { Text } from "@/components/ui/text";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { Switch } from "@/components/ui/switch";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { agentDetailOptions } from "@/data/queries/agents";
import { runtimeListOptions } from "@/data/queries/runtimes";
import { runtimeModelsOptions } from "@/data/queries/runtime-models";
import { useUpdateAgent } from "@/data/mutations/agents";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { ActionSheetModal } from "@/components/ui/action-sheet";
import { useAvatarUploader } from "@/lib/avatar";
import { resolveAttachmentUrl } from "@/lib/attachment-url";
import {
  findModelCapabilityEntry,
  resolveThinkingLevels,
} from "@/lib/model-capability";
import { useT } from "@/lib/use-t";
import { buildProfilePatch, parseBounded } from "@/lib/agent-profile-patch";

function formatTokens(n: number): string {
  return new Intl.NumberFormat().format(n);
}

export default function EditAgentProfile() {
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
  const [instructions, setInstructions] = useState("");
  const [avatarUrl, setAvatarUrl] = useState("");
  const [model, setModel] = useState("");
  const [runtimeId, setRuntimeId] = useState("");
  const [voiceRuntimeId, setVoiceRuntimeId] = useState("");
  const [thinking, setThinking] = useState("");
  const [tier, setTier] = useState("");
  const [concurrency, setConcurrency] = useState("1");
  const [maxContext, setMaxContext] = useState(
    String(AGENT_SESSION_MAX_CONTEXT_TOKENS_DEFAULT),
  );
  const [compactPct, setCompactPct] = useState(
    String(AGENT_SESSION_COMPACT_PCT_DEFAULT),
  );
  const [subagents, setSubagents] = useState(false);
  const [seeded, setSeeded] = useState(false);

  useEffect(() => {
    if (!agent || !agent.id || seeded) return;
    setName(agent.name);
    setDescription(agent.description);
    setInstructions(agent.instructions);
    setAvatarUrl(agent.avatar_url ?? "");
    setModel(agent.model);
    setRuntimeId(agent.runtime_id);
    setVoiceRuntimeId(agent.voice_runtime_id ?? "");
    setThinking(agent.thinking_level ?? "");
    setTier(agent.service_tier ?? "");
    setConcurrency(String(agent.max_concurrent_tasks || 1));
    setMaxContext(
      String(
        agent.session_max_context_tokens ??
          AGENT_SESSION_MAX_CONTEXT_TOKENS_DEFAULT,
      ),
    );
    setCompactPct(
      String(agent.session_compact_pct ?? AGENT_SESSION_COMPACT_PCT_DEFAULT),
    );
    setSubagents(allowsSubagents(agent.runtime_config));
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

  const runtime = runtimes?.find((r) => r.id === runtimeId);
  const provider = runtime?.provider ?? "";
  // Discovery only runs against an online runtime; offline keeps the manual
  // model input as the sole vocabulary (S4 fallback).
  const runtimeOnline = runtime?.status === "online";
  const modelsQuery = useQuery(runtimeModelsOptions(runtimeOnline ? runtimeId : null));
  const catalogModels = useMemo(
    () => modelsQuery.data?.models ?? [],
    [modelsQuery.data],
  );
  const catalogEntry = findModelCapabilityEntry(catalogModels, model, provider);
  const thinkingLevels = resolveThinkingLevels(catalogModels, model, provider);
  const tiers = catalogEntry?.service_tiers ?? [];
  const supportsExplicitStandard = catalogModels.some(
    (candidate) => candidate.supports_explicit_standard_service_tier === true,
  );

  // Changing the runtime resets model-derived state (model / thinking /
  // tier) because the old vocabulary may not exist on the new runtime —
  // same cascade web's editors apply. The voice slot is independent of the
  // text runtime, so the cascade never touches it.
  const handleRuntimeChange = (next: string) => {
    if (next === runtimeId) return;
    setRuntimeId(next);
    setModel("");
    setThinking("");
    setTier("");
  };
  // Changing the model invalidates thinking / tier the same way.
  const handleModelChange = (next: string) => {
    if (next === model) return;
    setModel(next);
    setThinking("");
    setTier("");
  };

  // The patch carries only fields whose value actually changed; the
  // semantics (only-changed fields, bounded-number revert, tri-state ""
  // clears, subagent runtime_config round-trip) live in
  // lib/agent-profile-patch with unit coverage (RUYI-538 ①).
  const patch = useMemo(() => {
    if (!agent || !seeded) return null;
    return buildProfilePatch(agent, {
      name,
      description,
      instructions,
      avatarUrl,
      model,
      runtimeId,
      voiceRuntimeId,
      thinking,
      tier,
      concurrency,
      maxContext,
      compactPct,
      subagents,
    });
  }, [
    agent,
    seeded,
    name,
    description,
    instructions,
    avatarUrl,
    runtimeId,
    voiceRuntimeId,
    model,
    thinking,
    tier,
    concurrency,
    maxContext,
    compactPct,
    subagents,
  ]);
  const dirty = !!patch && Object.keys(patch).length > 0;

  const runtimeReady =
    !runtimesLoading && (runtimeChoices.length > 0 || runtimeId !== "");

  const canSave = !!patch && dirty && runtimeReady && !update.isPending;

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

  // Effective compact trigger follows the DRAFT context ceiling so the hint
  // updates as the user types (web computes from saved values; the mobile
  // form saves in one shot, so the draft is the honest source here).
  const draftCtx = parseBounded(
    maxContext,
    AGENT_SESSION_MAX_CONTEXT_TOKENS_MIN,
    AGENT_SESSION_MAX_CONTEXT_TOKENS_MAX,
    AGENT_SESSION_MAX_CONTEXT_TOKENS_DISABLED,
  );
  const draftPct = parseBounded(
    compactPct,
    AGENT_SESSION_COMPACT_PCT_MIN,
    AGENT_SESSION_COMPACT_PCT_MAX,
  );
  const effectiveCtx =
    draftCtx ??
    agent?.session_max_context_tokens ??
    AGENT_SESSION_MAX_CONTEXT_TOKENS_DEFAULT;
  const gateDisabled = effectiveCtx === AGENT_SESSION_MAX_CONTEXT_TOKENS_DISABLED;

  const showThinking = thinkingLevels.length > 0 || thinking !== "";
  const tierOptions = supportsExplicitStandard
    ? [
        {
          id: "default",
          name: t("pickers.service_tier_standard", "Standard"),
        },
        ...tiers.filter((x) => x.id !== "default"),
      ]
    : tiers;
  const showTier = tierOptions.length > 0 || tier !== "";

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
                    onPress={() => handleRuntimeChange(r.id)}
                    className={`flex-row items-center gap-3 px-3 py-2.5 active:bg-secondary ${
                      i > 0 ? "border-t border-border" : ""
                    }`}
                    accessibilityRole="radio"
                    accessibilityState={{ selected }}
                  >
                    <RadioDot selected={selected} />
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

        {runtimeOnline && catalogModels.length > 0 ? (
          <Field label={t("mobile.edit.model_catalog", "Discovered models")}>
            <View className="rounded-md border border-border overflow-hidden">
              {catalogModels.map((m, i) => {
                const selected = m.id === model;
                return (
                  <Pressable
                    key={m.id}
                    onPress={() => handleModelChange(m.id)}
                    className={`flex-row items-center gap-3 px-3 py-2.5 active:bg-secondary ${
                      i > 0 ? "border-t border-border" : ""
                    }`}
                    accessibilityRole="radio"
                    accessibilityState={{ selected }}
                  >
                    <RadioDot selected={selected} />
                    <Text className="flex-1 text-sm text-foreground" numberOfLines={1}>
                      {m.id}
                    </Text>
                    {m.default ? (
                      <Text className="text-xs text-muted-foreground">
                        {t("mobile.edit.model_default", "Default")}
                      </Text>
                    ) : null}
                  </Pressable>
                );
              })}
            </View>
          </Field>
        ) : null}

        {showThinking ? (
          <Field label={t("inspector.prop_thinking", "Thinking")}>
            <View className="flex-row flex-wrap gap-2">
              <Chip
                label={t("pickers.thinking_default", "Follow CLI config")}
                selected={thinking === ""}
                onPress={() => setThinking("")}
              />
              {thinkingLevels.map((level) => (
                <Chip
                  key={level.value}
                  label={level.label}
                  selected={thinking === level.value}
                  onPress={() => setThinking(level.value)}
                />
              ))}
            </View>
          </Field>
        ) : null}

        {showTier ? (
          <Field label={t("inspector.prop_speed", "Speed")}>
            <View className="flex-row flex-wrap gap-2">
              {tierOptions.map((option) => (
                <Chip
                  key={option.id}
                  label={option.name}
                  selected={tier === option.id}
                  onPress={() => setTier(option.id)}
                />
              ))}
              {tier ? (
                <Chip
                  label={t("pickers.service_tier_clear", "Runtime default")}
                  selected={tier === ""}
                  onPress={() => setTier("")}
                />
              ) : null}
            </View>
          </Field>
        ) : null}

        <Field label={t("inspector.prop_concurrency", "Concurrency")}>
          <TextInput
            value={concurrency}
            onChangeText={(v) => setConcurrency(v.replace(/[^0-9]/g, ""))}
            keyboardType="number-pad"
            className="text-base text-foreground bg-secondary/50 rounded-md px-3 py-2 w-24"
            editable={!update.isPending}
          />
          <Text className="text-xs text-muted-foreground">
            {t("mobile.edit.concurrency_range", "Allowed range: {{min}}–{{max}}", {
              min: AGENT_MAX_CONCURRENT_TASKS_MIN,
              max: AGENT_MAX_CONCURRENT_TASKS_MAX,
            })}
          </Text>
        </Field>

        <Field label={t("inspector.section_session_context", "Session context")}>
          <Text className="text-xs text-muted-foreground">
            {t(
              "inspector.section_session_context_hint",
              "Control when a long conversation is replaced by a fresh session carrying a summary of the prior one.",
            )}
          </Text>
        </Field>

        <Field label={t("inspector.prop_session_max_context_tokens", "Context limit")}>
          <TextInput
            value={maxContext}
            onChangeText={(v) => setMaxContext(v.replace(/[^0-9]/g, ""))}
            keyboardType="number-pad"
            className="text-base text-foreground bg-secondary/50 rounded-md px-3 py-2 w-40"
            editable={!update.isPending}
          />
          <Text className="text-xs text-muted-foreground">
            {gateDisabled
              ? t(
                  "pickers.session_context_disabled",
                  "Turned off — sessions resume regardless of size",
                )
              : t(
                  "pickers.session_max_context_tokens_range",
                  "Context ceiling in tokens ({{min}}–{{max}}), or 0 to turn this off",
                  {
                    min: formatTokens(AGENT_SESSION_MAX_CONTEXT_TOKENS_MIN),
                    max: formatTokens(AGENT_SESSION_MAX_CONTEXT_TOKENS_MAX),
                  },
                )}
          </Text>
        </Field>

        <Field label={t("inspector.prop_session_compact_pct", "Switch at")}>
          <TextInput
            value={compactPct}
            onChangeText={(v) => setCompactPct(v.replace(/[^0-9]/g, ""))}
            keyboardType="number-pad"
            className="text-base text-foreground bg-secondary/50 rounded-md px-3 py-2 w-24"
            editable={!update.isPending && !gateDisabled}
          />
          <Text className="text-xs text-muted-foreground">
            {t(
              "pickers.session_compact_pct_range",
              "Start a fresh session at this percentage of the ceiling ({{min}}–{{max}}) — actually at {{threshold}} tokens, since the switch never fires below 50,000",
              {
                min: AGENT_SESSION_COMPACT_PCT_MIN,
                max: AGENT_SESSION_COMPACT_PCT_MAX,
                threshold: formatTokens(
                  agentSessionEffectiveCompactThreshold(
                    effectiveCtx,
                    draftPct ?? AGENT_SESSION_COMPACT_PCT_DEFAULT,
                  ),
                ),
              },
            )}
          </Text>
        </Field>

        <View className="flex-row items-start gap-3 rounded-md border border-border px-3 py-3">
          <View className="flex-1 gap-1">
            <Text className="text-sm font-medium text-foreground">
              {t("inspector.prop_subagents", "Subagent tools")}
            </Text>
            <Text className="text-xs text-muted-foreground">
              {t(
                "inspector.prop_subagents_hint",
                "Off (default) hard-denies the Agent/Task tools at runtime, so this agent cannot delegate work to subagents. Turn on only for agents that need it.",
              )}
            </Text>
          </View>
          <Switch
            checked={subagents}
            disabled={update.isPending}
            onCheckedChange={(enabled) => setSubagents(enabled)}
            aria-label={t("inspector.prop_subagents_toggle_aria", "Allow subagent tools")}
          />
        </View>
        <ActionSheetModal {...modalProps} />
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

function RadioDot({ selected }: { selected: boolean }) {
  return (
    <View
      className={`size-4 rounded-full border items-center justify-center ${
        selected ? "border-brand" : "border-muted-foreground"
      }`}
    >
      {selected ? <View className="size-2 rounded-full bg-brand" /> : null}
    </View>
  );
}

function Chip({
  label,
  selected,
  onPress,
}: {
  label: string;
  selected: boolean;
  onPress: () => void;
}) {
  return (
    <Pressable
      onPress={onPress}
      className={`rounded-full border px-3 py-1.5 active:opacity-70 ${
        selected
          ? "border-brand bg-brand/10"
          : "border-border bg-transparent"
      }`}
      accessibilityRole="radio"
      accessibilityState={{ selected }}
    >
      <Text
        className={`text-xs ${selected ? "text-brand font-medium" : "text-muted-foreground"}`}
      >
        {label}
      </Text>
    </Pressable>
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
