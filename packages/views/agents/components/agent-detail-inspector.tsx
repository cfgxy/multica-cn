"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import type {
  Agent,
  AgentRuntime,
  MemberWithUser,
} from "@multica/core/types";
import {
  AGENT_DESCRIPTION_MAX_LENGTH,
  AGENT_MAX_CONCURRENT_TASKS_MAX,
  AGENT_MAX_CONCURRENT_TASKS_MIN,
  AGENT_MAX_CONCURRENT_TASKS_UNLIMITED,
  AGENT_SESSION_COMPACT_PCT_MAX,
  AGENT_SESSION_COMPACT_PCT_MIN,
  AGENT_SESSION_MAX_CONTEXT_TOKENS_DISABLED,
  AGENT_SESSION_MAX_CONTEXT_TOKENS_MAX,
  AGENT_SESSION_MAX_CONTEXT_TOKENS_MIN,
  agentSessionEffectiveCompactThreshold,
  allowsSubagents,
  mergeSubagentAllowance,
} from "@multica/core/agents";
import {
  isRuntimeUsableForUser,
  runtimeModelsOptions,
} from "@multica/core/runtimes";
import { isImeComposing } from "@multica/core/utils";
import { Input } from "@multica/ui/components/ui/input";
import { Button } from "@multica/ui/components/ui/button";
import { Switch } from "@multica/ui/components/ui/switch";
import { Textarea } from "@multica/ui/components/ui/textarea";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "@multica/ui/components/ui/select";
import {
  parseVoiceAdvancedDraft,
  voiceProviderChainEligible,
  voiceProviderListOptions,
} from "@multica/core/voice/providers";
import { AvatarUploadControl } from "../../common/avatar-upload-control";
import {
  SettingsCard,
  SettingsRow,
  SettingsSaveState,
  SettingsSection,
} from "../../settings/components/settings-layout";
import { useAutoSave } from "../../settings/components/use-auto-save";
import { useT } from "../../i18n";
import { CharCounter } from "./char-counter";
import { ModelPicker } from "./inspector/model-picker";
import {
  buildModelChangeUpdate,
  type ModelCatalog,
} from "./inspector/model-change-cleanup";
import { RuntimePicker } from "./inspector/runtime-picker";
import { ThinkingSettingField } from "./inspector/thinking-prop-row";
import { ServiceTierSettingField } from "./inspector/service-tier-setting-field";

interface InspectorProps {
  agent: Agent;
  runtime: AgentRuntime | null;
  runtimes: AgentRuntime[];
  members: MemberWithUser[];
  currentUserId: string | null;
  canEdit: boolean;
  onUpdate: (id: string, data: Record<string, unknown>) => Promise<void>;
}

interface ProfileDraft {
  name: string;
  description: string;
}

function profileDraftsEqual(left: ProfileDraft, right: ProfileDraft) {
  return left.name === right.name && left.description === right.description;
}

/**
 * Full-width General settings form. Every editable value is presented as an
 * explicit field; compact inspector chips are used only through their
 * settings-field variants, where the whole control is a visible click target.
 */
