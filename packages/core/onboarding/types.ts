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

export type TeamSize = "solo" | "team" | "evaluating" | "other";

export type Role =
  | "developer"
  | "product_lead"
  | "writer"
  | "founder"
  | "other";

export type UseCase =
  | "coding"
  | "planning"
  | "writing_research"
  | "explore"
  | "other";

export type PlatformPreference = "web" | "desktop";

/**
 * Q1 / Q2 / Q3 answers from the Step 1 questionnaire.
 *
 * Every question is single-select with an explicit `"other"` escape
 * hatch that expands a free-text input (capped at 80 chars). When a
 * user picks a concrete option, the matching `*_other` field stays
 * null; when they pick `"other"`, the `*_other` field is required and
 * stores the user's custom answer.
 *
 * `use_case_other` is special — it gets embedded directly into the
 * Step 5 first-issue prompt, so Other users receive *more* personalized
 * content, not a generic fallback. `team_size_other` / `role_other`
 * are captured for market research only and don't drive any automated
 * routing.
 */
export interface QuestionnaireAnswers {
  team_size: TeamSize | null;
  team_size_other: string | null;
  role: Role | null;
  role_other: string | null;
  use_case: UseCase | null;
  use_case_other: string | null;
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
