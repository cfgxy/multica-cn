"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Store, Upload } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { useFeatureEnabled } from "@multica/core/config";
import { MARKETPLACE_V1_FLAG } from "@multica/core/feature-flags";
import { createSafeId } from "@multica/core/utils";
import {
  promptSourceVersionsOptions,
  promptTargetStateOptions,
} from "@multica/core/workspace/queries";
import { useRestorePrompt } from "@multica/core/workspace/mutations";
import { useT } from "../i18n";
import { PromptRestoreDialog } from "./prompt-confirm-dialogs";
import { PromptPublishDialog } from "./prompt-publish-dialog";

/**
 * The market strip above an agent's or squad's prompt editor.
 *
 * With the flag off this renders null before it issues a single request — the
 * regression this guards is not a stray button but a prompt tab that starts
 * failing two network calls on every server where the market is not enabled.
 *
 * The restore button is hidden, not disabled, once `can_restore` is false.
 * `applied_content_intact` goes false the moment someone edits the prompt by
 * hand after an apply, and at that point a restore would discard that edit —
 * the server refuses, and offering a button that only ever errors would be
 * worse than not offering one.
 */
export function PromptMarketStatusStrip(props: {
  wsId: string;
  targetType: "agent" | "squad";
  targetId: string;
  targetName: string;
  canManage: boolean;
  /**
   * True while the editor holds text the server has not stored.
   *
   * A publish snapshots the persisted prompt, never the draft on screen, so
   * publishing here would silently ship the previous text. The strip says so
   * and blocks the button rather than letting that happen quietly.
   */
  hasUnsavedEdits?: boolean;
}) {
  const enabled = useFeatureEnabled(MARKETPLACE_V1_FLAG, false);
  // The gate is a separate component so the inactive path calls no query hook
  // at all. `enabled: false` would still require a QueryClient in context, and
  // the prompt editors this mounts into are leaves that render without one.
  if (!enabled || !props.canManage || props.wsId === "" || props.targetId === "") {
    return null;
  }
  return <StatusStripBody {...props} />;
}

function StatusStripBody({
  wsId,
  targetType,
  targetId,
  targetName,
  hasUnsavedEdits = false,
}: {
  wsId: string;
  targetType: "agent" | "squad";
  targetId: string;
  targetName: string;
  canManage: boolean;
  hasUnsavedEdits?: boolean;
}) {
  const { t } = useT("prompt-market");
  const [publishOpen, setPublishOpen] = useState(false);
  const [restoreOpen, setRestoreOpen] = useState(false);

  const stateQuery = useQuery(promptTargetStateOptions(wsId, targetType, targetId));
  const versionsQuery = useQuery(
    promptSourceVersionsOptions(wsId, targetType, targetId),
  );
  const restore = useRestorePrompt(wsId);

  const state = stateQuery.data;
  const applied = state?.applied_version !== null && state?.applied_version !== undefined;
  const published = (versionsQuery.data ?? []).filter(
    (version) => version.state === "published",
  );
  const latestPublished = published.reduce<number | null>(
    (max, version) =>
      version.version !== null && (max === null || version.version > max)
        ? version.version
        : max,
    null,
  );
  const seriesId = published[0]?.series_id;

  const runRestore = async () => {
    try {
      await restore.mutateAsync({
        targetType,
        targetId,
        operation_id: createSafeId(),
        // Sending the hash the strip was rendered from turns a race into a
        // refusal instead of a silent discard of someone else's edit.
        expected_sha256: state?.current_sha256,
      });
      toast.success(t(($) => $.restore.done_toast));
      setRestoreOpen(false);
    } catch {
      // The server refuses a restore whose target moved; that refusal has its
      // own message, distinct from a generic failure.
      toast.error(
        state?.applied_content_intact === false
          ? t(($) => $.restore.denied_toast)
          : t(($) => $.restore.failed_toast),
      );
      setRestoreOpen(false);
    }
  };

  return (
    <>
      <div className="mb-2 flex flex-wrap items-center gap-2 rounded-lg border border-surface-border bg-muted/20 px-3 py-2">
        <Store className="h-4 w-4 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 text-caption text-muted-foreground">
          {applied
            ? state?.applied_content_intact
              ? t(($) => $.status_strip.applied, {
                  name: targetName,
                  version: state?.applied_version ?? 1,
                })
              : t(($) => $.status_strip.applied_edited, {
                  name: targetName,
                  version: state?.applied_version ?? 1,
                })
            : latestPublished !== null
              ? t(($) => $.status_strip.published, { version: latestPublished })
              : null}
        </span>

        {latestPublished !== null ? (
          <Badge variant="outline">
            {t(($) => $.badge.version, { version: latestPublished })}
          </Badge>
        ) : null}

        {applied && state?.can_restore ? (
          <Button size="sm" variant="outline" onClick={() => setRestoreOpen(true)}>
            {t(($) => $.restore.cta)}
          </Button>
        ) : null}

        <Button
          size="sm"
          variant="outline"
          disabled={hasUnsavedEdits}
          onClick={() => setPublishOpen(true)}
        >
          <Upload className="h-3.5 w-3.5" />
          {latestPublished !== null
            ? t(($) => $.status_strip.publish_new_version)
            : t(($) => $.publish.cta)}
        </Button>

        {hasUnsavedEdits ? (
          <p className="w-full text-caption text-muted-foreground">
            {t(($) => $.status_strip.unsaved_note)}
          </p>
        ) : null}
      </div>

      <PromptPublishDialog
        open={publishOpen}
        wsId={wsId}
        sourceType={targetType}
        sourceId={targetId}
        seriesId={seriesId}
        defaultName={targetName}
        onOpenChange={setPublishOpen}
      />
      <PromptRestoreDialog
        open={restoreOpen}
        pending={restore.isPending}
        onOpenChange={setRestoreOpen}
        onConfirm={() => void runRestore()}
      />
    </>
  );
}
