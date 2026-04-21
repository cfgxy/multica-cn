"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import {
  paths,
  resolvePostAuthDestination,
  useHasOnboarded,
} from "@multica/core/paths";
import { workspaceListOptions } from "@multica/core/workspace/queries";
import { CliInstallInstructions, OnboardingFlow } from "@multica/views/onboarding";

/**
 * Web shell for the onboarding flow. The route is the platform chrome on
 * web (matching `WindowOverlay` on desktop); content is the shared
 * `<OnboardingFlow />`. Kept minimal — guard on auth, render, exit.
 *
 * On complete: if a workspace was just created, navigate into it;
 * otherwise fall back to root (proxy / landing picks the user's first ws
 * or bounces to onboarding if still zero).
 *
 * The CLI install card is wired here so its `multica setup` command
 * points at THIS server — dev landing on localhost gets a localhost
 * self-host command, prod cloud gets the plain `multica setup`, prod
 * self-host gets one with explicit URLs. `appUrl` lives in useState
 * so SSR doesn't error on `window` — it fills in on mount.
 */
export default function OnboardingPage() {
  const router = useRouter();
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);
  const hasOnboarded = useHasOnboarded();
  const { data: workspaces = [] } = useQuery({
    ...workspaceListOptions(),
    enabled: !!user && hasOnboarded,
  });
  const [appUrl, setAppUrl] = useState<string | undefined>(undefined);

  useEffect(() => {
    setAppUrl(window.location.origin);
  }, []);

  useEffect(() => {
    if (isLoading || !user) {
      if (!isLoading && !user) router.replace(paths.login());
      return;
    }
    if (hasOnboarded) {
      router.replace(resolvePostAuthDestination(workspaces, hasOnboarded));
    }
  }, [isLoading, user, hasOnboarded, workspaces, router]);

  if (isLoading || !user || hasOnboarded) return null;

  // Layout: page owns its own scroll (root layout sets `body {
  // overflow: hidden }` for the app-shell convention). Content is
  // top-aligned — the previous `my-auto` vertical centering made the
  // baseline jump between steps of different heights. Now every
  // non-welcome step sits below a stable StepHeader anchor; Welcome
  // is the only short step and it provides its own internal
  // centering (see step-welcome.tsx).
  return (
    <div className="h-full overflow-y-auto bg-background">
      <div className="flex min-h-full flex-col items-center px-6 py-12">
        <div className="w-full max-w-xl">
          <OnboardingFlow
            onComplete={(ws, firstIssueId) => {
              if (ws && firstIssueId) {
                router.push(paths.workspace(ws.slug).issueDetail(firstIssueId));
              } else if (ws) {
                router.push(paths.workspace(ws.slug).issues());
              } else {
                router.push(paths.root());
              }
            }}
            runtimeInstructions={
              <CliInstallInstructions
                apiUrl={process.env.NEXT_PUBLIC_API_URL}
                appUrl={appUrl}
              />
            }
          />
        </div>
      </div>
    </div>
  );
}
