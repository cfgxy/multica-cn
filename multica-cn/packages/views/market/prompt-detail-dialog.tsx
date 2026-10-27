"use client";

import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, Loader2 } from "lucide-react";
import { Alert, AlertDescription } from "@multica/ui/components/ui/alert";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { promptVersionOptions } from "@multica/core/workspace/queries";
import type { PromptMarketItem } from "@multica/core/types";
import { useT } from "../i18n";
import { licenseLabel, promptKindLabel } from "./prompt-market-labels";

/**
 * The detail pane behind a catalog row.
 *
 * The full prompt text arrives only here, never in the listing — a catalog
 * render must not be able to leak the body of every published asset. The
 * source workspace has no field on the payload at all (Owner decision D4), so
 * there is nothing here to accidentally show.
 */
export function PromptDetailDialog({
  open,
  wsId,
  item,
  canInstall,
  onOpenChange,
  onInstall,
  onWithdraw,
}: {
  open: boolean;
  wsId: string;
  item: PromptMarketItem | null;
  canInstall: boolean;
  onOpenChange: (open: boolean) => void;
  onInstall: (item: PromptMarketItem) => void;
  onWithdraw: (item: PromptMarketItem) => void;
}) {
  const { t } = useT("prompt-market");
  const versionQuery = useQuery({
    ...promptVersionOptions(wsId, item?.id ?? ""),
    enabled: open && wsId !== "" && !!item,
  });

  if (!item) return null;

  const detail = versionQuery.data;
  const withdrawn = item.state === "withdrawn";
  // Only a reader who manages the source object gets these fields back, which
  // is exactly the condition for offering a withdrawal.
  const isPublisher = !!detail?.source_id;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{item.name}</DialogTitle>
          <DialogDescription>{item.summary}</DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="secondary">{promptKindLabel(t, item.kind)}</Badge>
            {item.version !== null ? (
              <Badge variant="outline">
                {t(($) => $.badge.version, { version: item.version })}
              </Badge>
            ) : null}
            <Badge variant={item.visibility === "public" ? "default" : "secondary"}>
              {item.visibility === "public"
                ? t(($) => $.badge.public)
                : t(($) => $.badge.private)}
            </Badge>
            {withdrawn ? (
              <Badge variant="secondary">{t(($) => $.badge.withdrawn)}</Badge>
            ) : null}
            {item.installed ? (
              <Badge variant="outline">{t(($) => $.badge.installed)}</Badge>
            ) : null}
          </div>

          {withdrawn ? (
            <Alert variant="destructive">
              <AlertTriangle />
              <AlertDescription>
                {t(($) => $.detail.withdrawn_note)}
              </AlertDescription>
            </Alert>
          ) : null}

          <dl className="grid gap-x-6 gap-y-2 text-caption sm:grid-cols-2">
            <Field label={t(($) => $.detail.publisher_label)}>
              {item.publisher_display_name}
            </Field>
            <Field label={t(($) => $.detail.license)}>
              {licenseLabel(t, item.license_code)}
            </Field>
            {item.audience ? (
              <Field label={t(($) => $.detail.audience)}>{item.audience}</Field>
            ) : null}
            {item.published_at ? (
              <Field label={t(($) => $.detail.published_at)}>
                {item.published_at}
              </Field>
            ) : null}
          </dl>

          {detail?.usage_notes ? (
            <Section title={t(($) => $.detail.usage_notes)}>
              <p className="text-caption leading-5 text-muted-foreground">
                {detail.usage_notes}
              </p>
            </Section>
          ) : null}

          {detail?.companions ? (
            <Section title={t(($) => $.detail.companions)}>
              <p className="text-caption leading-5 text-muted-foreground">
                {detail.companions}
              </p>
              <p className="mt-1 text-caption text-faint-foreground">
                {t(($) => $.detail.companions_note)}
              </p>
            </Section>
          ) : null}

          <Section title={t(($) => $.detail.content)}>
            {versionQuery.isLoading ? (
              <div className="flex items-center justify-center py-8 text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin" />
              </div>
            ) : versionQuery.isError ? (
              <div className="space-y-2 py-4 text-center">
                <p className="text-caption text-muted-foreground">
                  {t(($) => $.detail.load_failed)}
                </p>
                <Button size="sm" variant="outline" onClick={() => versionQuery.refetch()}>
                  {t(($) => $.detail.retry)}
                </Button>
              </div>
            ) : (
              <>
                <pre className="max-h-80 overflow-auto rounded-lg border bg-muted/20 px-3 py-2 font-mono text-caption leading-5 whitespace-pre-wrap">
                  {detail?.content ?? ""}
                </pre>
                {detail?.content_sha256 ? (
                  <p className="mt-1 text-micro text-faint-foreground">
                    {t(($) => $.detail.content_hash, {
                      hash: detail.content_sha256.slice(0, 12),
                    })}
                  </p>
                ) : null}
              </>
            )}
          </Section>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t(($) => $.detail.close)}
          </Button>
          {isPublisher && !withdrawn ? (
            <Button variant="destructive" onClick={() => onWithdraw(item)}>
              {t(($) => $.withdraw.cta)}
            </Button>
          ) : null}
          {/* A withdrawn version keeps its detail readable and loses every
              write affordance — that is the whole difference between a
              withdrawal and a delete. */}
          {canInstall && !withdrawn ? (
            <Button onClick={() => onInstall(item)}>
              {item.update_available
                ? t(($) => $.install.update_cta)
                : t(($) => $.install.cta)}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-caption text-muted-foreground">{label}</dt>
      <dd className="truncate text-body">{children}</dd>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <h3 className="mb-1.5 text-body font-medium">{title}</h3>
      {children}
    </div>
  );
}
