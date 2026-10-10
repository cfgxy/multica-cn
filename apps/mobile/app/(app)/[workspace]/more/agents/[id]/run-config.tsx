/**
 * Agent run configuration (`more/agents/[id]/run-config`, RUYI-624) —
 * mobile mirror of web's execution section (agent-detail-inspector.tsx):
 * runtime, model (+ discovered catalog), thinking, service tier,
 * concurrency, session-context gate and the subagent toggle, saved via the
 * same UpdateAgentRequest PUT. Before this screen the facts-card rows on
 * the detail page routed to edit-profile, so runtime/model/concurrency had
 * no direct editor on mobile (RUYI-624 defect ①).
 *
 * The form fields moved verbatim out of edit-profile.tsx when the editors
 * were split (profile vs execution, matching web's section split); the
 * patch is still built by lib/agent-profile-patch's buildProfilePatch with
 * the untouched fields pinned to the saved Agent — only changed fields land
 * in the PUT, so the split editors can never clobber each other's fields.
 *
 * Cascade semantics are web's: switching the runtime clears model /
 * thinking / tier (old vocabulary may not exist on the new runtime);
 * switching the model clears thinking / tier the same way. The voice slot
 * is independent of the text runtime and stays in edit-profile (RUYI-425).
 *
 * Numeric fields follow web's BoundedNumberField: a draft outside its range
 * is NOT stored (revert semantics) — buildProfilePatch omits it. Session
 * max-context keeps 0 as the legal "gate disabled" sentinel outside its
 * range.
 */
import { useEffect, useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, TextInput, View } from "react-native";
// RN 0.83 edge-to-edge 下 Android 的窗口 resize 失效，避让统一走
// keyboard-controller（behavior="padding" 两端一致），见 RUYI-30。
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { router, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import * as Haptics from "expo-haptics";
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
import {
  Field,
  RadioDot,
  Chip,
} from "@/components/agents/agent-editor-fields";
import { agentDetailOptions } from "@/data/queries/agents";
import { runtimeListOptions } from "@/data/queries/runtimes";
import { runtimeModelsOptions } from "@/data/queries/runtime-models";
import { useUpdateAgent } from "@/data/mutations/agents";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { agentSlotChoices } from "@multica/core/runtimes";
import {
  findModelCapabilityEntry,
  resolveThinkingLevels,
} from "@/lib/model-capability";
import { useT } from "@/lib/use-t";
import { buildProfilePatch, parseBounded } from "@/lib/agent-profile-patch";

function formatTokens(n: number): string {
  return new Intl.NumberFormat().format(n);
}

export default function AgentRunConfig() {
  const insets = useSafeAreaInsets();
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

  const [runtimeId, setRuntimeId] = useState("");
  const [model, setModel] = useState("");
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
    setRuntimeId(agent.runtime_id);
    setModel(agent.model);
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
    if (!agent) return [];
    const usable = agentSlotChoices(runtimes, "text", {
      currentUserId: me?.id ?? null,
      excludeRuntimeId: agent.voice_runtime_id ?? "",
      keepRuntimeId: runtimeId,
    });
    if (runtimeId && !usable.some((r) => r.id === runtimeId)) {
      const current = (runtimes ?? []).find((r) => r.id === runtimeId);
      if (current) return [current, ...usable];
    }
    return usable;
  }, [agent, runtimes, runtimeId, me]);

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
  // same cascade web's editors apply.
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

  // The patch carries only fields whose value actually changed; untouched
  // fields are pinned to the saved Agent so buildProfilePatch never emits
  // them (single-sourced PUT semantics shared with the split editors).
  const patch = useMemo(() => {
    if (!agent || !agent.id || !seeded) return null;
    return buildProfilePatch(agent, {
      name: agent.name,
      description: agent.description,
      instructions: agent.instructions,
      avatarUrl: agent.avatar_url ?? "",
      model,
      runtimeId,
      voiceRuntimeId: agent.voice_runtime_id ?? "",
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
    runtimeId,
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
      onSuccess: () => {
        Haptics.notificationAsync(
          Haptics.NotificationFeedbackType.Success,
        ).catch(() => {});
        router.back();
      },
      onError: (err) => {
        Alert.alert(
          t("detail.update_failed_toast", "Failed to update agent"),
          err instanceof Error ? err.message : undefined,
        );
      },
    });
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
      {/* formSheet 自绘头部（SHEET_OPTIONS headerShown: false）；顶部让出系统状态栏（RUYI-563）。 */}
      <View
        className="flex-row items-center px-4 pb-2 border-b border-border"
        style={{ paddingTop: insets.top + 12 }}
      >
        <Text className="flex-1 text-lg font-semibold text-foreground">
          {t("inspector.section_execution", "Execution")}
        </Text>
        <Pressable
          onPress={onSave}
          disabled={!canSave}
          className={`px-2 py-1 ${canSave ? "" : "opacity-40"}`}
          accessibilityRole="button"
          accessibilityLabel={t("tab_body.common.save", "Save")}
        >
          <Text className="text-base text-brand font-semibold">
            {t("tab_body.common.save", "Save")}
          </Text>
        </Pressable>
      </View>

      <ScrollView
        nestedScrollEnabled
        className="flex-1"
        contentContainerClassName="px-4 pt-4 pb-8 gap-4"
        keyboardShouldPersistTaps="handled"
      >
        <Field label={t("inspector.prop_runtime", "Runtime")}>
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
      </ScrollView>
    </KeyboardAvoidingView>
  );
}
