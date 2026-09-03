"use client";

import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, RotateCw, Trash2, Webhook } from "lucide-react";
import type { AgentWebhook } from "@multica/core/types";
import {
  agentWebhooksOptions,
  agentWebhooksKeys,
  buildAgentWebhookUrl,
  maskedAgentWebhookUrlPreview,
} from "@multica/core/agents";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { memberListOptions } from "@multica/core/workspace/queries";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Switch } from "@multica/ui/components/ui/switch";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { cn } from "@multica/ui/lib/utils";
import { WebhookUrlField } from "../../../autopilots/components/webhook-url-field";
import { useT } from "../../../i18n";

// Mirrors the server-side cap (server/internal/handler/agent_webhook.go
// maxAgentWebhooksPerAgent). The server stays the boundary; this only drives
// the inline hint and the disabled add button.
const MAX_AGENT_WEBHOOKS = 20;
const NAME_MAX_LEN = 50;
const PROMPT_MAX_LEN = 4000;
// The char counter only appears once the textarea is 80% full — a permanent
// counter on an optional free-text field is noise (design ④).
const PROMPT_COUNTER_THRESHOLD = Math.floor(PROMPT_MAX_LEN * 0.8);

interface WebhooksTabProps {
  agent: {
    id: string;
    owner_id: string | null;
  };
}

/**
 * Webhook tab on the agent detail page (RUYI-52): manage per-agent public
 * trigger URLs. Each webhook binds a fixed prompt; visiting its unique link
 * starts a fresh chat session whose first user message is that prompt.
 *
 * Permission gates follow the integrations tab: management actions are
 * limited to the agent owner or a workspace owner/admin (server
 * canManageAgent); non-managers get a read-only list whose URLs render as a
 * fixed-width mask — the server strips the credential fields from their
 * responses entirely.
 */
