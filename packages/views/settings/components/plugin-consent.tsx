"use client";

import { AlertCircle, CalendarClock, Loader2 } from "lucide-react";
import type { PluginPreview } from "@multica/core/types";
import { Alert, AlertDescription, AlertTitle } from "@multica/ui/components/ui/alert";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { useLocale, useT } from "../../i18n";

/**
 * The install consent screen.
 *
 * Shared by the two places a plugin can be installed from — this workspace's
 * own published list, and the instance directory — because the consent is a
 * statement about the code, not about where it was found. Two copies of this
 * screen would eventually diverge, and the one that drifted would be the one
 * showing somebody a shorter scope list than the grant it produces.
 *
 * There is no signature and no trust tier in this model: the administrator
 * reading the raw scope strings IS the trust decision, which is why they are
 * never summarized away.
 */
export function PluginConsent({
  preview,
  installing,
  canInstall,
  onCancel,
  onConfirm,
}: {
  preview: PluginPreview;
  installing: boolean;
  canInstall: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const { t } = useT("settings");
  const scheduledHooks = (preview.manifest.contributes?.hooks ?? []).filter(
    (hook) => hook.schedule !== undefined,
  );

  return (
    <div className="space-y-4">
      <div>
        <div className="text-body font-semibold">{preview.manifest.name}</div>
        <p className="text-caption text-muted-foreground">
          {t(($) => $.plugins.byline, {
            author: preview.manifest.author.name,
            version: preview.version,
          })}
          {preview.installed
            ? t(($) => $.plugins.consent.upgrade_from, { version: preview.installed_version ?? "" })
            : ""}
        </p>
        {preview.manifest.description ? (
          <p className="mt-2 text-caption">{preview.manifest.description}</p>
        ) : null}
      </div>

      <Alert>
        <AlertCircle />
        <AlertTitle>{t(($) => $.plugins.consent.title)}</AlertTitle>
        <AlertDescription>{t(($) => $.plugins.consent.description)}</AlertDescription>
      </Alert>

      <PluginScopeList scopes={preview.scopes} highlighted={preview.added_scopes} />

      {scheduledHooks.length > 0 ? (
        <Alert>
          <CalendarClock />
          <AlertTitle>{t(($) => $.plugins.schedule.consent_title)}</AlertTitle>
          <AlertDescription>
            {t(($) => $.plugins.schedule.consent_description)}
            <PluginScheduleList hooks={scheduledHooks} />
          </AlertDescription>
        </Alert>
      ) : null}

      <div className="flex justify-end gap-2">
        <Button variant="ghost" onClick={onCancel}>
          {t(($) => $.plugins.consent.cancel)}
        </Button>
        <Button disabled={!canInstall || installing} onClick={onConfirm}>
          {installing ? <Loader2 className="animate-spin" /> : null}
          {preview.installed
            ? t(($) => $.plugins.consent.confirm_upgrade)
            : t(($) => $.plugins.consent.confirm)}
        </Button>
      </div>
    </div>
  );
}

/**
 * The scope list is the entire trust model, so it shows the raw scope strings
 * alongside a plain-language line and never summarizes them away.
 */
export function PluginScopeList({
  scopes,
  highlighted,
}: {
  scopes: string[];
  highlighted?: string[];
}) {
  const { t } = useT("settings");
  const added = new Set(highlighted ?? []);
  return (
    <ul className="space-y-1.5">
      {scopes.map((scope) => (
        <li key={scope} className="flex items-baseline gap-2 text-caption">
          <code className="shrink-0 rounded bg-muted px-1.5 py-0.5 font-mono">{scope}</code>
          <span className="text-muted-foreground">{scopeDescription(scope, t)}</span>
          {added.has(scope) ? (
            <Badge variant="secondary">{t(($) => $.plugins.consent.new_scope)}</Badge>
          ) : null}
        </li>
      ))}
    </ul>
  );
}

export type ScheduledHook = {
  key: string;
  name: string;
  schedule?: { cron: string; timezone: string; next_run_at?: string };
};

export function PluginScheduleList({
  hooks,
  showNextRun = false,
}: {
  hooks: ScheduledHook[];
  showNextRun?: boolean;
}) {
  const { t } = useT("settings");
  const locale = useLocale();
  return (
    <ul className="mt-2 space-y-1.5">
      {hooks.map((hook) => (
        <li key={hook.key} className="flex flex-wrap items-baseline gap-2 text-caption">
          <span className="font-medium">{hook.name}</span>
          <span>{scheduleFrequency(hook.schedule?.cron ?? "", t)}</span>
          <code className="rounded bg-muted px-1.5 py-0.5 font-mono">{hook.schedule?.cron ?? ""}</code>
          <span className="text-muted-foreground">{hook.schedule?.timezone ?? ""}</span>
          {showNextRun && hook.schedule?.next_run_at ? (
            <span className="text-muted-foreground">
              {t(($) => $.plugins.schedule.next_run, {
                time: formatScheduleTime(hook.schedule?.next_run_at, locale),
              })}
            </span>
          ) : null}
        </li>
      ))}
    </ul>
  );
}

function formatScheduleTime(value: string | undefined, locale: string): string {
  if (!value) return "—";
  const timestamp = Date.parse(value);
  if (Number.isNaN(timestamp)) return "—";
  return new Intl.DateTimeFormat(locale, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(timestamp));
}

type Translate = ReturnType<typeof useT<"settings">>["t"];

function scheduleFrequency(cron: string, t: Translate): string {
  const fields = cron.trim().split(/\s+/);
  if (fields.length !== 5) return t(($) => $.plugins.schedule.frequency_custom);
  const everyMinutes = /^\*\/(\d+)$/.exec(fields[0] ?? "");
  if (everyMinutes && fields.slice(1).every((field) => field === "*")) {
    return t(($) => $.plugins.schedule.frequency_minutes, { count: Number(everyMinutes[1]) });
  }
  if (/^\d+$/.test(fields[0] ?? "") && fields.slice(1).every((field) => field === "*")) {
    return t(($) => $.plugins.schedule.frequency_hourly, { minute: fields[0] });
  }
  if (
    /^\d+$/.test(fields[0] ?? "") &&
    /^\d+$/.test(fields[1] ?? "") &&
    fields.slice(2).every((field) => field === "*")
  ) {
    return t(($) => $.plugins.schedule.frequency_daily, {
      hour: String(fields[1]).padStart(2, "0"),
      minute: String(fields[0]).padStart(2, "0"),
    });
  }
  return t(($) => $.plugins.schedule.frequency_custom);
}

function scopeDescription(scope: string, t: Translate): string {
  if (scope.startsWith("net:")) {
    return t(($) => $.plugins.scopes.net, { domain: scope.slice("net:".length) });
  }
  switch (scope) {
    case "issues:read": return t(($) => $.plugins.scopes.issues_read);
    case "issues:write": return t(($) => $.plugins.scopes.issues_write);
    case "comments:read": return t(($) => $.plugins.scopes.comments_read);
    case "comments:write": return t(($) => $.plugins.scopes.comments_write);
    case "tasks:read": return t(($) => $.plugins.scopes.tasks_read);
    case "tasks:write": return t(($) => $.plugins.scopes.tasks_write);
    case "agents:read": return t(($) => $.plugins.scopes.agents_read);
    case "members:read": return t(($) => $.plugins.scopes.members_read);
    case "storage:user": return t(($) => $.plugins.scopes.storage_user);
    case "storage:workspace": return t(($) => $.plugins.scopes.storage_workspace);
    default: return t(($) => $.plugins.scopes.unknown);
  }
}
