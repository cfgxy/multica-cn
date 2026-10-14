"use client";

import { useState } from "react";
import { RefreshCw, Trash2 } from "lucide-react";
import { useRuntimeCache } from "@multica/core/computers";
import { Button } from "@multica/ui/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import {
  AlertDialog,
  AlertDialogTrigger,
  AlertDialogContent,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogCancel,
} from "@multica/ui/components/ui/alert-dialog";
import { ProviderLogo } from "../runtimes/components/provider-logo";
import { useT } from "../i18n";

export function RuntimeCachePanel({ userId }: { userId: string }) {
  const { t } = useT("settings");
  const { cache, check, clear } = useRuntimeCache(userId);
  const [confirm, setConfirm] = useState(false);
  const [page, setPage] = useState(0);
  const d = cache.data;
  const busy = check.isPending || clear.isPending || (d?.downloads ?? 0) > 0;
  const error = cache.error || check.error || clear.error;
  const pageCount = Math.max(1, Math.ceil((d?.entries.length ?? 0) / 20));
  const currentPage = Math.min(page, pageCount - 1);
  const size = (bytes: number) =>
    t(($) => $.runtime_cache.size, {
      size: (bytes / 1024 / 1024).toLocaleString(undefined, {
        maximumFractionDigits: 1,
      }),
    });
  return (
    <section className="space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h2 className="text-title font-semibold">
            {t(($) => $.runtime_cache.title)}
          </h2>
          <p className="mt-1 max-w-2xl text-body text-muted-foreground">
            {t(($) => $.runtime_cache.help)}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            size="sm"
            disabled={busy}
            aria-busy={check.isPending}
            onClick={() => check.mutate()}
          >
            <RefreshCw
              aria-hidden="true"
              className={
                check.isPending ? "motion-safe:animate-spin" : undefined
              }
            />
            {t(($) => $.runtime_cache.refresh)}
          </Button>
          <AlertDialog
            open={confirm}
            onOpenChange={(open) => {
              if (!clear.isPending) setConfirm(open);
            }}
          >
            <AlertDialogTrigger
              render={
                <Button
                  variant="outline"
                  size="sm"
                  disabled={busy || !d?.entries.length}
                />
              }
            >
              <Trash2 aria-hidden="true" />
              {t(($) => $.runtime_cache.clear)}
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>
                  {t(($) => $.runtime_cache.clear)}
                </AlertDialogTitle>
                <AlertDialogDescription>
                  {t(($) => $.runtime_cache.clear_help)}
                </AlertDialogDescription>
              </AlertDialogHeader>
              {clear.error && (
                <p role="alert" className="text-destructive">
                  {clear.error.message}
                </p>
              )}
              <AlertDialogFooter>
                <AlertDialogCancel
                  render={
                    <Button variant="outline" disabled={clear.isPending} />
                  }
                >
                  {t(($) => $.linux_user.cancel)}
                </AlertDialogCancel>
                <Button
                  variant="destructive"
                  disabled={clear.isPending}
                  aria-busy={clear.isPending}
                  onClick={async () => {
                    try {
                      await clear.mutateAsync();
                      setConfirm(false);
                      setPage(0);
                    } catch {
                      /* Keep the error and confirmation visible. */
                    }
                  }}
                >
                  {t(($) => $.runtime_cache.clear)}
                </Button>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </div>
      </div>
      {error && (
        <p role="alert" className="text-destructive">
          {error.message}
        </p>
      )}
      {cache.isPending && <p role="status">{t(($) => $.computers.loading)}</p>}
      {d && (
        <>
          <p className="text-body tabular-nums" role="status">
            {t(($) => $.runtime_cache.summary, {
              used: size(d.bytes),
              limit: size(d.limit_bytes),
              count: d.entries.length,
              downloads: d.downloads,
            })}
          </p>
          <div className="overflow-hidden rounded-lg border">
            <Table aria-label={t(($) => $.admin.runtimes_title)}>
              <TableHeader className="bg-muted/40">
                <TableRow>
                  <TableHead>{t(($) => $.admin.runtimes_title)}</TableHead>
                  <TableHead>
                    {t(($) => $.linux_user_pages.runtime_versions.latest)}
                  </TableHead>
                  <TableHead>{t(($) => $.runtime_cache.coverage)}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {d.catalog.map((runtime) => (
                  <TableRow key={runtime.id}>
                    <TableCell>
                      <span className="flex items-center gap-2">
                        <span aria-hidden="true">
                          <ProviderLogo
                            provider={runtime.id}
                            className="size-4"
                          />
                        </span>
                        {runtime.display_name}
                      </span>
                    </TableCell>
                    <TableCell>
                      {runtime.latest_version_state === "ready"
                        ? runtime.latest_version
                        : runtime.latest_version_state === "checking"
                          ? t(
                              ($) =>
                                $.linux_user_pages.runtime_versions.checking,
                            )
                          : runtime.latest_version_state === "unsupported"
                            ? t(
                                ($) =>
                                  $.linux_user_pages.runtime_versions
                                    .unsupported,
                              )
                            : t(
                                ($) =>
                                  $.linux_user_pages.runtime_versions
                                    .unavailable,
                              )}
                    </TableCell>
                    <TableCell className="whitespace-normal text-caption text-muted-foreground">
                      {t(($) => $.runtime_cache.full)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          <h3 className="text-body font-semibold">
            {t(($) => $.runtime_cache.files)}
          </h3>
          {d.entries.length === 0 ? (
            <p className="text-body text-muted-foreground">
              {t(($) => $.runtime_cache.empty)}
            </p>
          ) : (
            <>
              <div className="overflow-hidden rounded-lg border">
                <Table aria-label={t(($) => $.runtime_cache.files)}>
                  <TableHeader className="bg-muted/40">
                    <TableRow>
                      <TableHead>{t(($) => $.runtime_cache.source)}</TableHead>
                      <TableHead>{t(($) => $.runtime_cache.bytes)}</TableHead>
                      <TableHead>{t(($) => $.runtime_cache.cached)}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {d.entries
                      .slice(currentPage * 20, (currentPage + 1) * 20)
                      .map((entry) => (
                        <TableRow key={entry.id}>
                          <TableCell className="max-w-xl whitespace-normal break-all text-caption">
                            {entry.url}
                          </TableCell>
                          <TableCell className="tabular-nums">
                            {size(entry.size)}
                          </TableCell>
                          <TableCell className="text-caption">
                            {new Date(entry.cached_at).toLocaleString()}
                          </TableCell>
                        </TableRow>
                      ))}
                  </TableBody>
                </Table>
              </div>
              {pageCount > 1 && (
                <div className="flex justify-end gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={currentPage === 0}
                    onClick={() => setPage(currentPage - 1)}
                  >
                    {t(($) => $.runtime_cache.previous)}
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={currentPage + 1 === pageCount}
                    onClick={() => setPage(currentPage + 1)}
                  >
                    {t(($) => $.runtime_cache.next)}
                  </Button>
                </div>
              )}
            </>
          )}
        </>
      )}
    </section>
  );
}
