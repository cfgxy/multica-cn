"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@multica/ui/components/ui/empty";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { clientErrorMessage } from "@multica/core/api";
import { useCurrentMember } from "@multica/core/permissions";
import {
  promptProposalListOptions,
  useApprovePromptProposal,
  useBatchApprovePromptProposals,
  useCreatePromptProposal,
  useEnactPromptProposal,
  useRejectPromptProposal,
  useRestorePromptProposal,
  useReworkPromptProposal,
  useSubmitPromptProposal,
  useUpdatePromptProposalDraft,
} from "@multica/core/self-evolution";
import type {
  PromptProposal,
  PromptProposalCarrierScope,
  PromptProposalDraftRequest,
} from "@multica/core/types";
import { ProposalPreviewDialog } from "./proposal-preview-dialog";
import { useT } from "../../i18n";

/**
 * The prompt legislation pool (RUYI-305 E2).
 *
 * A proposal is a clause-level draft against one of the four prompt carriers;
 * the draft carries the content-gate five answers and evidence anchors before
 * it can be submitted. Submission hands the row to the workspace owner: the
 * approve path forces a full-text diff preview and an explicit confirmation
 * (the injection defense — approval without having seen the diff is refused
 * by the server, and the UI refuses to send the flag before the checkbox),
 * then the legislation gate decides between enacted and gate_failed. Rejected
 * rows stay on the record; a gate-failed row returns to draft via rework.
 *
 * Writing is a member-level action. Approve/batch-approve/reject/restore/
 * enact are owner-only on the server, so those controls are hidden rather
 * than disabled for non-owners.
 */

const STATUSES = ["draft", "pending_owner", "approved", "enacted", "gate_failed", "rejected"] as const;

const SCOPES: PromptProposalCarrierScope[] = ["workspace", "project", "squad", "agent"];

const EMPTY_FORM: PromptProposalDraftRequest = {
  carrier_scope: "workspace",
  carrier_scope_id: "",
  target_section: "",
  change_kind: "add_clause",
  clause_name: "",
  clause_text: "",
  gate_answer_layer: "",
  gate_answer_retention: "",
  gate_answer_cost: "",
  gate_answer_conflict: "",
  gate_answer_dedup: "",
  evidence_anchors: [],
};

function statusTone(status: string): "default" | "secondary" | "destructive" | "outline" {
  switch (status) {
    case "enacted": return "default";
    case "gate_failed":
    case "rejected": return "destructive";
    case "pending_owner":
    case "approved": return "secondary";
    default: return "outline";
  }
}

