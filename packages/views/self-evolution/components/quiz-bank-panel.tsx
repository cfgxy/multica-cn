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
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { clientErrorMessage } from "@multica/core/api";
import {
  promptQuizItemOptions,
  promptQuizItemsOptions,
  quizDiscrimination,
  useCreatePromptQuizItem,
  useDeletePromptQuizItem,
  useUpdatePromptQuizItem,
} from "@multica/core/self-evolution";
import type { PromptQuizItem, PromptQuizItemDetail } from "@multica/core/types";
import { useT } from "../../i18n";

/**
 * Quiz bank maintenance (RUYI-185).
 *
 * The bank is one fixed set of questions replayed against every prompt version;
 * editing a body is what makes two versions comparable or not, so the panel
 * shows each question's revision and offers retiring before deleting.
 *
 * Each row also carries the question's discrimination mark (A4): a question whose
 * readings no longer spread cannot tell two prompt versions apart, and this list
 * is where the decision to reword or retire it is made.
 *
 * The editor loads the question through the owner-only single read rather than
 * from a list row. The list has no `rubric` field at all — the answer key never
 * travels to a member-visible surface — and an update replaces `rubric` whole, so
 * a form seeded from a list row would save an empty answer key over the stored
 * one.
 *
 * The server refuses a body that names a production entity — an issue key, a
 * bare UUID, a mention link, a URL — and its refusal names the kind and offset
 * without echoing the text (Owner Q8). That message is surfaced verbatim rather
 * than replaced with a generic one: the author needs to know WHICH reference to
 * remove, and this build must not have its own copy of the rule to drift from
 * the server's.
 */
export function QuizBankPanel({
  wsId,
  canManage,
}: {
  wsId: string;
  /** Owner-only, matching the server's route guard. */
  canManage: boolean;
}) {
  const { t } = useT("self-evolution");
  const items = useQuery(promptQuizItemsOptions(wsId));
  const create = useCreatePromptQuizItem(wsId);
  const update = useUpdatePromptQuizItem(wsId);
  const remove = useDeletePromptQuizItem(wsId);

  const [editing, setEditing] = useState<PromptQuizItem | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  // The private half lives only behind this read; the query is disabled until a
  // question is being edited.
  const editingDetail = useQuery(promptQuizItemOptions(wsId, editing?.id ?? ""));

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="text-body font-medium">{t(($) => $.quiz.bank.title)}</h3>
        <span className="text-caption text-muted-foreground">
          {t(($) => $.quiz.bank.description)}
        </span>
        {canManage ? (
          <Button size="sm" variant="outline" onClick={() => setCreateOpen(true)}>
            {t(($) => $.quiz.bank.add)}
          </Button>
        ) : null}
      </div>

      {items.isPending ? (
        <div className="flex flex-col gap-2">
          {Array.from({ length: 3 }, (_, i) => (
            <Skeleton key={i} className="h-16 w-full rounded-lg" />
          ))}
        </div>
      ) : items.isError ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>{t(($) => $.quiz.bank.error.title)}</EmptyTitle>
          </EmptyHeader>
          <Button size="sm" variant="outline" onClick={() => void items.refetch()}>
            {t(($) => $.quality.error.retry)}
          </Button>
        </Empty>
      ) : (items.data ?? []).length === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>{t(($) => $.quiz.bank.empty.title)}</EmptyTitle>
            <EmptyDescription>{t(($) => $.quiz.bank.empty.description)}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <ul className="flex flex-col gap-2">
          {(items.data ?? []).map((item) => (
            <li
              key={item.id}
              className="flex flex-wrap items-center gap-2 rounded-md border border-border px-3 py-2"
              data-testid={`quiz-item-${item.slug}`}
            >
              <span className="text-caption font-medium">{item.title}</span>
              <Badge variant="outline" className="font-mono">
                {item.slug}
              </Badge>
              <Badge variant="outline">
                {t(($) => $.quiz.bank.revision, { revision: item.revision })}
              </Badge>
              <Badge variant={item.active === true ? "secondary" : "outline"}>
                {item.active === true
                  ? t(($) => $.quiz.bank.active)
                  : t(($) => $.quiz.bank.retired)}
              </Badge>
              <Badge variant="outline" data-testid={`quiz-item-discrimination-${item.slug}`}>
                {t(($) => $.quiz.bank.discrimination[quizDiscrimination(item.discrimination)])}
              </Badge>
              {canManage ? (
                <div className="ml-auto flex gap-1">
                  <Button size="sm" variant="ghost" onClick={() => setEditing(item)}>
                    {t(($) => $.quiz.bank.edit)}
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={remove.isPending}
                    onClick={() => {
                      remove.mutate(item.id, {
                        onError: (e) =>
                          toast.error(
                            clientErrorMessage(e) ?? t(($) => $.quiz.bank.error.write),
                          ),
                      });
                    }}
                  >
                    {t(($) => $.quiz.bank.delete)}
                  </Button>
                </div>
              ) : null}
            </li>
          ))}
        </ul>
      )}

      <QuizItemDialog
        open={createOpen}
        item={null}
        ready
        pending={create.isPending}
        onOpenChange={setCreateOpen}
        onSubmit={(draft) => {
          create.mutate(
            { slug: draft.slug, title: draft.title, body: draft.body, rubric: draft.rubric },
            {
              onSuccess: () => setCreateOpen(false),
              onError: (e) =>
                toast.error(clientErrorMessage(e) ?? t(($) => $.quiz.bank.error.write)),
            },
          );
        }}
      />
      <QuizItemDialog
        open={editing !== null}
        item={editingDetail.data ?? null}
        ready={editing !== null && editingDetail.isSuccess}
        pending={update.isPending}
        onOpenChange={(next) => {
          if (!next) setEditing(null);
        }}
        onSubmit={(draft) => {
          if (editing === null) return;
          update.mutate(
            {
              itemId: editing.id,
              patch: {
                title: draft.title,
                body: draft.body,
                // Sent back explicitly: the server replaces the stored rubric
                // with what this field holds, so omitting it clears the answer
                // key of a question that was only being retitled.
                rubric: draft.rubric,
                runtime_profile: editing.runtime_profile,
                active: draft.active,
              },
            },
            {
              onSuccess: () => setEditing(null),
              onError: (e) =>
                toast.error(clientErrorMessage(e) ?? t(($) => $.quiz.bank.error.write)),
            },
          );
        }}
      />
    </section>
  );
}

