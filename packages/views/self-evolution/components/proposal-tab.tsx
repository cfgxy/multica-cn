"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
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
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
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
  proposalListOptions,
  useAdoptProposal,
  useCreateProposal,
  useRejectProposal,
  useRestoreProposal,
  useVerifyProposal,
} from "@multica/core/self-evolution";
import type { Proposal } from "@multica/core/types";
import { useT, useLocale } from "../../i18n";

/**
 * The proposal pool (RUYI-265 §A).
 *
 * B1: a proposal only enters the pool with a falsifiable prophecy attached at
 * creation — the form asks for it up front instead of letting rows pile up as
 * opinions. B2: adoption and verification are separate records on the same row;
 * verify is offered only after adopt. B3: rejected rows stay in the pool and
 * retrievable; restore brings one back to draft instead of un-deleting it.
 *
 * Writing a proposal is a member-level action — the pool is where proposals
 * come from. Adopting, rejecting, restoring and verifying are owner-only on
 * the server, so the buttons are hidden rather than disabled for non-owners.
 */
export function ProposalTab({ wsId }: { wsId: string }) {
  const { t } = useT("self-evolution");
  const locale = useLocale();
  const currentMember = useCurrentMember(wsId);
  const canManage = currentMember.role === "owner";

  const [status, setStatus] = useState("");
  const [selectedId, setSelectedId] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [rejectReason, setRejectReason] = useState("");
  const [verifyForm, setVerifyForm] = useState(false);
  const [verdict, setVerdict] = useState("established");
  const [evidence, setEvidence] = useState("");
  const [note, setNote] = useState("");

  const list = useQuery(proposalListOptions(wsId, status));
  const create = useCreateProposal(wsId);
  const adopt = useAdoptProposal(wsId);
  const reject = useRejectProposal(wsId);
  const restore = useRestoreProposal(wsId);
  const verify = useVerifyProposal(wsId);

  const selected: Proposal | undefined =
    list.data?.find((p) => p.id === selectedId) ?? list.data?.[0];

  const resetForms = () => {
    setRejectReason("");
    setVerifyForm(false);
    setEvidence("");
    setNote("");
    setVerdict("established");
  };

  // Literal-key switches keep the selector API's key typing; the row's status
  // is a server-controlled string and unknown values fall through verbatim.
  const statusLabel = (s: string) => {
    switch (s) {
      case "draft": return t(($) => $.proposals.status.draft);
      case "needs_revision": return t(($) => $.proposals.status.needs_revision);
      case "adopted": return t(($) => $.proposals.status.adopted);
      case "rejected": return t(($) => $.proposals.status.rejected);
      case "archived": return t(($) => $.proposals.status.archived);
      default: return s;
    }
  };
  const typeLabel = (s: string) => {
    switch (s) {
      case "prompt_revision": return t(($) => $.proposals.type.prompt_revision);
      case "skill": return t(($) => $.proposals.type.skill);
      case "project_cognition": return t(($) => $.proposals.type.project_cognition);
      case "lesson": return t(($) => $.proposals.type.lesson);
      case "pitfall": return t(($) => $.proposals.type.pitfall);
      default: return s;
    }
  };

  const mutationError = (e: unknown) =>
    toast.error(clientErrorMessage(e) ?? t(($) => $.proposals.error.write));

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-col gap-1.5">
          <h2 className="text-title font-semibold">{t(($) => $.proposals.title)}</h2>
          <p className="text-body text-muted-foreground">{t(($) => $.proposals.description)}</p>
        </div>
        <div className="flex items-center gap-2">
          <Select
            items={[
              { value: "", label: t(($) => $.proposals.filterAll) },
              ...PROPOSAL_STATUSES.map((s) => ({
                value: s,
                label: t(($) => $.proposals.status[s]),
              })),
            ]}
            value={status}
            onValueChange={(next) => {
              if (typeof next === "string") {
                setStatus(next);
                resetForms();
              }
            }}
          >
            <SelectTrigger size="sm" className="w-44">
              <SelectValue>
                {status === "" ? t(($) => $.proposals.filterAll) : statusLabel(status)}
              </SelectValue>
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="">{t(($) => $.proposals.filterAll)}</SelectItem>
              {PROPOSAL_STATUSES.map((s) => (
                <SelectItem key={s} value={s}>
                  {t(($) => $.proposals.status[s])}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button size="sm" variant="outline" onClick={() => setCreateOpen(true)}>
            {t(($) => $.proposals.create)}
          </Button>
        </div>
      </div>

      {list.isPending ? (
        <div className="space-y-2">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
        </div>
      ) : list.isError ? (
        <p role="alert" className="text-body text-destructive">
          {t(($) => $.proposals.error.read)}
          <Button size="sm" variant="outline" className="ml-2" onClick={() => void list.refetch()}>
            {t(($) => $.proposals.error.retry)}
          </Button>
        </p>
      ) : !list.data?.length ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>{t(($) => $.proposals.empty.title)}</EmptyTitle>
            <EmptyDescription>{t(($) => $.proposals.empty.description)}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <div className="grid min-w-0 gap-8 lg:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
          <section className="min-w-0 space-y-3" aria-label={t(($) => $.proposals.list)}>
            <h3 className="text-title font-medium">{t(($) => $.proposals.list)}</h3>
            <div className="divide-y border-y">
              {list.data.map((p) => (
                <button
                  key={p.id}
                  type="button"
                  className="flex w-full min-w-0 flex-col items-start gap-1 py-3 text-left text-body hover:bg-muted/50"
                  data-testid={`proposal-row-${p.id}`}
                  onClick={() => {
                    setSelectedId(p.id);
                    resetForms();
                  }}
                >
                  <span className="flex w-full items-center gap-2">
                    <span className="min-w-0 flex-1 truncate font-medium">{p.title}</span>
                    <Badge variant="outline">{typeLabel(p.type)}</Badge>
                    {p.created_by_type === "system" ? (
                      <Badge variant="secondary">{t(($) => $.proposals.source.system)}</Badge>
                    ) : (
                      <Badge variant="outline">{t(($) => $.proposals.source.member)}</Badge>
                    )}
                    <Badge variant={p.status === "adopted" ? "secondary" : "outline"}>
                      {statusLabel(p.status)}
                    </Badge>
                  </span>
                  <span className="text-caption text-muted-foreground">
                    {t(($) => $.proposals.createdAt, {
                      date: new Date(p.created_at).toLocaleDateString(locale),
                    })}
                  </span>
                </button>
              ))}
            </div>
          </section>

          {selected ? (
            <ProposalDetail
              proposal={selected}
              canManage={canManage}
              pending={adopt.isPending || reject.isPending || restore.isPending || verify.isPending}
              rejectReason={rejectReason}
              onRejectReason={setRejectReason}
              onAdopt={() =>
                adopt.mutate(selected.id, {
                  onSuccess: resetForms,
                  onError: mutationError,
                })
              }
              onReject={() =>
                reject.mutate(
                  { id: selected.id, reason: rejectReason },
                  {
                    onSuccess: resetForms,
                    onError: mutationError,
                  },
                )
              }
              onRestore={() =>
                restore.mutate(selected.id, {
                  onSuccess: resetForms,
                  onError: mutationError,
                })
              }
              verifyForm={verifyForm}
              onVerifyForm={setVerifyForm}
              verdict={verdict}
              onVerdict={setVerdict}
              evidence={evidence}
              onEvidence={setEvidence}
              note={note}
              onNote={setNote}
              onVerify={() =>
                verify.mutate(
                  {
                    id: selected.id,
                    data: {
                      verdict,
                      evidence,
                      ...(note ? { note } : {}),
                    },
                  },
                  {
                    onSuccess: resetForms,
                    onError: mutationError,
                  },
                )
              }
            />
          ) : null}
        </div>
      )}

      <ProposalCreateDialog
        open={createOpen}
        pending={create.isPending}
        onOpenChange={setCreateOpen}
        onSubmit={(draft) => {
          create.mutate(draft, {
            onSuccess: () => setCreateOpen(false),
            onError: mutationError,
          });
        }}
      />
    </div>
  );
}

const PROPOSAL_STATUSES = [
  "draft",
  "needs_revision",
  "adopted",
  "rejected",
  "archived",
] as const;

const QUANTITATIVE_TYPES = ["prompt_revision", "skill"] as const;
const BEHAVIOR_TYPES = ["project_cognition", "lesson", "pitfall"] as const;
const PROPOSAL_TYPES = [...QUANTITATIVE_TYPES, ...BEHAVIOR_TYPES] as const;
const VERDICTS = ["established", "partial", "refuted", "undeterminable"] as const;
const DIRECTIONS = ["down", "up", "unchanged"] as const;

type ProposalType = (typeof PROPOSAL_TYPES)[number];

function isQuantitative(type: string): boolean {
  return (QUANTITATIVE_TYPES as readonly string[]).includes(type);
}

/** The immutable half of the row: what was claimed, against what snapshot. */
function ProposalDetail({
  proposal,
  canManage,
  pending,
  rejectReason,
  onRejectReason,
  onAdopt,
  onReject,
  onRestore,
  verifyForm,
  onVerifyForm,
  verdict,
  onVerdict,
  evidence,
  onEvidence,
  note,
  onNote,
  onVerify,
}: {
  proposal: Proposal;
  canManage: boolean;
  pending: boolean;
  rejectReason: string;
  onRejectReason: (value: string) => void;
  onAdopt: () => void;
  onReject: () => void;
  onRestore: () => void;
  verifyForm: boolean;
  onVerifyForm: (value: boolean) => void;
  verdict: string;
  onVerdict: (value: string) => void;
  evidence: string;
  onEvidence: (value: string) => void;
  note: string;
  onNote: (value: string) => void;
  onVerify: () => void;
}) {
  const { t } = useT("self-evolution");
  const prophecy = proposal.prophecy ?? {};
  const marks = readMarks(proposal.verification);
  const object = isQuantitative(proposal.type)
    ? readMetric(prophecy.object)
    : undefined;
  const directionLabel = (s: string) => {
    switch (s) {
      case "down": return t(($) => $.proposals.direction.down);
      case "up": return t(($) => $.proposals.direction.up);
      case "unchanged": return t(($) => $.proposals.direction.unchanged);
      default: return s;
    }
  };
  const verdictLabel = (s: string) => {
    switch (s) {
      case "established": return t(($) => $.proposals.verdict.established);
      case "partial": return t(($) => $.proposals.verdict.partial);
      case "refuted": return t(($) => $.proposals.verdict.refuted);
      case "undeterminable": return t(($) => $.proposals.verdict.undeterminable);
      default: return s;
    }
  };

  return (
    <section className="min-w-0 space-y-5" aria-label={t(($) => $.proposals.detailTitle)}>
      <div className="space-y-2 border-b pb-4">
        <h3 className="text-title font-semibold">{proposal.title}</h3>
        <p className="text-body text-muted-foreground">{proposal.summary}</p>
        {proposal.transfer_error ? (
          <p role="alert" className="text-caption text-destructive">
            {t(($) => $.proposals.transferError, { error: proposal.transfer_error })}
          </p>
        ) : null}
      </div>

      <div className="space-y-2">
        <h4 className="text-body font-medium">{t(($) => $.proposals.detail.prophecy)}</h4>
        <p className="text-body">{String(prophecy.outcome_text ?? "")}</p>
        {typeof prophecy.falsify_condition === "string" && prophecy.falsify_condition ? (
          <p className="text-body text-muted-foreground">
            {t(($) => $.proposals.detail.falsify)}: {prophecy.falsify_condition}
          </p>
        ) : null}
        {object !== undefined ? (
          <p className="text-caption text-muted-foreground">
            {t(($) => $.proposals.detail.object)}: {object} ·{" "}
            {t(($) => $.proposals.detail.direction)}:{" "}
            {directionLabel(String(prophecy.direction ?? ""))} ·{" "}
            {t(($) => $.proposals.detail.band, {
              low: String(prophecy.range_low ?? ""),
              high: String(prophecy.range_high ?? ""),
            })}
          </p>
        ) : null}
      </div>

      <div className="space-y-2">
        <h4 className="text-body font-medium">{t(($) => $.proposals.detail.verification)}</h4>
        {!marks.length ? (
          <p className="text-body text-muted-foreground">
            {t(($) => $.proposals.detail.noVerification)}
          </p>
        ) : (
          <ul className="space-y-2">
            {marks.map((mark, i) => (
              <li key={i} className="rounded-md border px-3 py-2 text-caption">
                <span className="flex items-center gap-2">
                  <Badge variant="outline">
                    {verdictLabel(String(mark.verdict ?? ""))}
                  </Badge>
                  <span className="text-muted-foreground">{String(mark.at ?? "")}</span>
                </span>
                <p className="mt-1 break-words">{String(mark.evidence ?? "")}</p>
                {mark.note ? (
                  <p className="mt-1 text-muted-foreground">{String(mark.note)}</p>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="space-y-2">
        <h4 className="text-body font-medium">{t(($) => $.proposals.detail.audit)}</h4>
        <ul className="space-y-1 text-caption text-muted-foreground">
          {(proposal.audit_log ?? []).map((entry, i) => {
            const record = (entry ?? {}) as Record<string, unknown>;
            return (
              <li key={i}>
                {String(record.action ?? "")} · {String(record.actor ?? "")} ·{" "}
                {String(record.at ?? "")}
              </li>
            );
          })}
        </ul>
      </div>

      {canManage ? (
        <div className="space-y-3 border-t pt-4" data-testid="proposal-owner-actions">
          {proposal.transfer_state === "transferring" ? (
            <div className="space-y-1.5" data-testid="proposal-transferring">
              <Badge variant="outline">{t(($) => $.proposals.transferring)}</Badge>
              <p className="text-caption text-muted-foreground">
                {t(($) => $.proposals.transferringNote)}
              </p>
            </div>
          ) : null}
          {proposal.status === "draft" || proposal.status === "needs_revision" ? (
            proposal.transfer_state === "transferring" ? null : (
              <>
                <Button size="sm" disabled={pending} onClick={onAdopt}>
                  {t(($) => $.proposals.adopt)}
                </Button>
                <div className="space-y-1.5">
                  <Label htmlFor="proposal-reject-reason">
                    {t(($) => $.proposals.rejectReasonLabel)}
                  </Label>
                  <Input
                    id="proposal-reject-reason"
                    value={rejectReason}
                    onChange={(e) => onRejectReason(e.target.value)}
                    placeholder={t(($) => $.proposals.rejectReasonPlaceholder)}
                  />
                  <Button size="sm" variant="outline" disabled={pending} onClick={onReject}>
                    {t(($) => $.proposals.reject)}
                  </Button>
                </div>
              </>
            )
          ) : null}
          {proposal.status === "rejected" || proposal.status === "archived" ? (
            <Button size="sm" variant="outline" disabled={pending} onClick={onRestore}>
              {t(($) => $.proposals.restore)}
            </Button>
          ) : null}
          {proposal.status === "adopted" ? (
            verifyForm ? (
              <div className="space-y-2 rounded-md border p-3" data-testid="proposal-verify-form">
                <div className="space-y-1.5">
                  <Label>{t(($) => $.proposals.verifyVerdictLabel)}</Label>
                  <Select
                    items={VERDICTS.map((v) => ({ value: v, label: verdictLabel(v) }))}
                    value={verdict}
                    onValueChange={(next) => {
                      if (typeof next === "string") onVerdict(next);
                    }}
                  >
                    <SelectTrigger size="sm" className="w-56">
                      <SelectValue>{verdictLabel(verdict)}</SelectValue>
                    </SelectTrigger>
                    <SelectContent>
                      {VERDICTS.map((v) => (
                        <SelectItem key={v} value={v}>
                          {verdictLabel(v)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="proposal-verify-evidence">
                    {t(($) => $.proposals.verifyEvidenceLabel)}
                  </Label>
                  <Textarea
                    id="proposal-verify-evidence"
                    value={evidence}
                    onChange={(e) => onEvidence(e.target.value)}
                    placeholder={t(($) => $.proposals.verifyEvidencePlaceholder)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="proposal-verify-note">
                    {t(($) => $.proposals.verifyNoteLabel)}
                  </Label>
                  <Input
                    id="proposal-verify-note"
                    value={note}
                    onChange={(e) => onNote(e.target.value)}
                  />
                </div>
                <div className="flex gap-2">
                  <Button size="sm" disabled={pending || !evidence.trim()} onClick={onVerify}>
                    {t(($) => $.proposals.verifySubmit)}
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => onVerifyForm(false)}>
                    {t(($) => $.proposals.cancel)}
                  </Button>
                </div>
              </div>
            ) : (
              <Button size="sm" variant="outline" onClick={() => onVerifyForm(true)}>
                {t(($) => $.proposals.verify)}
              </Button>
            )
          ) : null}
        </div>
      ) : (
        <p className="text-caption text-muted-foreground border-t pt-4">
          {t(($) => $.proposals.ownerOnly)}
        </p>
      )}
    </section>
  );
}

function readMarks(verification: Record<string, unknown> | undefined) {
  const marks = verification?.marks;
  return Array.isArray(marks) ? (marks as Record<string, unknown>[]) : [];
}

function readMetric(object: unknown): string | undefined {
  if (object && typeof object === "object" && !Array.isArray(object)) {
    const metric = (object as Record<string, unknown>).metric;
    if (typeof metric === "string" && metric) return metric;
  }
  return undefined;
}

interface ProposalDraft {
  type: string;
  title: string;
  summary: string;
  prophecy: Record<string, unknown>;
}

/**
 * The creation form enforces B1 client-side only as far as required fields go;
 * the server re-validates and its refusal (missing prophecy parts, inverted
 * band) is surfaced verbatim so this form never carries a second copy of the
 * rule.
 */
function ProposalCreateDialog({
  open,
  pending,
  onOpenChange,
  onSubmit,
}: {
  open: boolean;
  pending: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (draft: ProposalDraft) => void;
}) {
  const { t } = useT("self-evolution");
  const [type, setType] = useState<ProposalType>("project_cognition");
  const [title, setTitle] = useState("");
  const [summary, setSummary] = useState("");
  const [outcome, setOutcome] = useState("");
  const [falsify, setFalsify] = useState("");
  const [metric, setMetric] = useState("");
  const [direction, setDirection] = useState<(typeof DIRECTIONS)[number]>("down");
  const [low, setLow] = useState("");
  const [high, setHigh] = useState("");

  const quantitative = isQuantitative(type);
  const valid =
    title.trim() !== "" &&
    summary.trim() !== "" &&
    outcome.trim() !== "" &&
    (quantitative
      ? metric.trim() !== "" && low !== "" && high !== ""
      : falsify.trim() !== "");

  const submit = () => {
    const prophecy: Record<string, unknown> = { outcome_text: outcome };
    if (quantitative) {
      prophecy.object = { metric };
      prophecy.direction = direction;
      prophecy.range_low = Number(low);
      prophecy.range_high = Number(high);
    } else {
      prophecy.falsify_condition = falsify;
    }
    onSubmit({ type, title, summary, prophecy });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t(($) => $.proposals.createTitle)}</DialogTitle>
          <DialogDescription>{t(($) => $.proposals.createDescription)}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label>{t(($) => $.proposals.typeLabel)}</Label>
            <Select
              items={PROPOSAL_TYPES.map((v) => ({
                value: v,
                label: t(($) => $.proposals.type[v]),
              }))}
              value={type}
              onValueChange={(next) => {
                if (typeof next === "string") setType(next as ProposalType);
              }}
            >
              <SelectTrigger size="sm" className="w-full">
                <SelectValue>{t(($) => $.proposals.type[type])}</SelectValue>
              </SelectTrigger>
              <SelectContent>
                {PROPOSAL_TYPES.map((v) => (
                  <SelectItem key={v} value={v}>
                    {t(($) => $.proposals.type[v])}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="proposal-title">{t(($) => $.proposals.titleLabel)}</Label>
            <Input
              id="proposal-title"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="proposal-summary">{t(($) => $.proposals.summaryLabel)}</Label>
            <Textarea
              id="proposal-summary"
              value={summary}
              onChange={(e) => setSummary(e.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="proposal-outcome">{t(($) => $.proposals.prophecyOutcome)}</Label>
            <Textarea
              id="proposal-outcome"
              value={outcome}
              onChange={(e) => setOutcome(e.target.value)}
            />
          </div>
          {quantitative ? (
            <>
              <div className="space-y-1.5">
                <Label htmlFor="proposal-metric">{t(($) => $.proposals.prophecyObject)}</Label>
                <Input
                  id="proposal-metric"
                  value={metric}
                  onChange={(e) => setMetric(e.target.value)}
                  // Backend metric key, not copy.
                  // eslint-disable-next-line no-restricted-syntax
                  placeholder="median_total_tokens"
                />
              </div>
              <div className="space-y-1.5">
                <Label>{t(($) => $.proposals.prophecyDirection)}</Label>
                <Select
                  items={DIRECTIONS.map((v) => ({
                    value: v,
                    label: t(($) => $.proposals.direction[v]),
                  }))}
                  value={direction}
                  onValueChange={(next) => {
                    if (typeof next === "string") setDirection(next);
                  }}
                >
                  <SelectTrigger size="sm" className="w-full">
                    <SelectValue>{t(($) => $.proposals.direction[direction])}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {DIRECTIONS.map((v) => (
                      <SelectItem key={v} value={v}>
                        {t(($) => $.proposals.direction[v])}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <Label htmlFor="proposal-low">{t(($) => $.proposals.prophecyRangeLow)}</Label>
                  <Input
                    id="proposal-low"
                    type="number"
                    value={low}
                    onChange={(e) => setLow(e.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="proposal-high">{t(($) => $.proposals.prophecyRangeHigh)}</Label>
                  <Input
                    id="proposal-high"
                    type="number"
                    value={high}
                    onChange={(e) => setHigh(e.target.value)}
                  />
                </div>
              </div>
            </>
          ) : (
            <div className="space-y-1.5">
              <Label htmlFor="proposal-falsify">{t(($) => $.proposals.prophecyFalsify)}</Label>
              <Textarea
                id="proposal-falsify"
                value={falsify}
                onChange={(e) => setFalsify(e.target.value)}
              />
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t(($) => $.proposals.cancel)}
          </Button>
          <Button disabled={pending || !valid} onClick={submit}>
            {t(($) => $.proposals.submit)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
