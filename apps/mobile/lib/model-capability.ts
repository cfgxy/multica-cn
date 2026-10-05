/**
 * Model capability lookup — mobile mirror of
 * packages/views/agents/components/inspector/model-capability.ts. The views
 * module is not on the mobile sharing whitelist, so the normalization and
 * catalog-entry resolution are copied here; keep both sides in step.
 */
import type { RuntimeModel } from "@multica/core/types";

// Claude Code appends a context-window modifier to some runtime-native model
// IDs (for example, claude-opus-5[1m]). Restrict inheritance to a numeric
// context size so arbitrary bracketed variants remain fail-closed.
const CLAUDE_CONTEXT_WINDOW_TAG = /\[[1-9]\d*[km]\]$/;

export function modelIdForCapabilityLookup(
  provider: string,
  model: string,
): string {
  return provider === "claude"
    ? model.replace(CLAUDE_CONTEXT_WINDOW_TAG, "")
    : model;
}

/**
 * Resolves the catalog entry used for capability display and cleanup. The raw
 * model remains the value persisted and sent to the runtime; only this lookup
 * identity is normalized. Both sides of the comparison run through the same
 * normalization because Claude discovery reports the tag included.
 */
export function findModelCapabilityEntry(
  models: readonly RuntimeModel[],
  model: string,
  provider: string,
): RuntimeModel | undefined {
  if (!model) return undefined;
  const lookupId = modelIdForCapabilityLookup(provider, model);
  return models.find(
    (entry) => modelIdForCapabilityLookup(provider, entry.id) === lookupId,
  );
}

/**
 * Resolves the catalog entry for capability preview when the form's model
 * field may be empty. Empty model = "follow the runtime's own default": for
 * codex that default can be any installed model, so preview fails closed
 * (mirrors web's pickModelEntry / backend ValidateThinkingLevel, MUL-4347).
 */
export function pickModelEntryForPreview(
  models: readonly RuntimeModel[],
  model: string,
  provider: string,
): RuntimeModel | undefined {
  if (model) return findModelCapabilityEntry(models, model, provider);
  if (provider === "codex") return undefined;
  return models.find((m) => m.default) ?? models[0];
}

/**
 * Vocabulary for a claude model the catalog cannot resolve — mirrors the
 * server's ValidateThinkingLevelWith subset for unresolvable claude models.
 */
export const CLAUDE_FALLBACK_THINKING_LEVELS = [
  { value: "low", label: "Low" },
  { value: "medium", label: "Medium" },
  { value: "high", label: "High" },
] as const;

export interface ThinkingLevelOption {
  value: string;
  label: string;
}

/**
 * Thinking vocabulary for the current (provider, model) — web
 * ThinkingPropRow.resolveThinkingLevels semantics:
 * catalog entry levels win; a claude model with NO entry falls back to the
 * static subset; an entry that exists with no levels stays empty (the catalog
 * answered "this model takes no effort"); no catalog answer → empty.
 */
export function resolveThinkingLevels(
  models: readonly RuntimeModel[] | undefined,
  model: string,
  provider: string,
): ThinkingLevelOption[] {
  if (!models) return [];
  const entry = pickModelEntryForPreview(models, model, provider);
  const levels = entry?.thinking?.supported_levels ?? [];
  if (levels.length > 0) return levels.map((l) => ({ value: l.value, label: l.label }));
  if (!entry && provider === "claude") {
    return CLAUDE_FALLBACK_THINKING_LEVELS.map((l) => ({ ...l }));
  }
  return [];
}
