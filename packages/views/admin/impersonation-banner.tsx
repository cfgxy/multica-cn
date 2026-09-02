"use client";

import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ShieldAlert, X } from "lucide-react";
import { useAuthStore } from "@multica/core/auth";
import { api } from "@multica/core/api";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";

/**
 * Global banner shown while the session carries an impersonation shadow
 * JWT (RUYI-47). Server-set /api/me impersonator_id is the only trigger —
 * a client cannot forge the state, because the banner's exit action just
 * re-mints a real login token and the next /api/me loses the field anyway.
 *
 * Mount once per app shell (web dashboard layout, desktop shell, and the
 * /admin page) so the identity is visible on every screen.
 */
export function ImpersonationBanner({ className }: { className?: string }) {
  const { t } = useTranslation("admin");
  const user = useAuthStore((s) => s.user);
  const applySession = useAuthStore((s) => s.applySession);
  const [stopping, setStopping] = useState(false);
  const [failed, setFailed] = useState(false);

  // Explicit boolean check per API-compat rules: only an actual
  // impersonation session renders the banner.
  if (user === null || user.impersonator_id === null || user.impersonator_id === undefined) {
    return null;
  }

  const targetName = user.name || user.email;

  const stop = async () => {
    setStopping(true);
    setFailed(false);
    try {
      const { token, user: restored } = await api.stopImpersonation();
      applySession(token, restored);
    } catch {
      setFailed(true);
    } finally {
      setStopping(false);
    }
  };

  return (
    <div
      role="status"
      data-testid="impersonation-banner"
      className={cn(
        "flex h-9 shrink-0 items-center justify-center gap-2 bg-amber-500/15 px-4 text-body text-amber-700 dark:text-amber-400",
        className,
      )}
    >
      <ShieldAlert className="size-4 shrink-0" aria-hidden />
      <span className="truncate">
        {t(($) => $.banner.impersonating, { name: targetName })}
      </span>
      <Button
        variant="ghost"
        size="sm"
        className="h-6 gap-1 px-2 text-body"
        disabled={stopping}
        onClick={() => void stop()}
      >
        <X className="size-3.5" aria-hidden />
        {stopping ? t(($) => $.banner.stopping) : t(($) => $.banner.stop)}
      </Button>
      {failed === true && (
        <span className="text-caption text-destructive">
          {t(($) => $.banner.stop_failed)}
        </span>
      )}
    </div>
  );
}