export function ProposalTab({ wsId }: { wsId: string }) {
  const { t } = useT("self-evolution");
  const currentMember = useCurrentMember(wsId);
  const canManage = currentMember.role === "owner";

  const [status, setStatus] = useState("");
  const [selectedId, setSelectedId] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [editing, setEditing] = useState<PromptProposal | null>(null);
  const [form, setForm] = useState<PromptProposalDraftRequest>(EMPTY_FORM);
  const [previewFor, setPreviewFor] = useState<PromptProposal | null>(null);
  const [batchOpen, setBatchOpen] = useState(false);
  const [checkedIds, setCheckedIds] = useState<string[]>([]);
  const [rejectFor, setRejectFor] = useState<PromptProposal | null>(null);
  const [rejectReason, setRejectReason] = useState("");

  const list = useQuery(promptProposalListOptions(wsId, status));
  const create = useCreatePromptProposal(wsId);
  const updateDraft = useUpdatePromptProposalDraft(wsId);
  const submit = useSubmitPromptProposal(wsId);
  const approve = useApprovePromptProposal(wsId);
  const batchApprove = useBatchApprovePromptProposals(wsId);
  const reject = useRejectPromptProposal(wsId);
  const restore = useRestorePromptProposal(wsId);
  const rework = useReworkPromptProposal(wsId);
  const enact = useEnactPromptProposal(wsId);

  const proposals = list.data ?? [];
  const selected: PromptProposal | undefined =
    proposals.find((p) => p.id === selectedId) ?? proposals[0];

  const pendingIds = useMemo(
    () => proposals.filter((p) => p.status === "pending_owner").map((p) => p.id),
    [proposals],
  );

  const statusLabel = (s: string) => {
    switch (s) {
      case "draft": return t(($) => $.legislation.status.draft);
      case "pending_owner": return t(($) => $.legislation.status.pending_owner);
      case "approved": return t(($) => $.legislation.status.approved);
      case "enacted": return t(($) => $.legislation.status.enacted);
      case "gate_failed": return t(($) => $.legislation.status.gate_failed);
      case "rejected": return t(($) => $.legislation.status.rejected);
      default: return s;
    }
  };

  const mutationError = (e: unknown) =>
    toast.error(clientErrorMessage(e) ?? t(($) => $.legislation.error));

  const openCreate = () => {
    setEditing(null);
    setForm(EMPTY_FORM);
    setCreateOpen(true);
  };

  const openEdit = (p: PromptProposal) => {
    setEditing(p);
    setForm({
      carrier_scope: p.carrier_scope,
      carrier_scope_id: p.carrier_scope_id,
      target_section: p.target_section,
      change_kind: p.change_kind,
      clause_name: p.clause_name,
      clause_text: p.clause_text,
      gate_answer_layer: p.gate_answer_layer,
      gate_answer_retention: p.gate_answer_retention,
      gate_answer_cost: p.gate_answer_cost,
      gate_answer_conflict: p.gate_answer_conflict,
      gate_answer_dedup: p.gate_answer_dedup,
      evidence_anchors: p.evidence_anchors,
    });
    setCreateOpen(true);
  };

  const formComplete =
    !!form.carrier_scope_id &&
    !!form.clause_name &&
    (form.change_kind === "remove_clause" || !!form.clause_text) &&
    !!form.gate_answer_layer &&
    !!form.gate_answer_retention &&
    !!form.gate_answer_cost &&
    !!form.gate_answer_conflict &&
    !!form.gate_answer_dedup;

  const saveDraft = () => {
    const payload: PromptProposalDraftRequest = {
      ...form,
      clause_text: form.change_kind === "remove_clause" ? "" : (form.clause_text ?? ""),
      target_section: form.target_section || "",
    };
    const done = () => {
      setCreateOpen(false);
    };
    if (editing) {
      updateDraft.mutate({ id: editing.id, data: payload }, { onError: mutationError, onSuccess: done });
    } else {
      create.mutate(payload, { onError: mutationError, onSuccess: done });
    }
  };

  const submitForApproval = (p: PromptProposal) => {
    submit.mutate(p.id, { onError: mutationError });
  };

  return (
    <div className="flex flex-col gap-6" data-testid="legislation-tab">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Select
            items={[
              { value: "", label: t(($) => $.legislation.filterAll) },
              ...STATUSES.map((st) => ({ value: st, label: statusLabel(st) })),
            ]}
            value={status}
            onValueChange={(next) => {
              if (typeof next === "string") setStatus(next);
            }}
          >
            <SelectTrigger size="sm" className="w-44" aria-label={t(($) => $.legislation.list)}>
              <SelectValue>
                {status === "" ? t(($) => $.legislation.filterAll) : statusLabel(status)}
              </SelectValue>
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="">{t(($) => $.legislation.filterAll)}</SelectItem>
              {STATUSES.map((st) => (
                <SelectItem key={st} value={st}>
                  {statusLabel(st)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {canManage && pendingIds.length > 0 ? (
            <Button
              variant="outline"
              size="sm"
              onClick={() => setBatchOpen(true)}
            >
              {t(($) => $.legislation.approveBatch)}
              <Badge variant="secondary" className="ml-1">
                {pendingIds.length}
              </Badge>
            </Button>
          ) : null}
        </div>
        <Button onClick={openCreate}>{t(($) => $.legislation.create)}</Button>
      </div>

      {list.isPending ? (
        <Skeleton className="h-32 w-full" data-testid="legislation-loading" />
      ) : proposals.length === 0 ? (
        <Empty data-testid="legislation-empty">
          <EmptyHeader>
            <EmptyMedia variant="icon">§</EmptyMedia>
            <EmptyTitle>{t(($) => $.legislation.empty)}</EmptyTitle>
            <EmptyDescription>{t(($) => $.legislation.emptyHint)}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <div className="grid gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
          <ul className="flex flex-col gap-2" data-testid="legislation-list">
            {proposals.map((p) => {
              const canCheck = canManage && p.status === "pending_owner";
              return (
                <li key={p.id}>
                  <div
                    className={
                      "flex items-center gap-2 rounded-md border p-3 " +
                      (p.id === selected?.id ? "border-primary" : "")
                    }
                  >
                    {canCheck ? (
                      <Checkbox
                        // Token prefix, not copy.
                        // eslint-disable-next-line no-restricted-syntax
                        aria-label={`select-${p.id}`}
                        checked={checkedIds.includes(p.id)}
                        onCheckedChange={(v) =>
                          setCheckedIds((prev) =>
                            v && !prev.includes(p.id)
                              ? [...prev, p.id]
                              : prev.filter((id) => id !== p.id),
                          )
                        }
                      />
                    ) : null}
                    <button
                      type="button"
                      className="min-w-0 flex-1 text-left"
                      onClick={() => setSelectedId(p.id)}
                    >
                      <div className="flex items-center gap-2">
                        <Badge variant={statusTone(p.status)}>{statusLabel(p.status)}</Badge>
                        <span className="truncate text-body font-medium">{p.clause_name}</span>
                      </div>
                      <div className="text-muted-foreground truncate text-caption">
                        {t(($) => $.legislation.carrierScope[p.carrier_scope])} ·{" "}
                        {t(($) => $.legislation.changeKind[p.change_kind])}
                        {p.source === "retrospective"
                          ? ` · ${t(($) => $.legislation.source.retrospective)}`
                          : null}
                      </div>
                    </button>
                  </div>
                </li>
              );
            })}
          </ul>

          {selected ? (
            <div className="flex flex-col gap-4 rounded-md border p-4" data-testid="legislation-detail">
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant={statusTone(selected.status)}>{statusLabel(selected.status)}</Badge>
                <span className="text-body font-semibold">{selected.clause_name}</span>
              </div>
              <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-body">
                <dt className="text-muted-foreground">{t(($) => $.legislation.carrierLabel)}</dt>
                <dd>{t(($) => $.legislation.carrierScope[selected.carrier_scope])}</dd>
                <dt className="text-muted-foreground">{t(($) => $.legislation.changeKindLabel)}</dt>
                <dd>{t(($) => $.legislation.changeKind[selected.change_kind])}</dd>
                {selected.clause_text ? (
                  <>
                    <dt className="text-muted-foreground">{t(($) => $.legislation.clauseTextLabel)}</dt>
                    <dd className="whitespace-pre-wrap">{selected.clause_text}</dd>
                  </>
                ) : null}
              </dl>
              {selected.gate_errors.length > 0 ? (
                <div className="text-destructive text-body" data-testid="legislation-gate-errors">
                  <div className="font-medium">{t(($) => $.legislation.gateErrors)}</div>
                  <ul className="list-disc pl-5">
                    {selected.gate_errors.map((e, i) => (
                      <li key={i}>
                        {typeof e.line === "number" ? `${e.line}: ` : ""}
                        {e.message}
                      </li>
                    ))}
                  </ul>
                </div>
              ) : null}
              {selected.gate_warnings.length > 0 ? (
                <div className="text-body text-amber-600 dark:text-amber-400" data-testid="legislation-gate-warnings">
                  <div className="font-medium">{t(($) => $.legislation.gateWarnings)}</div>
                  <ul className="list-disc pl-5">
                    {selected.gate_warnings.map((w, i) => (
                      <li key={i}>
                        {typeof w.line === "number" ? `${w.line}: ` : ""}
                        {w.message}
                      </li>
                    ))}
                  </ul>
                </div>
              ) : null}
              {selected.rollback_reason ? (
                <div className="text-muted-foreground text-body">
                  {t(($) => $.legislation.rejectReason)}: {selected.rollback_reason}
                </div>
              ) : null}
              {selected.enacted_version != null ? (
                <div className="text-body">
                  {t(($) => $.legislation.enactedVersion)}: {selected.enacted_version}
                </div>
              ) : null}

              <div className="flex flex-wrap gap-2">
                {selected.status === "draft" ? (
                  <>
                    <Button variant="outline" size="sm" onClick={() => openEdit(selected)}>
                      {t(($) => $.legislation.edit)}
                    </Button>
                    <Button
                      size="sm"
                      onClick={() => submitForApproval(selected)}
                      disabled={submit.isPending}
                    >
                      {t(($) => $.legislation.submit)}
                    </Button>
                  </>
                ) : null}
                {canManage && selected.status === "pending_owner" ? (
                  <Button
                    size="sm"
                    onClick={() => {
                      setBatchOpen(false);
                      setPreviewFor(selected);
                    }}
                  >
                    {t(($) => $.legislation.preview)}
                  </Button>
                ) : null}
                {canManage && selected.status === "approved" ? (
                  <Button
                    size="sm"
                    onClick={() => enact.mutate(selected.id, { onError: mutationError })}
                    disabled={enact.isPending}
                    title={t(($) => $.legislation.enactHint)}
                  >
                    {t(($) => $.legislation.enact)}
                  </Button>
                ) : null}
                {canManage && selected.status === "pending_owner" ? (
                  <Button
                    variant="destructive"
                    size="sm"
                    onClick={() => {
                      setRejectReason("");
                      setRejectFor(selected);
                    }}
                  >
                    {t(($) => $.legislation.reject)}
                  </Button>
                ) : null}
                {selected.status === "gate_failed" ? (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => rework.mutate(selected.id, { onError: mutationError })}
                    disabled={rework.isPending}
                  >
                    {t(($) => $.legislation.rework)}
                  </Button>
                ) : null}
                {canManage && selected.status === "rejected" ? (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => restore.mutate(selected.id, { onError: mutationError })}
                    disabled={restore.isPending}
                  >
                    {t(($) => $.legislation.restore)}
                  </Button>
                ) : null}
              </div>
            </div>
          ) : null}
        </div>
      )}

      {/* Create / edit draft dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="max-h-[85vh] max-w-xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{t(($) => $.legislation.createTitle)}</DialogTitle>
            <DialogDescription>{t(($) => $.legislation.createDescription)}</DialogDescription>
          </DialogHeader>
          <div className="grid gap-3">
            <div className="grid grid-cols-2 gap-3">
              <div className="grid gap-1">
                <Label htmlFor="leg-scope">{t(($) => $.legislation.carrierLabel)}</Label>
                <Select
                  items={SCOPES.map((sc) => ({
                    value: sc,
                    label: t(($) => $.legislation.carrierScope[sc]),
                  }))}
                  value={form.carrier_scope}
                  onValueChange={(v) => {
                    if (typeof v === "string") {
                      setForm((f) => ({ ...f, carrier_scope: v as PromptProposalCarrierScope }));
                    }
                  }}
                >
                  <SelectTrigger id="leg-scope">
                    <SelectValue>
                      {t(($) => $.legislation.carrierScope[form.carrier_scope])}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {SCOPES.map((sc) => (
                      <SelectItem key={sc} value={sc}>
                        {t(($) => $.legislation.carrierScope[sc])}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="grid gap-1">
                <Label htmlFor="leg-kind">{t(($) => $.legislation.changeKindLabel)}</Label>
                <Select
                  items={[
                    { value: "add_clause", label: t(($) => $.legislation.changeKind.add_clause) },
                    { value: "remove_clause", label: t(($) => $.legislation.changeKind.remove_clause) },
                  ]}
                  value={form.change_kind}
                  onValueChange={(v) => {
                    if (v === "add_clause" || v === "remove_clause") {
                      setForm((f) => ({ ...f, change_kind: v }));
                    }
                  }}
                >
                  <SelectTrigger id="leg-kind">
                    <SelectValue>{t(($) => $.legislation.changeKind[form.change_kind])}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="add_clause">
                      {t(($) => $.legislation.changeKind.add_clause)}
                    </SelectItem>
                    <SelectItem value="remove_clause">
                      {t(($) => $.legislation.changeKind.remove_clause)}
                    </SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </div>
            <div className="grid gap-1">
              <Label htmlFor="leg-carrier-id">{t(($) => $.legislation.carrierLabel)} ID</Label>
              <Input
                id="leg-carrier-id"
                value={form.carrier_scope_id}
                onChange={(e) => setForm((f) => ({ ...f, carrier_scope_id: e.target.value }))}
              />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="grid gap-1">
                <Label htmlFor="leg-section">{t(($) => $.legislation.sectionLabel)}</Label>
                <Input
                  id="leg-section"
                  value={form.target_section}
                  onChange={(e) => setForm((f) => ({ ...f, target_section: e.target.value }))}
                />
              </div>
              <div className="grid gap-1">
                <Label htmlFor="leg-clause-name">{t(($) => $.legislation.clauseNameLabel)}</Label>
                <Input
                  id="leg-clause-name"
                  value={form.clause_name}
                  onChange={(e) => setForm((f) => ({ ...f, clause_name: e.target.value }))}
                />
              </div>
            </div>
            {form.change_kind === "add_clause" ? (
              <div className="grid gap-1">
                <Label htmlFor="leg-clause-text">{t(($) => $.legislation.clauseTextLabel)}</Label>
                <Textarea
                  id="leg-clause-text"
                  rows={3}
                  placeholder={t(($) => $.legislation.clauseTextPlaceholder)}
                  value={form.clause_text}
                  onChange={(e) => setForm((f) => ({ ...f, clause_text: e.target.value }))}
                />
              </div>
            ) : null}
            <fieldset className="rounded-md border p-3">
              <legend className="px-1 text-body font-medium">{t(($) => $.legislation.gateAnswersTitle)}</legend>
              <div className="grid gap-2">
                {(
                  [
                    ["gate_answer_layer", "gateLayer"],
                    ["gate_answer_retention", "gateRetention"],
                    ["gate_answer_cost", "gateCost"],
                    ["gate_answer_conflict", "gateConflict"],
                    ["gate_answer_dedup", "gateDedup"],
                  ] as const
                ).map(([field, key]) => (
                  <div key={field} className="grid gap-1">
                    <Label htmlFor={`leg-${field}`}>{t(($) => $.legislation[key])}</Label>
                    <Textarea
                      id={`leg-${field}`}
                      rows={2}
                      placeholder={t(($) => $.legislation[`${key}Placeholder` as const])}
                      value={form[field]}
                      onChange={(e) => setForm((f) => ({ ...f, [field]: e.target.value }))}
                    />
                  </div>
                ))}
              </div>
            </fieldset>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>
              {t(($) => $.legislation.cancel)}
            </Button>
            <Button onClick={saveDraft} disabled={!formComplete || create.isPending || updateDraft.isPending}>
              {t(($) => $.legislation.saveDraft)}
            </Button>
            {editing ? (
              <Button
                onClick={() => {
                  const payload: PromptProposalDraftRequest = {
                    ...form,
                    clause_text: form.change_kind === "remove_clause" ? "" : (form.clause_text ?? ""),
                    target_section: form.target_section || "",
                  };
                  updateDraft.mutate(
                    { id: editing.id, data: payload },
                    {
                      onError: mutationError,
                      onSuccess: () => {
                        setCreateOpen(false);
                        submitForApproval({ ...editing, ...payload } as PromptProposal);
                      },
                    },
                  );
                }}
                disabled={!formComplete || updateDraft.isPending}
              >
                {t(($) => $.legislation.submit)}
              </Button>
            ) : null}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Single-approve preview dialog */}
      <ProposalPreviewDialog
        open={previewFor != null}
        onOpenChange={(o) => {
          if (!o) setPreviewFor(null);
        }}
        ids={previewFor ? [previewFor.id] : []}
        confirmLabel={t(($) => $.legislation.approve)}
        onConfirm={(flag) => {
          if (!previewFor) return;
          approve.mutate(
            { id: previewFor.id, confirmDiffPreviewed: flag },
            {
              onError: mutationError,
              onSuccess: () => setPreviewFor(null),
            },
          );
        }}
      />

      {/* Batch approve dialog */}
      <ProposalPreviewDialog
        open={batchOpen}
        onOpenChange={setBatchOpen}
        ids={checkedIds}
        confirmLabel={t(($) => $.legislation.approveBatch)}
        onConfirm={(flag) => {
          batchApprove.mutate(
            { ids: checkedIds, confirmDiffPreviewed: flag },
            {
              onSuccess: (outcomes) => {
                setBatchOpen(false);
                const failed = outcomes.filter((o) => o.status !== 200);
                if (failed.length > 0) {
                  toast.warning(t(($) => $.legislation.batchPartial));
                }
              },
              onError: mutationError,
            },
          );
        }}
      />

      {/* Reject dialog */}
      <Dialog open={rejectFor != null} onOpenChange={(o) => (!o ? setRejectFor(null) : undefined)}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>{t(($) => $.legislation.rejectDialogTitle)}</DialogTitle>
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor="leg-reject-reason">{t(($) => $.legislation.rejectReason)}</Label>
            <Textarea
              id="leg-reject-reason"
              rows={3}
              placeholder={t(($) => $.legislation.rejectPlaceholder)}
              value={rejectReason}
              onChange={(e) => setRejectReason(e.target.value)}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setRejectFor(null)}>
              {t(($) => $.legislation.cancel)}
            </Button>
            <Button
              variant="destructive"
              disabled={!rejectReason.trim() || reject.isPending}
              onClick={() => {
                if (!rejectFor) return;
                reject.mutate(
                  { id: rejectFor.id, reason: rejectReason.trim() },
                  {
                    onError: mutationError,
                    onSuccess: () => setRejectFor(null),
                  },
                );
              }}
            >
              {t(($) => $.legislation.rejectConfirm)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
