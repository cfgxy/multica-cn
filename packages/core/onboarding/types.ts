/**
 * Onboarding state types. Mirrors the server-side `user_onboarding`
 * schema defined in docs/onboarding-redesign-proposal.md §4.1 so the
 * store's implementation can later swap from dev-only in-memory state
 * to a TanStack Query against PATCH /api/me/onboarding without
 * changing consumer components.
 */

export type OnboardingStep =
  | "questionnaire"
  | "workspace"
  | "runtime"
  | "agent"
  | "first_issue";

export type TeamSize = "solo" | "team" | "evaluating";

export type UseCase = "coding" | "planning" | "writing" | "explore";

export type PlatformPreference = "web" | "desktop";

export interface QuestionnaireAnswers {
  /**
   * Multi-select of agent provider slugs — values match the ProviderLogo
   * switch in packages/views/runtimes/components/provider-logo.tsx
   * (claude / codex / cursor / copilot / ...). Empty array = "I haven't
   * used any yet" or skipped.
   */
  existing_agents: string[];
  team_size: TeamSize | null;
  use_case: UseCase | null;
}

export interface OnboardingState {
  current_step: OnboardingStep | null;
  onboarded_at: string | null;

  questionnaire: QuestionnaireAnswers;

  workspace_id: string | null;
  runtime_id: string | null;
  agent_id: string | null;
  first_issue_id: string | null;
  onboarding_project_id: string | null;

  platform_preference: PlatformPreference | null;
  cloud_waitlist_email: string | null;
}
