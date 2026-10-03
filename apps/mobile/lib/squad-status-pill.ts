/**
 * Squad member status → pill mapping (RUYI-346 S2), pure helpers split out
 * of `components/squads/squad-member-row.tsx` so the vitest node lane can
 * cover them (see vitest.config.ts — lib/ is the no-RN-runtime lane).
 *
 * The status pill renders the server-derived five-way bucket from
 * GET /members/status; humans carry status === null and get no pill (same
 * as web), and any UNKNOWN future value also renders no pill rather than a
 * wrong label — the switch's default branch is the neutral fallback.
 */

import type { SquadMemberStatusValue } from "@multica/core/types";

/** Pill descriptor for a derived member status. */
export interface SquadStatusPill {
  /** i18n key under the `squads` namespace. */
  key: string;
  fallback: string;
  tone: "success" | "warning" | "brand" | "muted";
}

/**
 * Server status → pill. null/undefined (human members) and unknown values
 * → null (no pill — neutral fallback; never render an untranslated label).
 */
export function squadMemberStatusPill(
  status: SquadMemberStatusValue | null | undefined,
): SquadStatusPill | null {
  switch (status) {
    case "working":
      return {
        key: "members_tab.status_working",
        fallback: "Working",
        tone: "success",
      };
    case "idle":
      return { key: "members_tab.status_idle", fallback: "Idle", tone: "brand" };
    case "offline":
      return {
        key: "members_tab.status_offline",
        fallback: "Offline",
        tone: "muted",
      };
    case "unstable":
      return {
        key: "members_tab.status_unstable",
        fallback: "Unstable",
        tone: "warning",
      };
    case "archived":
      return {
        key: "members_tab.status_archived",
        fallback: "Archived",
        tone: "muted",
      };
    default:
      return null;
  }
}

/** "last active {{time}}" — absolute short stamp; null when never active. */
export function formatLastActive(
  value: string | null | undefined,
): string | null {
  if (!value) return null;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return null;
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}