interface QuizItemDraft {
  slug: string;
  title: string;
  body: string;
  /** The expected answer and grading points. Never leaves the grading side. */
  rubric: string;
  active: boolean;
}

/**
 * One question's editor.
 *
 * The draft resets when the subject changes, so reopening on another question
 * never shows the previous one's body — and an edit whose single read has not
 * arrived yet cannot be saved, because saving an empty draft would overwrite the
 * stored body and answer key with blanks.
 */
function QuizItemDialog({
  open,
  item,
  ready,
  pending,
  onOpenChange,
  onSubmit,
}: {
  open: boolean;
  /** Null for a create, and for an edit whose single read has not arrived. */
  item: PromptQuizItemDetail | null;
  /** False while the question being edited is still loading. */
  ready: boolean;
  pending: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (draft: QuizItemDraft) => void;
}) {
  const { t } = useT("self-evolution");
  const [draft, setDraft] = useState<QuizItemDraft>(() => toDraft(item));
  const [subject, setSubject] = useState(item?.id ?? "");

  if ((item?.id ?? "") !== subject) {
    setSubject(item?.id ?? "");
    setDraft(toDraft(item));
  }

  const editing = item !== null;
  const canSubmit =
    ready &&
    draft.title.trim() !== "" &&
    draft.body.trim() !== "" &&
    (editing || draft.slug.trim() !== "");

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>
            {editing ? t(($) => $.quiz.bank.dialog.editTitle) : t(($) => $.quiz.bank.dialog.addTitle)}
          </DialogTitle>
          <DialogDescription>{t(($) => $.quiz.bank.dialog.description)}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          {editing ? null : (
            <label className="flex flex-col gap-1.5">
              <span className="text-caption text-muted-foreground">
                {t(($) => $.quiz.bank.dialog.slug)}
              </span>
              <Input
                value={draft.slug}
                onChange={(e) => setDraft((d) => ({ ...d, slug: e.target.value }))}
              />
            </label>
          )}
          <label className="flex flex-col gap-1.5">
            <span className="text-caption text-muted-foreground">
              {t(($) => $.quiz.bank.dialog.questionTitle)}
            </span>
            <Input
              value={draft.title}
              onChange={(e) => setDraft((d) => ({ ...d, title: e.target.value }))}
            />
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="text-caption text-muted-foreground">
              {t(($) => $.quiz.bank.dialog.body)}
            </span>
            <Textarea
              rows={8}
              value={draft.body}
              onChange={(e) => setDraft((d) => ({ ...d, body: e.target.value }))}
            />
            <span className="text-caption text-muted-foreground">
              {t(($) => $.quiz.bank.dialog.bodyHint)}
            </span>
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="text-caption text-muted-foreground">
              {t(($) => $.quiz.bank.dialog.rubric)}
            </span>
            <Textarea
              rows={4}
              value={draft.rubric}
              onChange={(e) => setDraft((d) => ({ ...d, rubric: e.target.value }))}
            />
            <span className="text-caption text-muted-foreground">
              {t(($) => $.quiz.bank.dialog.rubricHint)}
            </span>
          </label>
          {editing ? (
            <Button
              size="sm"
              variant={draft.active ? "secondary" : "outline"}
              className="self-start"
              onClick={() => setDraft((d) => ({ ...d, active: !d.active }))}
            >
              {draft.active ? t(($) => $.quiz.bank.active) : t(($) => $.quiz.bank.retired)}
            </Button>
          ) : null}
        </div>
        <DialogFooter>
          <Button size="sm" variant="ghost" onClick={() => onOpenChange(false)}>
            {t(($) => $.quiz.bank.dialog.cancel)}
          </Button>
          <Button size="sm" disabled={!canSubmit || pending} onClick={() => onSubmit(draft)}>
            {t(($) => $.quiz.bank.dialog.save)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function toDraft(item: PromptQuizItemDetail | null): QuizItemDraft {
  return {
    slug: item?.slug ?? "",
    title: item?.title ?? "",
    body: item?.body ?? "",
    rubric: item?.rubric ?? "",
    active: item?.active ?? true,
  };
}
