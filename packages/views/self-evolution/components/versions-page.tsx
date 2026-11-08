"use client";

import { LegacySelfEvolutionTabRedirect } from "./legacy-tab-redirect";

/** Keeps existing version links working while the page moves into quality tabs. */
export function VersionsPage() {
  return <LegacySelfEvolutionTabRedirect destination="versions" />;
}
