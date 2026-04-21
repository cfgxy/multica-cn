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

  // Layout notes:
  //  - `min-h-svh` so short steps (welcome) still feel centered, but the
  //    page grows to fit long steps (runtime with many connections) and
  //    the whole window scrolls naturally.
  //  - `my-auto` on the inner block centers it when content is shorter
  //    than the viewport, and lets it sit at the top (with py-12 padding)
  //    when taller. This avoids the trap where `items-center +
  //    justify-center` on an overflowing flex container pushes the
  //    bottom (Continue / Skip) off-screen with no way to scroll to it.
  return (
    <div className="flex min-h-svh flex-col items-center bg-background px-6 py-12">
      <div className="my-auto w-full max-w-xl">
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
