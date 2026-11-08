"use client";

import { LegacySelfEvolutionTabRedirect } from "./legacy-tab-redirect";

/** Keeps existing quiz links working while the page moves into quality tabs. */
export function QuizPage() {
  return <LegacySelfEvolutionTabRedirect destination="quiz" />;
}
