"use client";

import { LegacySelfEvolutionTabRedirect } from "./legacy-tab-redirect";

/**
 * Keeps existing proposal links working while the page moves into clause tabs.
 */
export function ProposalsPage() {
  return <LegacySelfEvolutionTabRedirect destination="proposals" />;
}
