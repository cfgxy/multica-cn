"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, Check, Loader2 } from "lucide-react";
import { Alert, AlertDescription, AlertTitle } from "@multica/ui/components/ui/alert";
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
import { ApiError } from "@multica/core/api";
import { useCurrentMember } from "@multica/core/permissions";
import { createSafeId } from "@multica/core/utils";
import {
  agentListOptions,
  squadListOptions,
} from "@multica/core/workspace/queries";
import {
  useApplyPrompt,
  usePreviewPromptApply,
} from "@multica/core/workspace/mutations";
import type {
  PromptApplyPreview,
  PromptApplyStrategy,
  PromptInstall,
} from "@multica/core/types";
import { useT } from "../i18n";
import { ChoiceList } from "./choice-list";
import { PromptDiffView } from "./prompt-diff-view";
import { promptKindLabel, targetTypeForKind } from "./prompt-market-labels";

type Step = "target" | "review" | "done";

interface TargetOption {
  id: string;
  name: string;
}

/**
 * The apply wizard: choose a target, look at what would change, confirm.
 *
 * Two rules drive the whole component and neither is negotiable.
 *
 * The diff is rendered ONLY from the preview response's `current_content` and
 * `incoming_content`. The obvious shortcut — diffing against whatever the
 * agent's instructions editor happens to hold in memory — would compare
 * against an unsaved draft and show a user an overwrite that is not the one
 * they are about to perform.
 *
 * The preview token is the proof that a human saw THIS diff. When the target's
 * text moves underneath (409 `prompt_preview_stale`), the wizard does not
 * retry with a fresh token; it drops back to a re-preview, because a silent
 * retry would apply against a comparison nobody ever looked at.
 */