export function WebhooksTab({ agent }: WebhooksTabProps) {
  const { t } = useT("agents");
  const wsId = useWorkspaceId();
  const user = useAuthStore((s) => s.user);
  const queryClient = useQueryClient();

  const { data: members = [] } = useQuery({
    ...memberListOptions(wsId),
    enabled: !!wsId,
  });

  const currentMember = members.find((m) => m.user_id === user?.id) ?? null;
  const isWorkspaceAdmin =
    currentMember?.role === "owner" || currentMember?.role === "admin";
  const isAgentOwner =
    currentMember != null &&
    !!user?.id &&
    agent.owner_id != null &&
    agent.owner_id === user.id;
  // Same rule as canManageLark in the integrations tab and the server's
  // canManageAgent: agent owner OR workspace owner/admin.
  const canManage = isWorkspaceAdmin || isAgentOwner;

  const webhooksQuery = useQuery(agentWebhooksOptions(agent.id));
  const webhooks = webhooksQuery.data ?? [];

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: agentWebhooksKeys.all(agent.id) });

  const setEnabled = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      api.setAgentWebhookEnabled(agent.id, id, enabled),
    onSettled: invalidate,
  });

  const removeWebhook = useMutation({
    mutationFn: (id: string) => api.deleteAgentWebhook(agent.id, id),
    // Close the confirmation only on success: a failed delete keeps the
    // dialog up (and its destructive button re-clickable) so the action can
    // be retried. QA round 1 caught the dialog lingering over the page and
    // blocking every click after the row was already gone.
    onSuccess: () => setDeleting(null),
    onSettled: invalidate,
  });

  const [createOpen, setCreateOpen] = useState(false);
  const [editing, setEditing] = useState<AgentWebhook | null>(null);
  const [deleting, setDeleting] = useState<AgentWebhook | null>(null);
  const [rotating, setRotating] = useState<AgentWebhook | null>(null);

  const rotate = useMutation({
    mutationFn: (id: string) => api.rotateAgentWebhook(agent.id, id),
    onSuccess: () => {
      invalidate();
      setRotating(null);
    },
    onError: () => {
      // Keep the confirmation open so the destructive action can be retried.
    },
  });

  const atLimit = webhooks.length >= MAX_AGENT_WEBHOOKS;
  const hasEverLoaded = webhooksQuery.isSuccess || webhooksQuery.isError;

  return (
    <div className="mx-auto w-full max-w-[1440px] space-y-6">
      <div className="space-y-2">
        <p className="text-caption text-muted-foreground leading-relaxed">
          {t(($) => $.webhooks.intro)}
        </p>
        <p className="text-caption text-muted-foreground leading-relaxed">
          {t(($) => $.webhooks.security_hint)}
        </p>
      </div>

      {canManage && hasEverLoaded && (
        <div className="flex items-center justify-end gap-3">
          {atLimit && (
            <span className="text-caption text-muted-foreground">
              {t(($) => $.webhooks.limit_reached, { max: MAX_AGENT_WEBHOOKS })}
            </span>
          )}
          <Button
            size="sm"
            variant="outline"
            disabled={atLimit}
            onClick={() => setCreateOpen(true)}
          >
            <Plus className="mr-1 h-3.5 w-3.5" />
            {t(($) => $.webhooks.add)}
          </Button>
        </div>
      )}

      {webhooksQuery.isPending && !webhooksQuery.isError ? (
        <div className="space-y-2" data-testid="webhooks-loading">
          {[0, 1].map((i) => (
            <div key={i} className="space-y-2 rounded-md border px-3 py-2">
              <Skeleton className="h-4 w-1/3" />
              <Skeleton className="h-3 w-2/3" />
              <Skeleton className="h-7 w-full" />
            </div>
          ))}
        </div>
      ) : webhooksQuery.isError ? (
        <div className="rounded-md border p-4 text-center">
          <p className="text-caption text-muted-foreground">
            {t(($) => $.webhooks.load_failed)}
          </p>
          <Button
            size="sm"
            variant="outline"
            className="mt-2"
            onClick={() => webhooksQuery.refetch()}
          >
            {t(($) => $.webhooks.retry)}
          </Button>
        </div>
      ) : webhooks.length === 0 ? (
        <EmptyState canManage={canManage} onAdd={() => setCreateOpen(true)} />
      ) : (
        <div className="space-y-2">
          {webhooks.map((wh) => (
            <WebhookRow
              key={wh.id}
              webhook={wh}
              canManage={canManage}
              enabledPending={setEnabled.isPending && setEnabled.variables?.id === wh.id}
              onToggle={(enabled) => setEnabled.mutate({ id: wh.id, enabled })}
              onEdit={() => setEditing(wh)}
              onRotate={() => setRotating(wh)}
              onDelete={() => setDeleting(wh)}
            />
          ))}
        </div>
      )}

      {canManage && (
        <>
          <CreateWebhookDialog
            open={createOpen}
            onOpenChange={setCreateOpen}
            agentId={agent.id}
            onCreated={invalidate}
          />
          <EditWebhookDialog
            webhook={editing}
            onOpenChange={(open) => {
              if (!open) setEditing(null);
            }}
            agentId={agent.id}
            onSaved={invalidate}
          />
          <AlertDialog
            open={deleting != null}
            onOpenChange={(open) => {
              if (!open) setDeleting(null);
            }}
          >
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>
                  {t(($) => $.webhooks.delete_title, { name: deleting?.name ?? "" })}
                </AlertDialogTitle>
                <AlertDialogDescription>
                  {t(($) => $.webhooks.delete_description)}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel disabled={removeWebhook.isPending}>
                  {t(($) => $.webhooks.cancel)}
                </AlertDialogCancel>
                <AlertDialogAction
                  className="bg-destructive text-white hover:bg-destructive/90"
                  disabled={removeWebhook.isPending}
                  onClick={(e) => {
                    e.preventDefault();
                    if (deleting) removeWebhook.mutate(deleting.id);
                  }}
                >
                  {removeWebhook.isPending
                    ? t(($) => $.webhooks.deleting)
                    : t(($) => $.webhooks.delete_confirm)}
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
          <AlertDialog
            open={rotating != null}
            onOpenChange={(open) => {
              if (!open) setRotating(null);
            }}
          >
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>{t(($) => $.webhooks.rotate_title)}</AlertDialogTitle>
                <AlertDialogDescription>
                  {t(($) => $.webhooks.rotate_description)}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel disabled={rotate.isPending}>
                  {t(($) => $.webhooks.cancel)}
                </AlertDialogCancel>
                <AlertDialogAction
                  disabled={rotate.isPending}
                  onClick={(e) => {
                    e.preventDefault();
                    if (rotating) rotate.mutate(rotating.id);
                  }}
                >
                  <RotateCw className={cn("mr-1 h-3.5 w-3.5", rotate.isPending && "animate-spin")} />
                  {rotate.isPending
                    ? t(($) => $.webhooks.rotating)
                    : t(($) => $.webhooks.rotate_confirm)}
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </>
      )}
    </div>
  );
}

