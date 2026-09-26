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
  promptQuizItemsOptions,
  useCreatePromptQuizItem,
  useDeletePromptQuizItem,
  useUpdatePromptQuizItem,
} from "@multica/core/self-evolution";
import type { PromptQuizItem } from "@multica/core/types";
import { useT } from "../../i18n";

/**
 * Quiz bank maintenance (RUYI-185).
 *
 * The bank is one fixed set of questions replayed against every prompt version;
 * editing a body is what makes two versions comparable or not, so the panel
 * shows each question's revision and offers retiring before deleting.
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
        pending={create.isPending}
        onOpenChange={setCreateOpen}
        onSubmit={(draft) => {
          create.mutate(
            { slug: draft.slug, title: draft.title, body: draft.body },
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
        item={editing}
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
  active: boolean;
}

/**
 * One question's editor.
 *
 * `key` on the dialog content resets the local draft when the subject changes,
 * so reopening on another question never shows the previous one's body.
 */
function QuizItemDialog({
  open,
  item,
  pending,
  onOpenChange,
  onSubmit,
}: {
  open: boolean;
  /** Null for a create. */
  item: PromptQuizItem | null;
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

function toDraft(item: PromptQuizItem | null): QuizItemDraft {
  return {
    slug: item?.slug ?? "",
    title: item?.title ?? "",
    body: item?.body ?? "",
    active: item?.active ?? true,
  };
}
