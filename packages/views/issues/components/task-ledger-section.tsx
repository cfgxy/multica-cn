"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  CheckCircle2,
  XCircle,
  Clock,
  AlertTriangle,
  RotateCcw,
  Sparkles,
  Plus,
  Trash2,
  ChevronDown,
  ChevronRight,
  ShieldCheck,
  ListTodo,
  History,
  ExternalLink,
  Edit2,
} from "lucide-react";
import {
  issueLedgerOptions,
  useExtractACs,
  useCreateAC,
  useVerifyAC,
  useRejectAC,
  useDeleteAC,
  useCreateTodo,
  useUpdateTodo,
  useReopenTodo,
  useDeleteTodo,
} from "@multica/core/issues";
import type {
  IssueACStatus,
  IssueTodoStatus,
  IssueAcceptanceCriteria,
  IssueTodo,
} from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

export interface TaskLedgerSectionProps {
  issueId: string;
  description?: string | null;
}

export function TaskLedgerSection({
  issueId,
  description,
}: TaskLedgerSectionProps) {
  const { t } = useT("issues");
  const { data: ledger, isLoading } = useQuery(issueLedgerOptions(issueId));

  const extractMutation = useExtractACs(issueId);
  const createACMutation = useCreateAC(issueId);
  const verifyACMutation = useVerifyAC(issueId);
  const rejectACMutation = useRejectAC(issueId);
  const deleteACMutation = useDeleteAC(issueId);

  const createTodoMutation = useCreateTodo(issueId);
  const updateTodoMutation = useUpdateTodo(issueId);
  const reopenTodoMutation = useReopenTodo(issueId);
  const deleteTodoMutation = useDeleteTodo(issueId);

  // Section collapse states
  const [sectionOpen, setSectionOpen] = useState(true);
  const [auditOpen, setAuditOpen] = useState(false);

  // Dialog states
  const [addACOpen, setAddACOpen] = useState(false);
  const [acKey, setAcKey] = useState("");
  const [acTitle, setAcTitle] = useState("");

  const [verifyACOpen, setVerifyACOpen] = useState(false);
  const [targetAC, setTargetAC] = useState<IssueAcceptanceCriteria | null>(null);
  const [verifyEvidence, setVerifyEvidence] = useState("");

  const [rejectACOpen, setRejectACOpen] = useState(false);
  const [rejectReason, setRejectReason] = useState("");
  const [rejectEvidence, setRejectEvidence] = useState("");

  const [addTodoOpen, setAddTodoOpen] = useState(false);
  const [todoTitle, setTodoTitle] = useState("");
  const [todoNextStep, setTodoNextStep] = useState("");
  const [todoSelectedACs, setTodoSelectedACs] = useState<string[]>([]);

  const [editTodoOpen, setEditTodoOpen] = useState(false);
  const [targetTodo, setTargetTodo] = useState<IssueTodo | null>(null);
  const [editTodoStatus, setEditTodoStatus] = useState<IssueTodoStatus>("pending");
  const [editTodoTitle, setEditTodoTitle] = useState("");
  const [editTodoNextStep, setEditTodoNextStep] = useState("");
  const [editTodoBlockedReason, setEditTodoBlockedReason] = useState("");
  const [editTodoEvidenceUrl, setEditTodoEvidenceUrl] = useState("");
  const [editTodoACs, setEditTodoACs] = useState<string[]>([]);

  const handleExtractACs = () => {
    extractMutation.mutate({ description: description ?? undefined });
  };

  const handleCreateAC = () => {
    if (!acTitle.trim()) return;
    createACMutation.mutate(
      { key: acKey.trim() || undefined, title: acTitle.trim() },
      {
        onSuccess: () => {
          setAcKey("");
          setAcTitle("");
          setAddACOpen(false);
        },
      },
    );
  };

  const handleVerifyAC = () => {
    if (!targetAC) return;
    verifyACMutation.mutate(
      { acId: targetAC.id, data: { evidence: verifyEvidence.trim() || undefined } },
      {
        onSuccess: () => {
          setVerifyACOpen(false);
          setTargetAC(null);
          setVerifyEvidence("");
        },
      },
    );
  };

  const handleRejectAC = () => {
    if (!targetAC || !rejectReason.trim()) return;
    rejectACMutation.mutate(
      {
        acId: targetAC.id,
        data: {
          reason: rejectReason.trim(),
          evidence: rejectEvidence.trim() || undefined,
        },
      },
      {
        onSuccess: () => {
          setRejectACOpen(false);
          setTargetAC(null);
          setRejectReason("");
          setRejectEvidence("");
        },
      },
    );
  };

  const handleCreateTodo = () => {
    if (!todoTitle.trim()) return;
    createTodoMutation.mutate(
      {
        title: todoTitle.trim(),
        next_step: todoNextStep.trim() || undefined,
        ac_ids: todoSelectedACs,
      },
      {
        onSuccess: () => {
          setTodoTitle("");
          setTodoNextStep("");
          setTodoSelectedACs([]);
          setAddTodoOpen(false);
        },
      },
    );
  };

  const handleUpdateTodo = () => {
    if (!targetTodo) return;
    updateTodoMutation.mutate(
      {
        todoId: targetTodo.id,
        data: {
          title: editTodoTitle.trim(),
          status: editTodoStatus,
          next_step: editTodoNextStep.trim(),
          blocked_reason: editTodoBlockedReason.trim(),
          ac_ids: editTodoACs,
          evidence_urls: editTodoEvidenceUrl.trim()
            ? [editTodoEvidenceUrl.trim()]
            : undefined,
        },
      },
      {
        onSuccess: () => {
          setEditTodoOpen(false);
          setTargetTodo(null);
        },
      },
    );
  };

  const openEditTodo = (todo: IssueTodo) => {
    setTargetTodo(todo);
    setEditTodoTitle(todo.title);
    setEditTodoStatus(todo.status);
    setEditTodoNextStep(todo.next_step ?? "");
    setEditTodoBlockedReason(todo.blocked_reason ?? "");
    setEditTodoEvidenceUrl(todo.evidence?.[0]?.url ?? "");
    setEditTodoACs(todo.ac_ids ?? []);
    setEditTodoOpen(true);
  };

  const toggleACSelection = (
    acKeyVal: string,
    list: string[],
    setList: (val: string[]) => void,
  ) => {
    if (list.includes(acKeyVal)) {
      setList(list.filter((k) => k !== acKeyVal));
    } else {
      setList([...list, acKeyVal]);
    }
  };

  if (isLoading && !ledger) {
    return (
      <div className="rounded-xl border bg-card/40 p-4 animate-pulse">
        <div className="h-4 w-48 bg-muted rounded mb-2" />
        <div className="h-3 w-64 bg-muted/60 rounded" />
      </div>
    );
  }

  const acs = ledger?.acs ?? [];
  const todos = ledger?.todos ?? [];
  const audit = ledger?.audit ?? [];
  const summary = ledger?.summary;

  const renderACStatusBadge = (status: IssueACStatus) => {
    switch (status) {
      case "verified":
        return (
          <Badge
            variant="outline"
            className="gap-1 bg-emerald-500/15 text-emerald-600 dark:text-emerald-400 border-emerald-500/30 text-micro font-medium"
          >
            <CheckCircle2 className="h-3 w-3" />
            {t(($) => $.ledger.status_verified)}
          </Badge>
        );
      case "pending_verification":
        return (
          <Badge
            variant="outline"
            className="gap-1 bg-amber-500/15 text-amber-600 dark:text-amber-400 border-amber-500/30 text-micro font-medium"
          >
            <Clock className="h-3 w-3" />
            {t(($) => $.ledger.status_pending_verification)}
          </Badge>
        );
      case "rejected":
        return (
          <Badge
            variant="outline"
            className="gap-1 bg-rose-500/15 text-rose-600 dark:text-rose-400 border-rose-500/30 text-micro font-medium"
          >
            <XCircle className="h-3 w-3" />
            {t(($) => $.ledger.status_rejected)}
          </Badge>
        );
      case "uncompleted":
      default:
        return (
          <Badge
            variant="outline"
            className="bg-muted text-muted-foreground border-border text-micro font-medium"
          >
            {t(($) => $.ledger.status_uncompleted)}
          </Badge>
        );
    }
  };

  const renderTodoStatusBadge = (status: IssueTodoStatus) => {
    switch (status) {
      case "completed":
        return (
          <Badge
            variant="outline"
            className="gap-1 bg-emerald-500/15 text-emerald-600 dark:text-emerald-400 border-emerald-500/30 text-micro font-medium"
          >
            <CheckCircle2 className="h-3 w-3" />
            {t(($) => $.ledger.status_completed)}
          </Badge>
        );
      case "in_progress":
        return (
          <Badge
            variant="outline"
            className="gap-1 bg-blue-500/15 text-blue-600 dark:text-blue-400 border-blue-500/30 text-micro font-medium"
          >
            <Clock className="h-3 w-3" />
            {t(($) => $.ledger.status_in_progress)}
          </Badge>
        );
      case "blocked":
        return (
          <Badge
            variant="outline"
            className="gap-1 bg-rose-500/15 text-rose-600 dark:text-rose-400 border-rose-500/30 text-micro font-medium"
          >
            <AlertTriangle className="h-3 w-3" />
            {t(($) => $.ledger.status_blocked)}
          </Badge>
        );
      case "pending_verification":
        return (
          <Badge
            variant="outline"
            className="gap-1 bg-amber-500/15 text-amber-600 dark:text-amber-400 border-amber-500/30 text-micro font-medium"
          >
            <Clock className="h-3 w-3" />
            {t(($) => $.ledger.status_pending_verification)}
          </Badge>
        );
      case "pending":
      default:
        return (
          <Badge
            variant="outline"
            className="bg-muted text-muted-foreground border-border text-micro font-medium"
          >
            {t(($) => $.ledger.status_pending)}
          </Badge>
        );
    }
  };

  return (
    <div className="rounded-xl border bg-card/60 shadow-sm overflow-hidden mb-6">
      {/* Top Ledger Header */}
      <div className="flex flex-wrap items-center justify-between gap-3 p-4 bg-muted/20 border-b">
        <div className="flex items-center gap-2.5">
          <button
            type="button"
            onClick={() => setSectionOpen((v) => !v)}
            className="flex items-center gap-2 text-title-sm font-semibold hover:text-foreground/80 transition-colors"
          >
            <ShieldCheck className="h-5 w-5 text-primary" />
            <span>{t(($) => $.ledger.title)}</span>
            <ChevronDown
              className={cn(
                "h-4 w-4 text-muted-foreground transition-transform",
                !sectionOpen && "-rotate-90",
              )}
            />
          </button>

          {/* Quick Metrics */}
          {summary && (
            <div className="flex items-center gap-2 ml-2">
              <span className="text-micro px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 font-medium">
                AC: {summary.ac_verified}/{summary.ac_total}
              </span>
              <span className="text-micro px-2 py-0.5 rounded-full bg-blue-500/10 text-blue-600 dark:text-blue-400 font-medium">
                TODO: {summary.todo_completed}/{summary.todo_total}
              </span>
              {summary.todo_blocked > 0 && (
                <span className="text-micro px-2 py-0.5 rounded-full bg-rose-500/15 text-rose-600 dark:text-rose-400 font-medium flex items-center gap-1">
                  <AlertTriangle className="h-3 w-3" />
                  {summary.todo_blocked} {t(($) => $.ledger.status_blocked)}
                </span>
              )}
            </div>
          )}
        </div>

        {/* Global Actions */}
        <div className="flex items-center gap-2">
          <Button
            size="sm"
            variant="outline"
            className="h-8 gap-1.5 text-caption font-medium"
            onClick={handleExtractACs}
            disabled={extractMutation.isPending}
          >
            <Sparkles className="h-3.5 w-3.5 text-primary" />
            <span>
              {extractMutation.isPending
                ? t(($) => $.ledger.extracting)
                : t(($) => $.ledger.extract_from_desc)}
            </span>
          </Button>
          <Button
            size="sm"
            variant="ghost"
            className="h-8 gap-1 text-caption"
            onClick={() => setAddACOpen(true)}
          >
            <Plus className="h-3.5 w-3.5" />
            <span>{t(($) => $.ledger.add_ac)}</span>
          </Button>
          <Button
            size="sm"
            variant="ghost"
            className="h-8 gap-1 text-caption"
            onClick={() => setAddTodoOpen(true)}
          >
            <Plus className="h-3.5 w-3.5" />
            <span>{t(($) => $.ledger.add_todo)}</span>
          </Button>
        </div>
      </div>

      {sectionOpen && (
        <div className="p-4 space-y-6">
          {/* AC3 Business Constraint Reminder Banner */}
          <div className="flex items-center gap-2 text-micro px-3 py-1.5 rounded-md bg-amber-500/10 border border-amber-500/20 text-amber-800 dark:text-amber-300">
            <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
            <span>{t(($) => $.ledger.verification_warning)}</span>
          </div>

          {/* AC Subsection */}
          <div>
            <div className="flex items-center justify-between mb-3">
              <h3 className="text-body font-semibold flex items-center gap-2">
                <CheckCircle2 className="h-4 w-4 text-primary" />
                <span>{t(($) => $.ledger.ac_section)}</span>
                <span className="text-caption text-muted-foreground font-normal">
                  ({acs.length})
                </span>
              </h3>
            </div>

            {acs.length === 0 ? (
              <p className="text-caption text-muted-foreground italic py-2">
                {t(($) => $.ledger.no_acs)}
              </p>
            ) : (
              <div className="divide-y divide-border/60 rounded-lg border bg-card/40">
                {acs.map((ac) => (
                  <div key={ac.id} className="p-3 hover:bg-muted/30 transition-colors">
                    <div className="flex items-start justify-between gap-3">
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-2 flex-wrap mb-1">
                          <span className="font-mono text-caption font-bold px-1.5 py-0.5 rounded bg-muted text-foreground">
                            {ac.key}
                          </span>
                          <span className="text-body font-medium">{ac.title}</span>
                          {renderACStatusBadge(ac.status)}
                        </div>

                        {/* Rejection Note if any */}
                        {ac.status === "rejected" && ac.rejection_reason && (
                          <div className="mt-2 text-micro px-2.5 py-1 rounded bg-rose-500/10 text-rose-700 dark:text-rose-300 border border-rose-500/20 flex items-center gap-1.5">
                            <XCircle className="h-3.5 w-3.5 shrink-0" />
                            <span>
                              {t(($) => $.ledger.rejected_by, {
                                reason: ac.rejection_reason,
                              })}
                            </span>
                          </div>
                        )}

                        {/* Verification Evidence */}
                        {ac.evidence && ac.evidence.length > 0 && (
                          <div className="mt-2 space-y-1">
                            {ac.evidence.map((ev, idx) => (
                              <div
                                key={idx}
                                className="text-micro text-muted-foreground flex items-center gap-1.5 bg-muted/40 px-2 py-0.5 rounded w-fit"
                              >
                                <ExternalLink className="h-3 w-3" />
                                <span>[{ev.type}]</span>
                                {ev.url && (
                                  <a
                                    href={ev.url}
                                    target="_blank"
                                    rel="noreferrer"
                                    className="underline hover:text-foreground"
                                  >
                                    {ev.url}
                                  </a>
                                )}
                                {ev.note && <span>{ev.note}</span>}
                              </div>
                            ))}
                          </div>
                        )}
                      </div>

                      {/* AC Action buttons */}
                      <div className="flex items-center gap-1.5 shrink-0">
                        {ac.status !== "verified" && (
                          <Button
                            size="sm"
                            variant="outline"
                            className="h-7 px-2 text-micro font-medium text-emerald-600 hover:text-emerald-700 hover:bg-emerald-500/10 border-emerald-500/30"
                            onClick={() => {
                              setTargetAC(ac);
                              setVerifyEvidence("");
                              setVerifyACOpen(true);
                            }}
                          >
                            <CheckCircle2 className="h-3 w-3 mr-1" />
                            {t(($) => $.ledger.verify_action)}
                          </Button>
                        )}
                        {ac.status !== "rejected" && (
                          <Button
                            size="sm"
                            variant="outline"
                            className="h-7 px-2 text-micro font-medium text-rose-600 hover:text-rose-700 hover:bg-rose-500/10 border-rose-500/30"
                            onClick={() => {
                              setTargetAC(ac);
                              setRejectReason("");
                              setRejectEvidence("");
                              setRejectACOpen(true);
                            }}
                          >
                            <XCircle className="h-3 w-3 mr-1" />
                            {t(($) => $.ledger.reject_action)}
                          </Button>
                        )}
                        <Button
                          size="sm"
                          variant="ghost"
                          className="h-7 w-7 p-0 text-muted-foreground hover:text-destructive"
                          onClick={() => deleteACMutation.mutate(ac.id)}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* Dynamic TODO Subsection */}
          <div>
            <div className="flex items-center justify-between mb-3">
              <h3 className="text-body font-semibold flex items-center gap-2">
                <ListTodo className="h-4 w-4 text-primary" />
                <span>{t(($) => $.ledger.todo_section)}</span>
                <span className="text-caption text-muted-foreground font-normal">
                  ({todos.length})
                </span>
              </h3>
            </div>

            {todos.length === 0 ? (
              <p className="text-caption text-muted-foreground italic py-2">
                {t(($) => $.ledger.no_todos)}
              </p>
            ) : (
              <div className="divide-y divide-border/60 rounded-lg border bg-card/40">
                {todos.map((todo) => (
                  <div key={todo.id} className="p-3 hover:bg-muted/30 transition-colors">
                    <div className="flex items-start justify-between gap-3">
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-2 flex-wrap mb-1">
                          <span
                            className={cn(
                              "text-body font-medium",
                              todo.status === "completed" && "line-through text-muted-foreground",
                            )}
                          >
                            {todo.title}
                          </span>
                          {renderTodoStatusBadge(todo.status)}

                          {/* Associated AC tags */}
                          {todo.ac_ids && todo.ac_ids.length > 0 && (
                            <div className="flex items-center gap-1">
                              {todo.ac_ids.map((k) => (
                                <span
                                  key={k}
                                  className="text-micro font-mono px-1 py-0.2 rounded bg-primary/10 text-primary font-medium"
                                >
                                  {k}
                                </span>
                              ))}
                            </div>
                          )}
                        </div>

                        {/* Next Step Callout */}
                        {todo.next_step && (
                          <div className="mt-1.5 text-caption text-blue-600 dark:text-blue-400 flex items-center gap-1 font-medium">
                            <span className="text-muted-foreground">👉 {t(($) => $.ledger.next_step)}:</span>
                            <span>{todo.next_step}</span>
                          </div>
                        )}

                        {/* Blocked Reason Callout */}
                        {(todo.status === "blocked" || todo.blocked_reason) && (
                          <div className="mt-1.5 text-micro px-2.5 py-1 rounded bg-rose-500/10 text-rose-700 dark:text-rose-300 border border-rose-500/20 flex items-center gap-1.5">
                            <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
                            <span>
                              {t(($) => $.ledger.blocked_reason)}: {todo.blocked_reason}
                            </span>
                          </div>
                        )}
                      </div>

                      {/* TODO Actions */}
                      <div className="flex items-center gap-1.5 shrink-0">
                        {(todo.status === "completed" || todo.status === "blocked") && (
                          <Button
                            size="sm"
                            variant="outline"
                            className="h-7 px-2 text-micro font-medium"
                            onClick={() => reopenTodoMutation.mutate({ todoId: todo.id })}
                          >
                            <RotateCcw className="h-3 w-3 mr-1" />
                            {t(($) => $.ledger.reopen_action)}
                          </Button>
                        )}
                        <Button
                          size="sm"
                          variant="ghost"
                          className="h-7 w-7 p-0 text-muted-foreground hover:text-foreground"
                          onClick={() => openEditTodo(todo)}
                        >
                          <Edit2 className="h-3.5 w-3.5" />
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          className="h-7 w-7 p-0 text-muted-foreground hover:text-destructive"
                          onClick={() => deleteTodoMutation.mutate(todo.id)}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* Audit Trail Subsection (Collapsible) */}
          {audit.length > 0 && (
            <div className="pt-2 border-t border-border/40">
              <button
                type="button"
                onClick={() => setAuditOpen((v) => !v)}
                className="flex items-center gap-1.5 text-caption font-medium text-muted-foreground hover:text-foreground transition-colors mb-2"
              >
                <History className="h-3.5 w-3.5" />
                <span>{t(($) => $.ledger.audit_section)}</span>
                <span className="font-normal">({audit.length})</span>
                <ChevronRight
                  className={cn(
                    "h-3 w-3 transition-transform",
                    auditOpen && "rotate-90",
                  )}
                />
              </button>

              {auditOpen && (
                <div className="max-h-60 overflow-y-auto rounded-lg border bg-card/20 divide-y divide-border/40 text-micro">
                  {audit.map((entry) => (
                    <div key={entry.id} className="p-2 flex items-center justify-between gap-2">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="font-mono uppercase font-semibold text-muted-foreground">
                          [{entry.target_type}]
                        </span>
                        <span className="font-medium">{entry.action}</span>
                        {entry.from_status && entry.to_status && (
                          <span className="text-muted-foreground">
                            {entry.from_status} → {entry.to_status}
                          </span>
                        )}
                        {entry.note && (
                          <span className="italic text-muted-foreground">
                            "{entry.note}"
                          </span>
                        )}
                      </div>
                      <span className="text-muted-foreground text-micro shrink-0">
                        {entry.created_at?.slice(0, 19).replace("T", " ")}
                      </span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>
      )}

      {/* Verify AC Dialog */}
      <Dialog open={verifyACOpen} onOpenChange={setVerifyACOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.ledger.verify_action)}: {targetAC?.key}</DialogTitle>
            <DialogDescription>
              {targetAC?.title}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3 py-2">
            <div>
              <label className="text-caption font-medium mb-1 block">
                {t(($) => $.ledger.evidence)}
              </label>
              <Textarea
                value={verifyEvidence}
                onChange={(e) => setVerifyEvidence(e.target.value)}
                placeholder={t(($) => $.ledger.evidence_input_placeholder)}
                rows={3}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setVerifyACOpen(false)}>
              Cancel
            </Button>
            <Button
              className="bg-emerald-600 hover:bg-emerald-700 text-white"
              onClick={handleVerifyAC}
              disabled={verifyACMutation.isPending}
            >
              {t(($) => $.ledger.verify_action)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Reject AC Dialog */}
      <Dialog open={rejectACOpen} onOpenChange={setRejectACOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="text-rose-600">
              {t(($) => $.ledger.reject_action)}: {targetAC?.key}
            </DialogTitle>
            <DialogDescription>
              {targetAC?.title}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3 py-2">
            <div>
              <label className="text-caption font-medium mb-1 block">
                {t(($) => $.ledger.blocked_reason)} *
              </label>
              <Input
                value={rejectReason}
                onChange={(e) => setRejectReason(e.target.value)}
                placeholder={t(($) => $.ledger.rejection_reason_placeholder)}
              />
            </div>
            <div>
              <label className="text-caption font-medium mb-1 block">
                {t(($) => $.ledger.evidence)}
              </label>
              <Textarea
                value={rejectEvidence}
                onChange={(e) => setRejectEvidence(e.target.value)}
                placeholder={t(($) => $.ledger.evidence_input_placeholder)}
                rows={2}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setRejectACOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={handleRejectAC}
              disabled={!rejectReason.trim() || rejectACMutation.isPending}
            >
              {t(($) => $.ledger.reject_action)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Add AC Dialog */}
      <Dialog open={addACOpen} onOpenChange={setAddACOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.ledger.add_ac)}</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2">
            <div>
              <label className="text-caption font-medium mb-1 block">Key (e.g. AC1)</label>
              <Input
                value={acKey}
                onChange={(e) => setAcKey(e.target.value)}
                placeholder="AC1"
              />
            </div>
            <div>
              <label className="text-caption font-medium mb-1 block">Title *</label>
              <Textarea
                value={acTitle}
                onChange={(e) => setAcTitle(e.target.value)}
                placeholder="Acceptance criteria description..."
                rows={3}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setAddACOpen(false)}>
              Cancel
            </Button>
            <Button onClick={handleCreateAC} disabled={!acTitle.trim() || createACMutation.isPending}>
              {t(($) => $.ledger.add_ac)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Add TODO Dialog */}
      <Dialog open={addTodoOpen} onOpenChange={setAddTodoOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.ledger.add_todo)}</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2">
            <div>
              <label className="text-caption font-medium mb-1 block">Title *</label>
              <Input
                value={todoTitle}
                onChange={(e) => setTodoTitle(e.target.value)}
                placeholder="What needs to be done?"
              />
            </div>
            <div>
              <label className="text-caption font-medium mb-1 block">
                {t(($) => $.ledger.next_step)}
              </label>
              <Input
                value={todoNextStep}
                onChange={(e) => setTodoNextStep(e.target.value)}
                placeholder={t(($) => $.ledger.next_step_placeholder)}
              />
            </div>
            {acs.length > 0 && (
              <div>
                <label className="text-caption font-medium mb-1 block">
                  {t(($) => $.ledger.linked_acs)}
                </label>
                <div className="flex flex-wrap gap-1.5">
                  {acs.map((ac) => {
                    const selected = todoSelectedACs.includes(ac.key);
                    return (
                      <button
                        key={ac.id}
                        type="button"
                        onClick={() =>
                          toggleACSelection(ac.key, todoSelectedACs, setTodoSelectedACs)
                        }
                        className={cn(
                          "px-2 py-0.5 rounded text-micro font-mono border transition-colors",
                          selected
                            ? "bg-primary text-primary-foreground border-primary"
                            : "bg-muted text-muted-foreground border-border hover:bg-muted/80",
                        )}
                      >
                        {ac.key}
                      </button>
                    );
                  })}
                </div>
              </div>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setAddTodoOpen(false)}>
              Cancel
            </Button>
            <Button
              onClick={handleCreateTodo}
              disabled={!todoTitle.trim() || createTodoMutation.isPending}
            >
              {t(($) => $.ledger.add_todo)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit TODO Dialog */}
      <Dialog open={editTodoOpen} onOpenChange={setEditTodoOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Edit TODO</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2">
            <div>
              <label className="text-caption font-medium mb-1 block">Title</label>
              <Input
                value={editTodoTitle}
                onChange={(e) => setEditTodoTitle(e.target.value)}
              />
            </div>
            <div>
              <label className="text-caption font-medium mb-1 block">Status</label>
              <select
                value={editTodoStatus}
                onChange={(e) => setEditTodoStatus(e.target.value as IssueTodoStatus)}
                className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              >
                <option value="pending">{t(($) => $.ledger.status_pending)}</option>
                <option value="in_progress">{t(($) => $.ledger.status_in_progress)}</option>
                <option value="pending_verification">{t(($) => $.ledger.status_pending_verification)}</option>
                <option value="completed">{t(($) => $.ledger.status_completed)}</option>
                <option value="blocked">{t(($) => $.ledger.status_blocked)}</option>
              </select>
            </div>
            <div>
              <label className="text-caption font-medium mb-1 block">
                {t(($) => $.ledger.next_step)}
              </label>
              <Input
                value={editTodoNextStep}
                onChange={(e) => setEditTodoNextStep(e.target.value)}
                placeholder={t(($) => $.ledger.next_step_placeholder)}
              />
            </div>
            <div>
              <label className="text-caption font-medium mb-1 block">
                {t(($) => $.ledger.blocked_reason)}
              </label>
              <Input
                value={editTodoBlockedReason}
                onChange={(e) => setEditTodoBlockedReason(e.target.value)}
                placeholder={t(($) => $.ledger.blocked_reason_placeholder)}
              />
            </div>
            <div>
              <label className="text-caption font-medium mb-1 block">
                {t(($) => $.ledger.evidence)} URL
              </label>
              <Input
                value={editTodoEvidenceUrl}
                onChange={(e) => setEditTodoEvidenceUrl(e.target.value)}
                placeholder="https://..."
              />
            </div>
            {acs.length > 0 && (
              <div>
                <label className="text-caption font-medium mb-1 block">
                  {t(($) => $.ledger.linked_acs)}
                </label>
                <div className="flex flex-wrap gap-1.5">
                  {acs.map((ac) => {
                    const selected = editTodoACs.includes(ac.key);
                    return (
                      <button
                        key={ac.id}
                        type="button"
                        onClick={() =>
                          toggleACSelection(ac.key, editTodoACs, setEditTodoACs)
                        }
                        className={cn(
                          "px-2 py-0.5 rounded text-micro font-mono border transition-colors",
                          selected
                            ? "bg-primary text-primary-foreground border-primary"
                            : "bg-muted text-muted-foreground border-border hover:bg-muted/80",
                        )}
                      >
                        {ac.key}
                      </button>
                    );
                  })}
                </div>
              </div>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditTodoOpen(false)}>
              Cancel
            </Button>
            <Button onClick={handleUpdateTodo} disabled={updateTodoMutation.isPending}>
              Save Changes
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