// ── Row ─────────────────────────────────────────────────────────────────────

function WebhookRow({
  webhook,
  canManage,
  enabledPending,
  onToggle,
  onEdit,
  onRotate,
  onDelete,
}: {
  webhook: AgentWebhook;
  canManage: boolean;
  enabledPending: boolean;
  onToggle: (enabled: boolean) => void;
  onEdit: () => void;
  onRotate: () => void;
  onDelete: () => void;
}) {
  const { t } = useT("agents");

  // Same URL composition the autopilot trigger row uses: prefer the
  // server-provided webhook_url, fall back to apiBaseUrl + webhook_path,
  // then origin + path. Non-managers hold no credential fields — they get
  // the recognizable fixed-mask preview instead.
  const url = canManage
    ? buildAgentWebhookUrl({
        webhook,
        apiBaseUrl: api.getBaseUrl(),
        currentOrigin: typeof window !== "undefined" ? window.location.origin : undefined,
      }) ?? maskedAgentWebhookUrlPreview({})
    : maskedAgentWebhookUrlPreview({
        apiBaseUrl: api.getBaseUrl(),
        currentOrigin: typeof window !== "undefined" ? window.location.origin : undefined,
      });

  return (
    <div className="space-y-2 rounded-md border px-3 py-2">
      <div className="flex items-start gap-2">
        <Webhook className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate text-body font-medium" title={webhook.name}>
          {webhook.name}
        </span>
        {!webhook.enabled && (
          <span className="rounded bg-muted px-1.5 py-0.5 text-caption">
            {t(($) => $.webhooks.disabled_badge)}
          </span>
        )}
        {canManage && (
          <Switch
            checked={webhook.enabled}
            disabled={enabledPending}
            onCheckedChange={onToggle}
            aria-label={webhook.name}
          />
        )}
      </div>
      <p className="line-clamp-2 text-caption text-muted-foreground" title={webhook.prompt}>
        {t(($) => $.webhooks.prompt_prefix)}
        {webhook.prompt}
      </p>
      {canManage ? (
        // No wrapper flex: the field sits as a plain block child of the row
        // card, so its root gets the full card width and the inner
        // `flex-1 min-w-0 truncate` value can actually shrink on narrow
        // viewports — wrapping it in a flex container made it a flex item
        // whose min-content (the unbreakable mask run) forced the row past
        // the viewport edge and pushed the row actions out of reach
        // (QA round 1 defect 3).
        <WebhookUrlField
          url={url}
          size="sm"
          actions={
            <>
              <RowIconButton label={t(($) => $.webhooks.edit)} onClick={onEdit}>
                <Pencil className="h-3.5 w-3.5 text-muted-foreground" />
              </RowIconButton>
              <RowIconButton label={t(($) => $.webhooks.rotate)} onClick={onRotate}>
                <RotateCw className="h-3.5 w-3.5 text-muted-foreground" />
              </RowIconButton>
              <RowIconButton label={t(($) => $.webhooks.delete)} onClick={onDelete}>
                <Trash2 className="h-3.5 w-3.5 text-muted-foreground" />
              </RowIconButton>
            </>
          }
        />
      ) : (
        <code className="block max-w-full truncate rounded bg-muted px-2 py-1 font-mono text-caption text-foreground">
          {url}
        </code>
      )}
    </div>
  );
}

function RowIconButton({
  label,
  onClick,
  children,
}: {
  label: string;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <Button
      size="icon"
      variant="ghost"
      className="h-7 w-7 shrink-0"
      title={label}
      aria-label={label}
      onClick={onClick}
    >
      {children}
    </Button>
  );
}

// ── Empty state ─────────────────────────────────────────────────────────────

function EmptyState({ canManage, onAdd }: { canManage: boolean; onAdd: () => void }) {
  const { t } = useT("agents");
  return (
    <div className="rounded-xl border border-dashed">
      <div className="flex flex-col items-center gap-2 px-4 py-10 text-center">
        <span className="inline-flex size-9 items-center justify-center rounded-full bg-muted">
          <Webhook className="h-4 w-4 text-muted-foreground" />
        </span>
        {canManage ? (
          <>
            <p className="text-body font-medium">{t(($) => $.webhooks.empty_title)}</p>
            <p className="text-caption text-muted-foreground">
              {t(($) => $.webhooks.empty_description)}
            </p>
            <Button size="sm" variant="outline" className="mt-1" onClick={onAdd}>
              <Plus className="mr-1 h-3.5 w-3.5" />
              {t(($) => $.webhooks.add)}
            </Button>
          </>
        ) : (
          <p className="text-caption text-muted-foreground">
            {t(($) => $.webhooks.empty_readonly)}
          </p>
        )}
      </div>
    </div>
  );
}

