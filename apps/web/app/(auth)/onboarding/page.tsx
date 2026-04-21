"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { useAuthStore } from "@multica/core/auth";
import { paths } from "@multica/core/paths";
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
  const [appUrl, setAppUrl] = useState<string | undefined>(undefined);

  useEffect(() => {
    setAppUrl(window.location.origin);
  }, []);

  useEffect(() => {
    if (!isLoading && !user) router.replace(paths.login());
  }, [isLoading, user, router]);

  if (isLoading || !user) return null;

  // Layout: top-aligned, body scrolls. `min-h-svh` gives short steps
  // (welcome) a full-viewport background so they don't feel cramped.
  // Previous `my-auto` + `items-center` vertical-centering trick broke
  // for long content (questionnaire with many options): the centered
  // block's top could be pushed above the scroll origin, making
  // Continue/Skip unreachable. Top-alignment with natural body scroll
  // is the boring-but-correct baseline.
  return (
    <div className="flex min-h-svh flex-col items-center bg-background px-6 py-12">
      <div className="w-full max-w-xl">
        <OnboardingFlow
          onComplete={(ws) => {
            if (ws) router.push(paths.workspace(ws.slug).issues());
            else router.push(paths.root());
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
  );
}
