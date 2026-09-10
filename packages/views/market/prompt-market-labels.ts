/**
 * Label helpers shared by the prompt market surfaces (RUYI-100).
 *
 * Every one of these takes a plain server string rather than a union type, and
 * every one falls back to rendering that string as itself. A newer backend that
 * adds a licence or a kind must degrade to "shows an unfamiliar word" — never
 * to a blank field, which would read as "this prompt has no licence".
 */

import type { TFunction } from "i18next";

type PromptMarketT = TFunction<"prompt-market">;

const LICENSE_KEYS = [
  "cc0",
  "cc-by-4.0",
  "internal-only",
  "all-rights-reserved",
] as const;

export function licenseLabel(t: PromptMarketT, code: string): string {
  switch (code) {
    case "cc0":
      return t(($) => $.license["cc0"]);
    case "cc-by-4.0":
      return t(($) => $.license["cc-by-4.0"]);
    case "internal-only":
      return t(($) => $.license["internal-only"]);
    case "all-rights-reserved":
      return t(($) => $.license["all-rights-reserved"]);
    default:
      return code;
  }
}

export const LICENSE_CODES: readonly string[] = LICENSE_KEYS;

export function promptKindLabel(t: PromptMarketT, kind: string): string {
  switch (kind) {
    case "agent_prompt":
      return t(($) => $.kind.agent_prompt);
    case "squad_prompt":
      return t(($) => $.kind.squad_prompt);
    default:
      return kind;
  }
}

/** The object kind a prompt of this kind can be applied to. */
export function targetTypeForKind(kind: string): "agent" | "squad" | null {
  if (kind === "agent_prompt") return "agent";
  if (kind === "squad_prompt") return "squad";
  return null;
}

/** The asset kind published from an agent or a squad. */
export function kindForSourceType(sourceType: "agent" | "squad"): string {
  return sourceType === "agent" ? "agent_prompt" : "squad_prompt";
}