// ── Create dialog (form → created panel) ────────────────────────────────────

function CreateWebhookDialog({
  open,
  onOpenChange,
  agentId,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  agentId: string;
  onCreated: () => void;
}) {
  const { t } = useT("agents");
  const [name, setName] = useState("");
  const [prompt, setPrompt] = useState("");
  const [created, setCreated] = useState<AgentWebhook | null>(null);

  // Reset the form when the dialog fully closes, so a reopen starts clean
  // while a same-session reopen after success doesn't resurrect old input.
  useEffect(() => {
    if (!open) {
      setName("");
      setPrompt("");
      setCreated(null);
    }
  }, [open]);

  const create = useMutation({
    mutationFn: () => api.createAgentWebhook(agentId, { name: name.trim(), prompt: prompt.trim() }),
    onSuccess: (webhook) => {
      onCreated();
      setCreated(webhook);
    },
  });

  const nameError =
    name.trim().length > NAME_MAX_LEN
      ? t(($) => $.webhooks.form_name_too_long)
      : null;
  const promptError =
    prompt.trim().length > PROMPT_MAX_LEN
      ? t(($) => $.webhooks.form_prompt_too_long)
      : null;
  const canSubmit =
    name.trim().length > 0 &&
    prompt.trim().length > 0 &&
    !nameError &&
    !promptError &&
    !create.isPending;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-sm">
        {created == null ? (
          <>
            <DialogHeader>
              <DialogTitle>{t(($) => $.webhooks.form_title)}</DialogTitle>
            </DialogHeader>
            <div className="space-y-4">
              <div className="space-y-1.5">
                <label className="text-caption font-medium" htmlFor="agent-webhook-name">
                  {t(($) => $.webhooks.form_name_label)}
                </label>
                <Input
                  id="agent-webhook-name"
                  value={name}
                  maxLength={NAME_MAX_LEN + 1}
                  placeholder={t(($) => $.webhooks.form_name_placeholder)}
                  onChange={(e) => setName(e.target.value)}
                />
                {nameError && (
                  <p className="text-caption text-destructive">{nameError}</p>
                )}
              </div>
              <div className="space-y-1.5">
                <label className="text-caption font-medium" htmlFor="agent-webhook-prompt">
                  {t(($) => $.webhooks.form_prompt_label)}
                </label>
                <Textarea
                  id="agent-webhook-prompt"
                  className="min-h-[72px]"
                  value={prompt}
                  placeholder={t(($) => $.webhooks.form_prompt_placeholder)}
                  onChange={(e) => setPrompt(e.target.value)}
                />
                {promptError ? (
                  <p className="text-caption text-destructive">{promptError}</p>
                ) : (
                  prompt.trim().length > PROMPT_COUNTER_THRESHOLD && (
                    <p className="text-right text-micro text-muted-foreground">
                      {prompt.trim().length}/{PROMPT_MAX_LEN}
                    </p>
                  )
                )}
              </div>
              {create.isError && (
                <p className="text-caption text-destructive">
                  {t(($) => $.webhooks.form_submit_failed)}
                </p>
              )}
              <div className="flex justify-end gap-2">
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => onOpenChange(false)}
                  disabled={create.isPending}
                >
                  {t(($) => $.webhooks.cancel)}
                </Button>
                <Button
                  size="sm"
                  disabled={!canSubmit}
                  onClick={() => create.mutate()}
                >
                  {create.isPending
                    ? t(($) => $.webhooks.form_creating)
                    : t(($) => $.webhooks.form_create)}
                </Button>
              </div>
            </div>
          </>
        ) : (
          <WebhookCreatedPanel
            webhook={created}
            onClose={() => onOpenChange(false)}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

// Post-create panel: the only moment the full URL is front and center, with
// the amber credential warning. Mirrors the autopilot WebhookCreatedPanel.
function WebhookCreatedPanel({
  webhook,
  onClose,
}: {
  webhook: AgentWebhook;
  onClose: () => void;
}) {
  const { t } = useT("agents");
  const url =
    buildAgentWebhookUrl({
      webhook,
      apiBaseUrl: api.getBaseUrl(),
      currentOrigin: typeof window !== "undefined" ? window.location.origin : undefined,
    }) ?? "";

  return (
    <>
      <DialogHeader>
        <DialogTitle className="flex items-center gap-3">
          <span className="inline-flex size-9 items-center justify-center rounded-full bg-primary/15 text-primary">
            <Webhook className="size-4" />
          </span>
          {t(($) => $.webhooks.created_title)}
        </DialogTitle>
      </DialogHeader>
      <div className="space-y-4">
        <p className="text-body text-muted-foreground leading-relaxed">
          {t(($) => $.webhooks.created_description)}
        </p>
        <div>
          <div className="mb-2 text-micro font-semibold uppercase tracking-[0.08em] text-muted-foreground">
            {t(($) => $.webhooks.url_label)}
          </div>
          <WebhookUrlField url={url} size="md" />
        </div>
        <div className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-caption leading-relaxed text-amber-700 dark:text-amber-400">
          {t(($) => $.webhooks.created_warning)}
        </div>
        <p className="text-caption text-muted-foreground">
          {t(($) => $.webhooks.created_test_hint)}
        </p>
        <div className="flex justify-end">
          <Button size="sm" onClick={onClose}>
            {t(($) => $.webhooks.created_done)}
          </Button>
        </div>
      </div>
    </>
  );
}

// ── Edit dialog (name + prompt only; URL/token immutable) ───────────────────

function EditWebhookDialog({
  webhook,
  onOpenChange,
  agentId,
  onSaved,
}: {
  webhook: AgentWebhook | null;
  onOpenChange: (open: boolean) => void;
  agentId: string;
  onSaved: () => void;
}) {
  const { t } = useT("agents");
  const [name, setName] = useState("");
  const [prompt, setPrompt] = useState("");

  useEffect(() => {
    if (webhook) {
      setName(webhook.name);
      setPrompt(webhook.prompt);
    }
  }, [webhook]);

  const save = useMutation({
    mutationFn: () => {
      if (!webhook) throw new Error("webhook closed");
      return api.updateAgentWebhook(agentId, webhook.id, {
        name: name.trim(),
        prompt: prompt.trim(),
      });
    },
    onSuccess: () => {
      onSaved();
      onOpenChange(false);
    },
  });

  const nameError =
    name.trim().length > NAME_MAX_LEN
      ? t(($) => $.webhooks.form_name_too_long)
      : null;
  const canSubmit =
    webhook != null &&
    name.trim().length > 0 &&
    prompt.trim().length > 0 &&
    !nameError &&
    !save.isPending;

  return (
    <Dialog open={webhook != null} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>{t(($) => $.webhooks.edit_title)}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <p className="text-caption text-muted-foreground">
            {t(($) => $.webhooks.edit_url_unchanged)}
          </p>
          <div className="space-y-1.5">
            <label className="text-caption font-medium" htmlFor="agent-webhook-edit-name">
              {t(($) => $.webhooks.form_name_label)}
            </label>
            <Input
              id="agent-webhook-edit-name"
              value={name}
              maxLength={NAME_MAX_LEN + 1}
              onChange={(e) => setName(e.target.value)}
            />
            {nameError && <p className="text-caption text-destructive">{nameError}</p>}
          </div>
          <div className="space-y-1.5">
            <label className="text-caption font-medium" htmlFor="agent-webhook-edit-prompt">
              {t(($) => $.webhooks.form_prompt_label)}
            </label>
            <Textarea
              id="agent-webhook-edit-prompt"
              className="min-h-[72px]"
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
            />
            {prompt.trim().length > PROMPT_MAX_LEN && (
              <p className="text-caption text-destructive">
                {t(($) => $.webhooks.form_prompt_too_long)}
              </p>
            )}
          </div>
          {save.isError && (
            <p className="text-caption text-destructive">
              {t(($) => $.webhooks.form_submit_failed)}
            </p>
          )}
          <div className="flex justify-end gap-2">
            <Button
              size="sm"
              variant="outline"
              onClick={() => onOpenChange(false)}
              disabled={save.isPending}
            >
              {t(($) => $.webhooks.cancel)}
            </Button>
            <Button size="sm" disabled={!canSubmit} onClick={() => save.mutate()}>
              {save.isPending ? t(($) => $.webhooks.form_saving) : t(($) => $.webhooks.form_save)}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
