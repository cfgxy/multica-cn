"use client";

import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { AlertCircle, Loader2 } from "lucide-react";
import { bootstrapNoRuntimeOnboarding } from "@multica/core/onboarding";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useCurrentWorkspace, paths } from "@multica/core/paths";
import { issueKeys } from "@multica/core/issues/queries";
import { Button } from "@multica/ui/components/ui/button";
import { useNavigation } from "../navigation";
import { useT } from "../i18n";
import { OnboardingHelperModal } from "./onboarding-helper-modal";

/**
 * Single decision point for what the workspace shell does when a user
 * first lands on /<slug>/issues. Reads `me.onboarded_at`,
 * `me.onboarding_runtime_skipped`, and `me.onboarding_runtime_id` to pick
 * exactly one branch:
 *
 *   onboarded_at != null              — already done, run only the
 *                                       install-runtime issue fallback
 *   runtime_skipped === true          — run bootstrapNoRuntimeOnboarding
 *                                       (Branch 1: loading + navigate)
 *   runtime_id != null                — render Modal in dumb mode
 *   none of the above                 — bail back to /onboarding to walk
 *                                       Step 3 again
 *
 * Branch 1 must NOT silently swallow a bootstrap failure — that would
 * leave the user staring at a loading veil forever. On error we render a
 * Retry UI and keep state local so the user can recover without a page
 * reload.
 */
export function WorkspaceOnboardingInit() {
  const me = useAuthStore((s) => s.user);
  const workspace = useCurrentWorkspace();

  if (!me || !workspace) return null;

  // Branch 0: already onboarded. Fire the workspace-content ensure hook
  // once per mount. Server is idempotent (gate + advisory lock) so
  // duplicates from tab switches are cheap no-ops. This back-compat shim
  // replaces the seed calls previously wired into CreateWorkspace /
  // AcceptInvitation / CompleteOnboarding.
  if (me.onboarded_at != null) {
    return <EnsureWorkspaceContent workspaceId={workspace.id} />;
  }

  if (me.onboarding_runtime_skipped === true) {
    return <SkipBootstrapping workspace={workspace} />;
  }

  if (me.onboarding_runtime_id) {
    return (
      <OnboardingHelperModal
        workspace={workspace}
        runtimeId={me.onboarding_runtime_id}
      />
    );
  }

  return <RescueToOnboarding />;
}

/**
 * Branch 1: explicit-Skip user lands here. Runs bootstrapNoRuntimeOnboarding,
 * navigates to the seeded issue on success, surfaces a Retry button on
 * failure. Errors used to silently clear a ref hoping a remount would
 * retry, but the same mount kept rendering the loading veil — user got
 * stuck until a page reload.
 */
function SkipBootstrapping({
  workspace,
}: {
  workspace: { id: string; slug: string };
}) {
  const { t } = useT("onboarding");
  const navigation = useNavigation();
  const qc = useQueryClient();

  // attemptKey: bumping it forces the effect to re-run after the user
  // clicks Retry. inFlight gates against StrictMode double-mount. error
  // captures the most recent failure for the retry UI.
  const [attemptKey, setAttemptKey] = useState(0);
  const inFlightRef = useRef(false);
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
    if (inFlightRef.current) return;
    inFlightRef.current = true;
    let cancelled = false;
    (async () => {
      try {
        const result = await bootstrapNoRuntimeOnboarding(workspace.id);
        await qc.invalidateQueries({ queryKey: issueKeys.all(workspace.id) });
        if (cancelled) return;
        if (result.issue_id) {
          navigation.push(
            paths.workspace(workspace.slug).issueDetail(result.issue_id),
          );
        }
      } catch (err) {
        if (cancelled) return;
        setError(err instanceof Error ? err : new Error(String(err)));
      } finally {
        inFlightRef.current = false;
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [attemptKey, workspace.id, workspace.slug, qc, navigation]);

  if (error) {
    return (
      <div className="fixed inset-0 z-50 flex items-center justify-center bg-background/80 backdrop-blur-sm">
        <div className="flex max-w-md flex-col items-center gap-4 rounded-lg border bg-card p-6 shadow-md">
          <AlertCircle className="h-6 w-6 text-destructive" />
          <p className="text-center text-sm font-medium text-foreground">
            {t(($) => $.workspace_init.error_title)}
          </p>
          <p className="text-center text-xs text-muted-foreground">
            {error.message || t(($) => $.workspace_init.error_generic)}
          </p>
          <Button
            size="sm"
            onClick={() => {
              setError(null);
              setAttemptKey((n) => n + 1);
            }}
          >
            {t(($) => $.workspace_init.retry)}
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-background/80 backdrop-blur-sm">
      <div className="flex flex-col items-center gap-3">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
        <p className="text-sm text-muted-foreground">
          {t(($) => $.workspace_init.setting_up)}
        </p>
      </div>
    </div>
  );
}

/**
 * Branch 3: user reached the workspace without recording either Step 3
 * choice (closed the tab before clicking Pick or Skip). Send them back
 * to /onboarding. Effect (not render-phase navigation) so StrictMode
 * double-mount doesn't dispatch twice in a way React warns about.
 */
function RescueToOnboarding() {
  const navigation = useNavigation();
  useEffect(() => {
    navigation.push(paths.onboarding());
  }, [navigation]);
  return null;
}

/**
 * Renders nothing. Fires `api.ensureOnboardingContent(wsId)` once per
 * workspace id per mount. The server gate (workspace has no runtime AND
 * no existing install-runtime issue) is authoritative — this hook asks;
 * the server decides. Fire-and-forget: success surfaces via WS
 * EventIssueCreated, which the existing issue list subscription consumes.
 */
function EnsureWorkspaceContent({ workspaceId }: { workspaceId: string }) {
  const askedRef = useRef<string | null>(null);
  useEffect(() => {
    if (askedRef.current === workspaceId) return;
    askedRef.current = workspaceId;
    void api.ensureOnboardingContent(workspaceId).catch(() => {
      askedRef.current = null;
    });
  }, [workspaceId]);
  return null;
}