export function AgentDetailInspector({
  agent,
  runtime,
  runtimes,
  members,
  currentUserId,
  canEdit,
  onUpdate,
}: InspectorProps) {
  const { t } = useT("agents");
  const { t: ts } = useT("settings");
  const update = useCallback(
    (data: Record<string, unknown>) => onUpdate(agent.id, data),
    [agent.id, onUpdate],
  );

  const [name, setName] = useState(agent.name);
  const [description, setDescription] = useState(agent.description ?? "");

  useEffect(() => {
    setName(agent.name);
    setDescription(agent.description ?? "");
    // Reset only when moving to another agent. Cache updates from this form
    // must not erase a newer local draft while an autosave is in flight.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agent.id]);

  const profileDraft = useMemo(
    () => ({ name: name.trim(), description }),
    [description, name],
  );
  const savedProfile = useMemo(
    () => ({
      name: agent.name,
      description: agent.description ?? "",
    }),
    [agent.description, agent.name],
  );
  const saveProfile = useCallback(
    async (next: ProfileDraft) => {
      await update({ name: next.name, description: next.description });
    },
    [update],
  );
  const profileAutoSave = useAutoSave({
    value: profileDraft,
    savedValue: savedProfile,
    onSave: saveProfile,
    enabled:
      canEdit &&
      profileDraft.name.length > 0 &&
      profileDraft.description.length <= AGENT_DESCRIPTION_MAX_LENGTH,
    isEqual: profileDraftsEqual,
  });

  const isOnline = runtime?.status === "online";
  const canReadRuntime =
    runtime != null && isRuntimeUsableForUser(runtime, currentUserId);
  const canDiscoverRuntimeModels = isOnline && canReadRuntime;
  const nameInvalid = name.trim().length === 0;

  // Same query the Thinking / Speed fields already use, so switching model
  // costs no extra request. `null` = not authoritative (offline runtime, still
  // loading, or discovery failed) and must not trigger any clearing.
  const modelsQuery = useQuery(
    runtimeModelsOptions(canDiscoverRuntimeModels ? agent.runtime_id : null),
  );
  const modelCatalog = useMemo<ModelCatalog>(
    () =>
      modelsQuery.isSuccess
        ? modelsQuery.data.supported
          ? modelsQuery.data.models
          : []
        : null,
    [modelsQuery.data, modelsQuery.isSuccess],
  );
  const handleModelChange = useCallback(
    (model: string) =>
      update(
        buildModelChangeUpdate({
          provider: runtime?.provider ?? "",
          model,
          thinkingLevel: agent.thinking_level ?? "",
          serviceTier: agent.service_tier ?? "",
          catalog: modelCatalog,
        }),
      ),
    [agent.service_tier, agent.thinking_level, modelCatalog, runtime?.provider, update],
  );

  return (
    <div className="space-y-8">
      <SettingsSection
        title={t(($) => $.inspector.section_profile)}
        description={t(($) => $.inspector.section_profile_hint)}
        action={
          <SettingsSaveState
            status={profileAutoSave.status}
            savingLabel={ts(($) => $.auto_save.saving)}
            savedLabel={ts(($) => $.auto_save.saved)}
            errorLabel={ts(($) => $.auto_save.failed)}
          />
        }
      >
        <SettingsCard>
          <SettingsRow
            label={t(($) => $.inspector.avatar_label)}
            description={t(($) => $.inspector.avatar_hint)}
            size="none"
          >
            <div className="flex justify-start sm:justify-end">
              <AvatarUploadControl
                variant="agent"
                value={agent.avatar_url ?? null}
                name={agent.name}
                size={56}
                disabled={!canEdit}
                onUploaded={(url) => update({ avatar_url: url })}
                onEmojiSelected={(value) => update({ avatar_url: value })}
              />
            </div>
          </SettingsRow>

          <SettingsRow
            label={t(($) => $.inspector.name_label)}
            size="text"
          >
            <div>
              <Input
                type="text"
                name="agent-name"
                autoComplete="off"
                aria-label={t(($) => $.inspector.name_label)}
                value={name}
                onChange={(event) => setName(event.target.value)}
                onBlur={profileAutoSave.flush}
                disabled={!canEdit}
                aria-invalid={nameInvalid || undefined}
              />
              {nameInvalid ? (
                <p className="mt-1 text-caption text-destructive">
                  {t(($) => $.inspector.rename_required)}
                </p>
              ) : null}
            </div>
          </SettingsRow>

          <SettingsRow
            label={t(($) => $.inspector.description_label)}
            size="text"
            align="start"
          >
            <div>
              <Textarea
                name="agent-description"
                autoComplete="off"
                aria-label={t(($) => $.inspector.description_label)}
                value={description}
                onChange={(event) => setDescription(event.target.value)}
                onBlur={profileAutoSave.flush}
                disabled={!canEdit}
                rows={5}
                maxLength={AGENT_DESCRIPTION_MAX_LENGTH}
                className="resize-y"
                placeholder={t(($) => $.inspector.description_placeholder)}
              />
              <CharCounter
                length={[...description].length}
                max={AGENT_DESCRIPTION_MAX_LENGTH}
              />
            </div>
          </SettingsRow>
        </SettingsCard>
      </SettingsSection>

      <SettingsSection
        title={t(($) => $.inspector.section_execution)}
        description={t(($) => $.inspector.section_execution_hint)}
      >
        <SettingsCard>
          <SettingsRow
            label={t(($) => $.inspector.prop_runtime)}
            size="select-wide"
          >
            <RuntimePicker
              variant="field"
              showLabel={false}
              value={agent.runtime_id}
              runtimes={runtimes}
              members={members}
              currentUserId={currentUserId}
              canEdit={canEdit}
              // Model, thinking level, and service tier are runtime/model
              // native. Clear them together so the new runtime resolves its
              // own defaults instead of inheriting incompatible tokens.
              onChange={(id) =>
                update({
                  runtime_id: id,
                  model: "",
                  thinking_level: "",
                  service_tier: "",
                })
              }
            />
          </SettingsRow>
          <SettingsRow
            label={t(($) => $.inspector.prop_model)}
            size="select-wide"
          >
            <ModelPicker
              variant="field"
              showLabel={false}
              runtimeId={agent.runtime_id}
              runtimeOnline={canDiscoverRuntimeModels}
              value={agent.model ?? ""}
              canEdit={canEdit}
              onChange={handleModelChange}
            />
          </SettingsRow>
          <ThinkingSettingField
            label={t(($) => $.inspector.prop_thinking)}
            runtimeId={agent.runtime_id}
            runtimeOnline={canDiscoverRuntimeModels}
            provider={runtime?.provider ?? ""}
            model={agent.model ?? ""}
            value={agent.thinking_level ?? ""}
            canEdit={canEdit}
            onChange={(thinkingLevel) =>
              update({ thinking_level: thinkingLevel })
            }
          />
          <ServiceTierSettingField
            label={t(($) => $.inspector.prop_speed)}
            runtimeId={agent.runtime_id}
            runtimeOnline={canDiscoverRuntimeModels}
            provider={runtime?.provider ?? ""}
            model={agent.model ?? ""}
            value={agent.service_tier ?? ""}
            canEdit={canEdit}
            onChange={(serviceTier) => update({ service_tier: serviceTier })}
          />
          <SettingsRow
            label={t(($) => $.inspector.prop_concurrency)}
            size="select-wide"
          >
            <ConcurrencyField
              value={agent.max_concurrent_tasks}
              canEdit={canEdit}
              onSave={(next) => update({ max_concurrent_tasks: next })}
            />
          </SettingsRow>
          <SubagentSettingRow agent={agent} canEdit={canEdit} update={update} />
        </SettingsCard>
      </SettingsSection>

      <VoiceOverrideSection agent={agent} canEdit={canEdit} update={update} />

      <SessionContextSection
        agent={agent}
        canEdit={canEdit}
        update={update}
      />
    </div>
  );
}

/**
 * Subagent tool allowance (runtime_config.allow_subagents). Lives in the
 * execution section because it gates what the agent's task process may do —
 * same family as concurrency — and defaults to denied (fail-closed) on the
 * daemon side. Saves immediately like the other single-purpose toggles.
 */
function SubagentSettingRow({
  agent,
  canEdit,
  update,
}: {
  agent: Agent;
  canEdit: boolean;
  update: (data: Record<string, unknown>) => Promise<void>;
}) {
  const { t } = useT("agents");
  const [busy, setBusy] = useState(false);
  const allowed = allowsSubagents(agent.runtime_config);
  const handleToggle = async (allow: boolean) => {
    setBusy(true);
    try {
      await update({
        runtime_config: mergeSubagentAllowance(agent.runtime_config, allow),
      });
    } catch (error) {
      toast.error(
        error instanceof Error && error.message
          ? error.message
          : t(($) => $.inspector.subagents_toggle_failed_toast),
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <SettingsRow
      label={t(($) => $.inspector.prop_subagents)}
      description={t(($) => $.inspector.prop_subagents_hint)}
      align="start"
    >
      {canEdit && (
        <Switch
          checked={allowed}
          disabled={busy}
          onCheckedChange={(checked: boolean) => handleToggle(checked)}
          aria-label={t(($) => $.inspector.prop_subagents_toggle_aria)}
        />
      )}
    </SettingsRow>
  );
}

/**
 * Agent-level voice override (RUYI-749 D1). The server PATCH is level-wise
 * atomic: `voice_provider_id` binds the override as one unit (omitted
 * model/advanced in the same request reset that level), while
 * `voice_model`/`voice_advanced` alone refine an existing override and
 * `voice_provider_id: ""` unbinds it whole. The draft mirrors those
 * semantics instead of inventing a client-side merge.
 */
function VoiceOverrideSection({
  agent,
  canEdit,
  update,
}: {
  agent: Agent;
  canEdit: boolean;
  update: (data: Record<string, unknown>) => Promise<void>;
}) {
  const { t } = useT("agents");
  const { data: providers = [] } = useQuery(
    voiceProviderListOptions(agent.workspace_id),
  );
  const eligible = useMemo(
    () => providers.filter(voiceProviderChainEligible),
    [providers],
  );

  // The wire represents "unbound" as an empty string (the server field is a
  // plain string, no omitempty), so normalize before the null-check — a raw
  // "" would otherwise read as a bound-but-unknown provider and blank the
  // selector.
  const savedProviderId = agent.voice_provider_id || null;
  const [providerDraft, setProviderDraft] = useState(savedProviderId ?? "none");
  const [modelDraft, setModelDraft] = useState(agent.voice_model ?? "");
  const [advancedDraft, setAdvancedDraft] = useState(
    agent.voice_advanced ? JSON.stringify(agent.voice_advanced, null, 2) : "",
  );
  const [advancedInvalid, setAdvancedInvalid] = useState(false);
  const [busy, setBusy] = useState(false);

  // Re-seed when the saved triple changes underneath (post-save refetch, or
  // a pool edit that unbound the override server-side).
  const savedKey = `${savedProviderId ?? ""}\u0000${agent.voice_model ?? ""}\u0000${JSON.stringify(agent.voice_advanced ?? null)}`;
  const [seenSavedKey, setSeenSavedKey] = useState(savedKey);
  if (savedKey !== seenSavedKey) {
    setSeenSavedKey(savedKey);
    setProviderDraft(savedProviderId ?? "none");
    setModelDraft(agent.voice_model ?? "");
    setAdvancedDraft(
      agent.voice_advanced ? JSON.stringify(agent.voice_advanced, null, 2) : "",
    );
    setAdvancedInvalid(false);
  }

  const run = useCallback(
    async (data: Record<string, unknown>) => {
      setBusy(true);
      try {
        await update(data);
      } catch (error) {
        toast.error(
          error instanceof Error && error.message
            ? error.message
            : t(($) => $.inspector.voice_save_failed_toast),
        );
      } finally {
        setBusy(false);
      }
    },
    [t, update],
  );

  const commitProvider = (id: string) => {
    setProviderDraft(id);
    if (id === "none") {
      void run({ voice_provider_id: "" });
      return;
    }
    const advanced = parseVoiceAdvancedDraft(advancedDraft);
    if (advanced === "invalid") {
      setAdvancedInvalid(true);
      return;
    }
    setAdvancedInvalid(false);
    void run({
      voice_provider_id: id,
      ...(modelDraft.trim() ? { voice_model: modelDraft.trim() } : {}),
      ...(advanced ? { voice_advanced: advanced } : {}),
    });
  };

  const commitModel = () => {
    if (savedProviderId == null) return;
    const next = modelDraft.trim();
    if (next === (agent.voice_model ?? "")) return;
    void run({ voice_model: next });
  };

  const commitAdvanced = () => {
    if (savedProviderId == null) return;
    const parsed = parseVoiceAdvancedDraft(advancedDraft);
    if (parsed === "invalid") {
      setAdvancedInvalid(true);
      return;
    }
    setAdvancedInvalid(false);
    if (parsed === null) {
      // Cleared draft over a stored value sends explicit {} — the PATCH
      // treats an absent/null field as "preserve what is stored".
      if (agent.voice_advanced) void run({ voice_advanced: {} });
      return;
    }
    if (JSON.stringify(parsed) === JSON.stringify(agent.voice_advanced ?? null)) return;
    void run({ voice_advanced: parsed });
  };

  const overrideActive = savedProviderId != null;
  const unbound = providerDraft === "none";

  return (
    <SettingsSection
      title={t(($) => $.inspector.section_voice)}
      description={t(($) => $.inspector.section_voice_hint)}
      action={
        overrideActive && canEdit ? (
          <Button
            variant="ghost"
            size="sm"
            disabled={busy}
            onClick={() => void run({ voice_provider_id: "" })}
          >
            {t(($) => $.inspector.voice_reset_action)}
          </Button>
        ) : null
      }
    >
      <SettingsCard>
        <SettingsRow
          label={t(($) => $.inspector.prop_voice_provider)}
          description={
            eligible.length === 0
              ? t(($) => $.inspector.voice_no_providers_hint)
              : undefined
          }
          size="select-wide"
        >
          <Select
            items={[
              { value: "none", label: t(($) => $.inspector.voice_inherit_option) },
              ...eligible.map((p) => ({ value: p.id, label: p.display_name })),
            ]}
            value={providerDraft}
            // Base UI's change payload is `string | null`; a null carries no
            // item choice of ours (every SelectItem has a string value), so
            // it must not commit a draft — ignore it.
            onValueChange={(value) => {
              if (value != null) commitProvider(value);
            }}
            disabled={!canEdit || busy}
          >
            <SelectTrigger aria-label={t(($) => $.inspector.prop_voice_provider)}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="none">
                {t(($) => $.inspector.voice_inherit_option)}
              </SelectItem>
              {eligible.map((p) => (
                <SelectItem key={p.id} value={p.id}>
                  {p.display_name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </SettingsRow>
        <SettingsRow
          label={t(($) => $.inspector.prop_voice_model)}
          size="text"
        >
          <Input
            type="text"
            value={modelDraft}
            onChange={(e) => setModelDraft(e.target.value)}
            onBlur={commitModel}
            disabled={!canEdit || busy || unbound}
            placeholder={t(($) => $.inspector.voice_model_placeholder_default)}
          />
        </SettingsRow>
        <SettingsRow
          label={t(($) => $.inspector.prop_voice_advanced)}
          size="text"
          align="start"
        >
          <div>
            <Textarea
              value={advancedDraft}
              onChange={(e) => setAdvancedDraft(e.target.value)}
              onBlur={commitAdvanced}
              disabled={!canEdit || busy || unbound}
              rows={3}
              placeholder={t(($) => $.inspector.voice_advanced_placeholder)}
              aria-invalid={advancedInvalid || undefined}
              className="resize-y font-mono text-caption"
            />
            {advancedInvalid ? (
              <p className="mt-1 text-caption text-destructive">
                {t(($) => $.inspector.voice_advanced_invalid)}
              </p>
            ) : null}
          </div>
        </SettingsRow>
      </SettingsCard>
    </SettingsSection>
  );
}

/**
 * Session context gate (RUYI-107). Rendered as its own section because both
 * fields describe one behaviour — when a long-running session is abandoned in
 * favour of a fresh one carrying a prior-context brief.
 *
 * A server predating RUYI-107 omits both fields. Editing then is not merely
 * useless but misleading, so the section states the limitation instead of
 * offering controls whose writes the backend would drop.
 */
function SessionContextSection({
  agent,
  canEdit,
  update,
}: {
  agent: Agent;
  canEdit: boolean;
  update: (data: Record<string, unknown>) => Promise<void>;
}) {
  const { t } = useT("agents");
  const maxContextTokens = agent.session_max_context_tokens;
  const compactPct = agent.session_compact_pct;
  const supported =
    typeof maxContextTokens === "number" && typeof compactPct === "number";

  return (
    <SettingsSection
      title={t(($) => $.inspector.section_session_context)}
      description={t(($) => $.inspector.section_session_context_hint)}
    >
      <SettingsCard>
        {!supported ? (
          <SettingsRow
            label={t(($) => $.inspector.prop_session_max_context_tokens)}
            size="text"
          >
            <p className="text-caption text-muted-foreground">
              {t(($) => $.inspector.session_context_unsupported)}
            </p>
          </SettingsRow>
        ) : (
          <>
            <SettingsRow
              label={t(($) => $.inspector.prop_session_max_context_tokens)}
              size="select-wide"
            >
              <BoundedNumberField
                id="agent-session-max-context-tokens"
                value={maxContextTokens}
                min={AGENT_SESSION_MAX_CONTEXT_TOKENS_MIN}
                max={AGENT_SESSION_MAX_CONTEXT_TOKENS_MAX}
                // Zero sits outside the range on purpose: it is the off switch.
                extraAllowed={AGENT_SESSION_MAX_CONTEXT_TOKENS_DISABLED}
                canEdit={canEdit}
                label={t(($) => $.inspector.prop_session_max_context_tokens)}
                hint={
                  maxContextTokens === AGENT_SESSION_MAX_CONTEXT_TOKENS_DISABLED
                    ? t(($) => $.pickers.session_context_disabled)
                    : t(($) => $.pickers.session_max_context_tokens_range, {
                        min: AGENT_SESSION_MAX_CONTEXT_TOKENS_MIN,
                        max: AGENT_SESSION_MAX_CONTEXT_TOKENS_MAX,
                      })
                }
                onSave={(next) => update({ session_max_context_tokens: next })}
              />
            </SettingsRow>
            <SettingsRow
              label={t(($) => $.inspector.prop_session_compact_pct)}
              size="select-wide"
            >
              <BoundedNumberField
                id="agent-session-compact-pct"
                value={compactPct}
                min={AGENT_SESSION_COMPACT_PCT_MIN}
                max={AGENT_SESSION_COMPACT_PCT_MAX}
                canEdit={
                  canEdit &&
                  maxContextTokens !==
                    AGENT_SESSION_MAX_CONTEXT_TOKENS_DISABLED
                }
                label={t(($) => $.inspector.prop_session_compact_pct)}
                // The hint states the token count the switch REALLY happens at,
                // not the percentage the user typed: the server floors the
                // trigger at 50K, so a low percentage on a small ceiling fires
                // later than the arithmetic suggests, and a hint showing only
                // the range would misdescribe the setting it labels.
                hint={t(($) => $.pickers.session_compact_pct_range, {
                  min: AGENT_SESSION_COMPACT_PCT_MIN,
                  max: AGENT_SESSION_COMPACT_PCT_MAX,
                  threshold: agentSessionEffectiveCompactThreshold(
                    maxContextTokens,
                    compactPct,
                  ),
                })}
                onSave={(next) => update({ session_compact_pct: next })}
              />
            </SettingsRow>
          </>
        )}
      </SettingsCard>
    </SettingsSection>
  );
}

/**
 * Integer field that reverts to the saved value rather than clamping when the
 * draft falls outside its range — clamping would silently store a number the
 * operator never typed. `extraAllowed` admits one sentinel outside the range.
 */
function BoundedNumberField({
  id,
  value,
  min,
  max,
  extraAllowed,
  canEdit,
  label,
  hint,
  onSave,
}: {
  id: string;
  value: number;
  min: number;
  max: number;
  extraAllowed?: number;
  canEdit: boolean;
  label: string;
  hint: string;
  onSave: (next: number) => Promise<void>;
}) {
  const [draft, setDraft] = useState(String(value));

  useEffect(() => setDraft(String(value)), [value]);

  const commit = () => {
    const next = Number(draft);
    const allowed =
      Number.isInteger(next) &&
      ((next >= min && next <= max) || next === extraAllowed);
    if (draft.trim() === "" || !allowed) {
      setDraft(String(value));
      return;
    }
    if (next !== value) void onSave(next);
  };

  return (
    <div>
      <Input
        id={id}
        type="number"
        name={id}
        autoComplete="off"
        inputMode="numeric"
        min={extraAllowed ?? min}
        max={max}
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={commit}
        onKeyDown={(event) => {
          if (isImeComposing(event)) return;
          if (event.key === "Enter") {
            event.preventDefault();
            commit();
          }
        }}
        disabled={!canEdit}
        aria-label={label}
        className="font-mono tabular-nums"
      />
      <p className="mt-1 text-caption text-muted-foreground">{hint}</p>
    </div>
  );
}

function ConcurrencyField({
  value,
  canEdit,
  onSave,
}: {
  value: number;
  canEdit: boolean;
  onSave: (next: number) => Promise<void>;
}) {
  const { t } = useT("agents");
  const [draft, setDraft] = useState(String(value));

  useEffect(() => setDraft(String(value)), [value]);

  const commit = () => {
    const next = Number(draft);
    if (
      !Number.isInteger(next) ||
      (next !== AGENT_MAX_CONCURRENT_TASKS_UNLIMITED &&
        (next < AGENT_MAX_CONCURRENT_TASKS_MIN ||
          next > AGENT_MAX_CONCURRENT_TASKS_MAX))
    ) {
      setDraft(String(value));
      return;
    }
    if (next !== value) void onSave(next);
  };

  return (
    <div>
      <Input
        id="agent-concurrency"
        type="number"
        name="agent-concurrency"
        autoComplete="off"
        inputMode="numeric"
        min={AGENT_MAX_CONCURRENT_TASKS_UNLIMITED}
        max={AGENT_MAX_CONCURRENT_TASKS_MAX}
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={commit}
        onKeyDown={(event) => {
          if (isImeComposing(event)) return;
          if (event.key === "Enter") {
            event.preventDefault();
            commit();
          }
        }}
        disabled={!canEdit}
        aria-label={t(($) => $.inspector.prop_concurrency)}
        className="font-mono tabular-nums"
      />
      <p className="mt-1 text-caption text-muted-foreground">
        {t(($) => $.pickers.concurrency_hint)}
      </p>
    </div>
  );
}
