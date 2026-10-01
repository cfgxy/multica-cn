"use client";

import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import type {
  RuntimeModel,
  RuntimeModelThinkingLevel,
} from "@multica/core/types";
import { runtimeModelsOptions } from "@multica/core/runtimes";
import { PropRow } from "../../../common/prop-row";
import { SettingsRow } from "../../../settings/components/settings-layout";
import { useT } from "../../../i18n";
import { ThinkingPicker } from "./thinking-picker";
import { findModelCapabilityEntry } from "./model-capability";

/**
 * Vocabulary offered for a claude model the daemon catalog cannot resolve —
 * not in `models` at all (a release the static list hasn't caught up with, an
 * org-proxy alias). Mirrors the server side: ValidateThinkingLevelWith accepts
 * exactly this subset for unresolvable claude models, so the picker never
 * offers a level the daemon would warn-and-drop at run time. Deliberately
 * narrower than the CLI's full superset — xhigh/max would dangle on models
 * that reject them (e.g. Haiku).
 */
const CLAUDE_FALLBACK_THINKING_LEVELS: RuntimeModelThinkingLevel[] = [
  { value: "low", label: "Low" },
  { value: "medium", label: "Medium" },
  { value: "high", label: "High" },
];

/**
 * Resolves the picker vocabulary for the agent's current (provider, model).
 *
 * - Catalog answered and has the model → the model's own levels.
 * - Catalog answered but has no entry for the model → claude falls back to
 *   CLAUDE_FALLBACK_THINKING_LEVELS; every other provider stays empty.
 *   An entry that EXISTS with no thinking vocabulary is the catalog answering
 *   "this model takes no effort" (old CLI, or a live row with effort off), so
 *   it stays empty rather than borrowing the fallback.
 * - Catalog hasn't answered (runtime offline, query disabled) → empty, so an
 *   offline agent doesn't preview levels we can't verify.
 */
function resolveThinkingLevels(
  models: RuntimeModel[] | undefined,
  model: string,
  provider: string,
): RuntimeModelThinkingLevel[] {
  if (!models) return [];
  const entry = pickModelEntry(models, model, provider);
  const levels = entry?.thinking?.supported_levels ?? [];
  if (levels.length > 0) return levels;
  if (!entry && provider === "claude") {
    return CLAUDE_FALLBACK_THINKING_LEVELS;
  }
  return levels;
}

/**
 * Thinking row for the agent inspector. Hidden when the active model has
 * no `supported_levels` advertised AND nothing is persisted, so providers
 * that don't expose reasoning never surface an empty row. If the agent
 * already has a `thinking_level` saved (model swap into a non-thinking
 * runtime, or the daemon / CLI catalog shrank and dropped the entry),
 * we still render the row so the user can see the orphan token the
 * backend is still sending and explicit-clear it via the picker footer.
 * PR1's per-model invalid behavior is daemon-side warn/drop, not a
 * synchronous DB clear, so the frontend has to surface the persisted
 * state honestly.
 *
 * Reuses the shared runtime-models query so it hits the same 60s cache
 * as the model picker; no extra round-trip on the inspector's hot path.
 * The sibling ModelPicker mounts unconditionally next to this row, so
 * the shared query subscription is established by the inspector mount
 * itself — returning null here does NOT cancel discovery.
 */
export function ThinkingPropRow({
  runtimeId,
  runtimeOnline,
  provider,
  model,
  value,
  canEdit,
  onChange,
}: {
  runtimeId: string | null;
  runtimeOnline: boolean;
  /** Runtime provider type (e.g. "codex", "claude"). Used to decide whether an
   *  empty model can safely preview a default model's effort catalog. */
  provider: string;
  model: string;
  value: string;
  canEdit: boolean;
  onChange: (next: string) => Promise<void> | void;
}) {
  const { t } = useT("agents");
  const modelsQuery = useQuery(
    runtimeModelsOptions(runtimeOnline ? runtimeId : null),
  );

  const levels = resolveThinkingLevels(
    modelsQuery.data?.models,
    model,
    provider,
  );
  if (levels.length === 0 && !value) return null;

  return (
    <PropRow label={t(($) => $.inspector.prop_thinking)} interactive={false}>
      <ThinkingPicker
        value={value}
        levels={levels}
        canEdit={canEdit}
        onChange={onChange}
      />
    </PropRow>
  );
}

/** Full-width counterpart used by the General settings form. */
export function ThinkingSettingField({
  label,
  runtimeId,
  runtimeOnline,
  provider,
  model,
  value,
  canEdit,
  onChange,
}: {
  label: ReactNode;
  runtimeId: string | null;
  runtimeOnline: boolean;
  provider: string;
  model: string;
  value: string;
  canEdit: boolean;
  onChange: (next: string) => Promise<void> | void;
}) {
  const modelsQuery = useQuery(
    runtimeModelsOptions(runtimeOnline ? runtimeId : null),
  );
  const levels = resolveThinkingLevels(
    modelsQuery.data?.models,
    model,
    provider,
  );

  if (levels.length === 0 && !value) return null;

  return (
    <SettingsRow label={label} size="select-wide">
      <ThinkingPicker
        variant="field"
        showLabel={false}
        value={value}
        levels={levels}
        canEdit={canEdit}
        onChange={onChange}
      />
    </SettingsRow>
  );
}

function pickModelEntry(
  models: RuntimeModel[],
  model: string,
  provider: string,
): RuntimeModel | undefined {
  if (model) return findModelCapabilityEntry(models, model, provider);
  // Empty model = "follow the runtime's own default". For codex that default
  // comes from the local config.toml and can be any installed model, so we
  // must NOT preview the flagged Default entry's effort catalog — gpt-5.6-sol
  // alone advertises `ultra`, which the actually-configured model may not
  // support. Fail closed (no preview): the row hides unless a stale level is
  // persisted, in which case it still renders so the orphan can be cleared.
  // Mirrors the backend ValidateThinkingLevel. (MUL-4347)
  if (provider === "codex") return undefined;
  return models.find((m) => m.default) ?? models[0];
}