export function PromptApplyDialog({
  open,
  wsId,
  install,
  onOpenChange,
}: {
  open: boolean;
  wsId: string;
  install: PromptInstall | null;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("prompt-market");
  const targetType = targetTypeForKind(install?.kind ?? "");

  const [step, setStep] = useState<Step>("target");
  const [targetId, setTargetId] = useState("");
  const [preview, setPreview] = useState<PromptApplyPreview | null>(null);
  const [strategy, setStrategy] = useState<PromptApplyStrategy>("preserve");
  const [stale, setStale] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [appliedVersion, setAppliedVersion] = useState<number | null>(null);

  const currentMember = useCurrentMember(wsId);
  const isWorkspaceManager =
    currentMember.role === "owner" || currentMember.role === "admin";

  const agents = useQuery({
    ...agentListOptions(wsId),
    enabled: open && wsId !== "" && targetType === "agent",
  });
  const squads = useQuery({
    ...squadListOptions(wsId),
    enabled: open && wsId !== "" && targetType === "squad",
  });

  const previewMutation = usePreviewPromptApply();
  const applyMutation = useApplyPrompt(wsId);

  // Reopening on a different install must not inherit the previous run's
  // preview token — that token authorises a write to another target.
  useEffect(() => {
    if (open) {
      setStep("target");
      setTargetId("");
      setPreview(null);
      setStrategy("preserve");
      setStale(false);
      setFailure(null);
      setAppliedVersion(null);
    }
  }, [open, install?.id]);

  const targets: TargetOption[] = useMemo(() => {
    if (targetType === "agent") {
      return (agents.data ?? [])
        .filter((agent) => !agent.archived_at)
        .map((agent) => ({ id: agent.id, name: agent.name }));
    }
    if (targetType === "squad") {
      return (squads.data ?? [])
        .filter((squad) => !squad.archived_at)
        .map((squad) => ({ id: squad.id, name: squad.name }));
    }
    return [];
  }, [targetType, agents.data, squads.data]);

  if (!install || !targetType) return null;

  const targetsLoading = agents.isLoading || squads.isLoading;

  const runPreview = async (nextTargetId: string) => {
    setFailure(null);
    setStale(false);
    try {
      const result = await previewMutation.mutateAsync({
        installId: install.id,
        target_type: targetType,
        target_id: nextTargetId,
      });
      setPreview(result);
      // Preserve stays the default even when an overwrite is the only useful
      // outcome: choosing to destroy text is the user's action, not a
      // pre-selected one.
      setStrategy("preserve");
      setStep("review");
    } catch (error) {
      setFailure(errorMessage(error, t(($) => $.apply.failed_title)));
    }
  };

  const runApply = async () => {
    if (!preview) return;
    setFailure(null);
    try {
      const result = await applyMutation.mutateAsync({
        installId: install.id,
        target_type: targetType,
        target_id: targetId,
        strategy,
        preview_token: preview.preview_token,
        // Sending the hash the diff was drawn from makes the server reject an
        // apply the user could not have seen, even if the token were still
        // nominally valid.
        expected_sha256: preview.current_sha256,
        operation_id: createSafeId(),
      });
      setAppliedVersion(result.applied_version);
      setStep("done");
    } catch (error) {
      if (isStalePreview(error)) {
        setStale(true);
        return;
      }
      setFailure(errorMessage(error, t(($) => $.apply.failed_title)));
    }
  };

  const requiresReplace = preview?.requires_confirmation === true;
  const canConfirm =
    preview !== null &&
    !preview.identical &&
    !stale &&
    (!requiresReplace || strategy === "replace");

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{t(($) => $.apply.title, { name: install.name })}</DialogTitle>
          <DialogDescription>
            {t(($) => $.apply.step_of, {
              current: step === "target" ? 1 : step === "review" ? 2 : 3,
              total: 3,
            })}
            {" · "}
            {step === "target"
              ? t(($) => $.apply.step_target)
              : step === "review"
                ? t(($) => $.apply.step_review)
                : t(($) => $.apply.step_done)}
          </DialogDescription>
        </DialogHeader>

        {step === "target" ? (
          <div className="space-y-3">
            <p className="text-caption text-muted-foreground">
              {t(($) => $.apply.cross_kind_note, {
                kind: promptKindLabel(t, install.kind),
              })}
            </p>
            {targetsLoading ? (
              <div className="flex items-center justify-center py-8 text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin" />
              </div>
            ) : targets.length === 0 ? (
              <div className="px-4 py-8 text-center">
                <p className="text-body font-medium">
                  {t(($) => $.apply.no_targets_title)}
                </p>
                <p className="mx-auto mt-1 max-w-md text-caption leading-5 text-muted-foreground">
                  {t(($) => $.apply.no_targets_description)}
                </p>
              </div>
            ) : (
              <ChoiceList
                name="prompt-apply-target"
                value={targetId}
                onChange={setTargetId}
                options={targets.map((target) => ({
                  value: target.id,
                  label: target.name,
                  // The write itself is authorised server-side; this line only
                  // explains up front why an apply would be refused.
                  description: isWorkspaceManager
                    ? undefined
                    : t(($) => $.apply.no_permission),
                }))}
              />
            )}
            {failure ? <FailureAlert message={failure} /> : null}
          </div>
        ) : null}

        {step === "review" && preview ? (
          <div className="space-y-3">
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant="outline">
                {t(($) => $.badge.version, { version: install.installed_version })}
              </Badge>
              {preview.target_empty ? (
                <span className="text-caption text-muted-foreground">
                  {t(($) => $.apply.target_empty_state)}
                </span>
              ) : (
                <span className="text-caption text-muted-foreground">
                  {t(($) => $.apply.target_has_content)}
                </span>
              )}
            </div>

            {stale ? (
              <Alert variant="destructive">
                <AlertTriangle />
                <AlertTitle>{t(($) => $.apply.drift_title)}</AlertTitle>
                <AlertDescription>
                  {t(($) => $.apply.drift_description)}
                </AlertDescription>
              </Alert>
            ) : null}

            {preview.identical ? (
              <Alert>
                <Check />
                <AlertDescription>{t(($) => $.apply.identical)}</AlertDescription>
              </Alert>
            ) : (
              <PromptDiffView
                current={preview.current_content}
                incoming={preview.incoming_content}
              />
            )}

            {requiresReplace && !preview.identical ? (
              <div className="space-y-2">
                <Alert variant="destructive">
                  <AlertTriangle />
                  <AlertDescription>
                    {t(($) => $.apply.conflict_warning)}
                  </AlertDescription>
                </Alert>
                <ChoiceList
                  name="prompt-apply-strategy"
                  value={strategy}
                  onChange={setStrategy}
                  options={[
                    {
                      value: "preserve",
                      label: t(($) => $.apply.strategy_preserve),
                    },
                    {
                      value: "replace",
                      label: t(($) => $.apply.strategy_replace),
                      tone: "danger",
                    },
                  ]}
                />
              </div>
            ) : null}

            {failure ? <FailureAlert message={failure} /> : null}
          </div>
        ) : null}

        {step === "done" ? (
          <div className="space-y-2 py-4 text-center">
            <Check className="mx-auto h-5 w-5 text-success" />
            <p className="text-body font-medium">{t(($) => $.apply.done_title)}</p>
            <p className="mx-auto max-w-md text-caption leading-5 text-muted-foreground">
              {t(($) => $.apply.done_description, {
                name: install.name,
                version: appliedVersion ?? install.installed_version,
              })}
            </p>
          </div>
        ) : null}

        <DialogFooter>
          {step === "target" ? (
            <>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                {t(($) => $.install.cancel)}
              </Button>
              <Button
                disabled={targetId === "" || previewMutation.isPending}
                onClick={() => void runPreview(targetId)}
              >
                {previewMutation.isPending ? (
                  <Loader2 className="h-4 w-4 animate-spin" />
                ) : null}
                {t(($) => $.apply.next)}
              </Button>
            </>
          ) : null}

          {step === "review" ? (
            <>
              <Button variant="outline" onClick={() => setStep("target")}>
                {t(($) => $.apply.back)}
              </Button>
              {stale ? (
                <Button
                  disabled={previewMutation.isPending}
                  onClick={() => void runPreview(targetId)}
                >
                  {previewMutation.isPending ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : null}
                  {t(($) => $.apply.re_preview)}
                </Button>
              ) : (
                <Button
                  variant={strategy === "replace" ? "destructive" : "default"}
                  disabled={!canConfirm || applyMutation.isPending}
                  onClick={() => void runApply()}
                >
                  {applyMutation.isPending ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : null}
                  {strategy === "replace"
                    ? t(($) => $.apply.confirm_replace)
                    : t(($) => $.apply.confirm)}
                </Button>
              )}
            </>
          ) : null}

          {step === "done" ? (
            <>
              <Button
                variant="outline"
                onClick={() => {
                  setStep("target");
                  setTargetId("");
                  setPreview(null);
                  setStrategy("preserve");
                  setAppliedVersion(null);
                }}
              >
                {t(($) => $.apply.apply_another)}
              </Button>
              <Button onClick={() => onOpenChange(false)}>
                {t(($) => $.apply.finish)}
              </Button>
            </>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function FailureAlert({ message }: { message: string }) {
  const { t } = useT("prompt-market");
  return (
    <Alert variant="destructive">
      <AlertTriangle />
      <AlertTitle>{message}</AlertTitle>
      {/* The server rolls an apply back as one transaction, so saying the
          target is untouched is a statement of fact, not reassurance. */}
      <AlertDescription>{t(($) => $.apply.failed_note)}</AlertDescription>
    </Alert>
  );
}

/** A 409 the server tags as a stale preview, as opposed to any other conflict. */
function isStalePreview(error: unknown): boolean {
  if (!(error instanceof ApiError) || error.status !== 409) return false;
  const body = error.body;
  if (!body || typeof body !== "object") return false;
  const code = (body as { code?: unknown }).code;
  return code === "prompt_preview_stale" || code === "prompt_target_modified";
}

function errorMessage(error: unknown, fallback: string): string {
  if (error instanceof Error && error.message) return error.message;
  return fallback;
}
