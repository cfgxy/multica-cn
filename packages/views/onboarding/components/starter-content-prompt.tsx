"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useNavigation } from "@multica/views/navigation";
import { useCurrentWorkspace } from "@multica/core/paths";
import { paths } from "@multica/core/paths";
import { agentListOptions } from "@multica/core/workspace/queries";
import type { QuestionnaireAnswers } from "@multica/core/onboarding";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { buildImportPayload } from "../utils/starter-content-templates";

/**
 * Post-onboarding opt-in dialog.
 *
 * Shown exactly once per user, on the first workspace landing where
 * `user.starter_content_state === null` (i.e. the server has never
 * recorded a decision). The dialog is mandatory — there's no X,
 * clicking outside doesn't dismiss, ESC doesn't dismiss; the only
 * exits are Import or No thanks. Both are terminal state transitions
 * on the server (NULL → 'imported' or NULL → 'dismissed') so the
 * dialog will never reappear on a subsequent visit.
 *
 * Why these two exits are the only way to close:
 *   - We want every user to consciously answer "do you want the tour?"
 *   - A passive dismissal (ESC, click-outside) leaves the state in
 *     a half-decided place; re-showing on next visit would feel
 *     like nagging.
 *
 * The two branches of Import are decided by inspecting the workspace's
 * agent list (not the onboarding flow state): if any agent exists, the
 * agent-guided template is used (welcome issue + agent-guided sub-issues);
 * otherwise the self-serve template (no welcome issue; first sub-issue is
 * "install a runtime"). This makes path A (Welcome skip with existing
 * workspace) work correctly too — we key off current reality, not how
 * the user got here.
 */
export function StarterContentPrompt() {
  const workspace = useCurrentWorkspace();
  const user = useAuthStore((s) => s.user);
  const refreshMe = useAuthStore((s) => s.refreshMe);
  const { push } = useNavigation();

  // Pull the agent list for this workspace. We need this to decide
  // between the agent-guided and self-serve templates. The query runs
  // only when we actually need to show the dialog; otherwise the
  // component is a no-op.
  const wsId = workspace?.id ?? "";
  const shouldShow =
    !!user &&
    !!workspace &&
    user.onboarded_at != null &&
    user.starter_content_state == null;

  const { data: agents = [], isFetched: agentsFetched } = useQuery({
    ...agentListOptions(wsId),
    enabled: shouldShow,
  });

  const [submitting, setSubmitting] = useState<"import" | "dismiss" | null>(
    null,
  );

  if (!shouldShow || !workspace || !user) return null;

  // Default to the self-serve branch until we've actually loaded the
  // agent list — this matters because the copy reads slightly
  // differently on the two branches. If we render before agents are
  // fetched, show a light "Loading…" beat inside the dialog so the
  // user doesn't see copy that later swaps under them.
  const agent = agents[0];
  const hasAgent = agents.length > 0;

  const onImport = async () => {
    if (submitting) return;
    setSubmitting("import");
    try {
      const questionnaire = mergeQuestionnaire(user.onboarding_questionnaire);
      const payload = buildImportPayload({
        workspaceId: workspace.id,
        userName: user.name || user.email,
        questionnaire,
        agentId: hasAgent ? agent!.id : null,
      });
      const result = await api.importStarterContent(payload);

      // Sync the new starter_content_state into the auth store so this
      // component unmounts cleanly on the next render.
      await refreshMe();

      toast.success(
        hasAgent
          ? "Welcome tour ready — opening your first issue"
          : "Getting Started project created — check the sidebar",
      );

      if (result.welcome_issue_id) {
        push(paths.workspace(workspace.slug).issueDetail(result.welcome_issue_id));
      }
      // Self-serve path: stay on issues list. The new Getting Started
      // project and its sub-issues appear via realtime event invalidation.
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : "Import failed — please retry",
      );
      setSubmitting(null);
    }
  };

  const onDismiss = async () => {
    if (submitting) return;
    setSubmitting("dismiss");
    try {
      await api.dismissStarterContent();
      await refreshMe();
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : "Could not dismiss — please retry",
      );
      setSubmitting(null);
    }
  };

  return (
    <Dialog
      open
      // `disablePointerDismissal` stops outside-click close; the
      // `onOpenChange` handler cancels Base UI's ESC-close path
      // via `eventDetails.cancel()`. Together these make Import /
      // No thanks the only exits — matching the server-side NULL
      // state gate (any second call returns 409 Conflict).
      disablePointerDismissal
      onOpenChange={(_open, eventDetails) => {
        eventDetails.cancel();
      }}
    >
      <DialogContent
        showCloseButton={false}
        className="sm:max-w-[440px]"
      >
        <DialogHeader>
          <DialogTitle className="text-balance font-serif text-[22px] leading-[1.2] font-medium tracking-tight">
            Welcome — add starter tasks?
          </DialogTitle>
          <DialogDescription className="pt-2 text-[14px] leading-[1.55]">
            {!agentsFetched ? (
              <>Loading your workspace…</>
            ) : hasAgent ? (
              <>
                A{" "}
                <span className="font-medium text-foreground">
                  Getting Started
                </span>{" "}
                project with short tasks that teach how agents, issues,
                and context fit together — plus a welcome issue{" "}
                <span className="font-medium text-foreground">
                  {agent!.name}
                </span>{" "}
                replies to right away.
              </>
            ) : (
              <>
                A{" "}
                <span className="font-medium text-foreground">
                  Getting Started
                </span>{" "}
                project with short tasks that teach how runtimes, agents,
                and issues fit together in Multica.
              </>
            )}
          </DialogDescription>
        </DialogHeader>

        <DialogFooter className="mt-2 gap-2 sm:justify-end">
          <Button
            variant="ghost"
            onClick={onDismiss}
            disabled={submitting !== null || !agentsFetched}
          >
            {submitting === "dismiss" && (
              <Loader2 className="h-4 w-4 animate-spin" />
            )}
            Start blank workspace
          </Button>
          <Button
            onClick={onImport}
            disabled={submitting !== null || !agentsFetched}
          >
            {submitting === "import" && (
              <Loader2 className="h-4 w-4 animate-spin" />
            )}
            Add starter tasks
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// Local helper — mirrors the onboarding flow's mergeQuestionnaire.
// Kept private to this component so the view package doesn't grow a
// second public API for the same concept.
function mergeQuestionnaire(
  raw: Record<string, unknown>,
): QuestionnaireAnswers {
  const empty: QuestionnaireAnswers = {
    team_size: null,
    team_size_other: null,
    role: null,
    role_other: null,
    use_case: null,
    use_case_other: null,
  };
  return { ...empty, ...(raw as Partial<QuestionnaireAnswers>) };
}
