"use client";

// MCP OAuth consent confirmation page (RUYI-420). The Go authorize endpoint
// parks the validated request in Redis and lands the browser here with
// ?request=<ticket>; the user sees who is asking for what and either approves
// (persisting the grant) or denies (access_denied back at the redirect_uri).
// Standalone client page, same shape as auth/callback.

import { Suspense, useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import { useTranslation } from "react-i18next";
import { api } from "@multica/core/api";
import type { OAuthConsentInfo } from "@multica/core/oauth-admin/types";
import { ApiError } from "@multica/core/api/client";
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
} from "@multica/ui/components/ui/card";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
import { Loader2, ShieldCheck } from "lucide-react";

function ConsentContent() {
  const searchParams = useSearchParams();
  const { t } = useTranslation("settings");
  const requestId = searchParams.get("request") || "";
  const [info, setInfo] = useState<OAuthConsentInfo | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const errorMessages: Record<string, string> = {
    expired: t(($) => $.oauth_consent.expired),
    not_signed_in: t(($) => $.oauth_consent.not_signed_in),
    invalid_request: t(($) => $.oauth_consent.invalid_request),
  };
  const scopeDescriptions: Record<string, string> = {
    "mcp:read": t(($) => $.oauth_consent.scope_read),
    "mcp:write": t(($) => $.oauth_consent.scope_write),
    "mcp:run": t(($) => $.oauth_consent.scope_run),
    mcp: t(($) => $.oauth_consent.scope_legacy),
  };

  useEffect(() => {
    if (requestId === "") {
      setError("invalid_request");
      return;
    }
    let cancelled = false;
    api
      .getOAuthConsent(requestId)
      .then((data) => {
        if (!cancelled) setInfo(data);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof ApiError) setError(err.status === 401 ? "not_signed_in" : "expired");
        else setError("expired");
      });
    return () => {
      cancelled = true;
    };
  }, [requestId]);

  const decide = async (approve: boolean) => {
    if (busy) return;
    setBusy(true);
    try {
      const res = approve
        ? await api.approveOAuthConsent(requestId)
        : await api.denyOAuthConsent(requestId);
      if (res.redirect !== "") {
        window.location.href = res.redirect;
        return;
      }
      setError("expired");
    } catch {
      setError("expired");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex min-h-svh items-center justify-center p-4">
      <Card className="w-full max-w-md">
        <CardHeader>
          <div className="mb-2 flex items-center gap-2 text-muted-foreground">
            <ShieldCheck className="h-5 w-5" />
            <span className="text-caption">{t(($) => $.oauth_consent.brand)}</span>
          </div>
          <CardTitle>
            {error !== null
              ? t(($) => $.oauth_consent.error_title)
              : t(($) => $.oauth_consent.title, { client: info?.client_name || info?.client_id || "" })}
          </CardTitle>
          <CardDescription>
            {error !== null
              ? errorMessages[error] ?? error
              : t(($) => $.oauth_consent.subtitle)}
          </CardDescription>
        </CardHeader>
        {error === null && info !== null && (
          <CardContent className="space-y-5">
            <div className="space-y-2">
              <p className="text-caption font-medium text-muted-foreground">
                {t(($) => $.oauth_consent.scopes_title)}
              </p>
              <ul className="space-y-2">
                {info.scopes.map((scope) => (
                  <li key={scope} className="flex items-start gap-2">
                    <Badge variant="outline" className="mt-0.5 shrink-0 font-mono">{scope}</Badge>
                    <span className="text-caption">{scopeDescriptions[scope] ?? scope}</span>
                  </li>
                ))}
              </ul>
            </div>
            <div className="flex justify-end gap-2">
              <Button variant="outline" disabled={busy} onClick={() => void decide(false)}>
                {t(($) => $.oauth_consent.deny)}
              </Button>
              <Button disabled={busy} onClick={() => void decide(true)}>
                {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
                {t(($) => $.oauth_consent.approve)}
              </Button>
            </div>
          </CardContent>
        )}
      </Card>
    </div>
  );
}

export default function OAuthConsentPage() {
  return (
    <Suspense fallback={null}>
      <ConsentContent />
    </Suspense>
  );
}
