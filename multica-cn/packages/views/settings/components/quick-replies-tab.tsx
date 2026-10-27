"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { GripVertical, MoreHorizontal, Pencil, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import {
  DndContext,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  arrayMove,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { useWorkspaceId } from "@multica/core/hooks";
import { useAuthStore } from "@multica/core/auth";
import { memberListOptions } from "@multica/core/workspace/queries";
import { quickReplyListOptions } from "@multica/core/quick-replies/queries";
import {
  useCreateQuickReply,
  useDeleteQuickReply,
  useReorderQuickReplies,
  useUpdateQuickReply,
} from "@multica/core/quick-replies/mutations";
import type { QuickReply } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Label as FieldLabel } from "@multica/ui/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { useT } from "../../i18n";
import { SettingsTab } from "./settings-layout";

/**
 * Workspace quick-reply catalog management (RUYI-435).
 *
 * Quick replies are the workspace's shared comment templates: every member
 * sees them in the composer menu, so the whole surface is read-open and
 * write-admin. The list is one flat, position-ordered table — unlike issue
 * statuses there is no category semantics to expose, only "what order do the
 * menu items appear in", which is exactly what the drag handle edits.
 *
 * The 5 seeded replies ship with every workspace (self-healed server-side on
 * first read), so the empty state only appears if an admin deliberately
 * deleted everything.
 */

interface ReplyDraft {
  name: string;
  content: string;
}

const EMPTY_DRAFT: ReplyDraft = { name: "", content: "" };

export function QuickRepliesTab() {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();

  const [editing, setEditing] = useState<QuickReply | null>(null);
  const [creating, setCreating] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<QuickReply | null>(null);

  const { data: replies = [], isLoading } = useQuery(quickReplyListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const currentUser = useAuthStore((s) => s.user);
  const myRole = useMemo(() => {
    if (!currentUser) return null;
    return members.find((m) => m.user_id === currentUser.id)?.role ?? null;
  }, [members, currentUser]);
  const isAdmin = myRole === "owner" || myRole === "admin";

  return (
    <SettingsTab
      title={t(($) => $.quick_replies.title)}
      description={t(($) => $.quick_replies.description)}
    >
      <div className="space-y-4">
        {isLoading ? (
          <div className="rounded-lg border border-surface-border bg-card px-4 py-12 text-center text-body text-muted-foreground">
            {t(($) => $.quick_replies.loading)}
          </div>
        ) : replies.length === 0 ? (
          <div className="rounded-lg border border-surface-border bg-card px-4 py-12 text-center">
            <p className="text-body font-medium">{t(($) => $.quick_replies.empty_title)}</p>
            <p className="mt-1 text-caption text-muted-foreground">
              {t(($) => $.quick_replies.empty_hint)}
            </p>
            {isAdmin && (
              <Button variant="outline" size="sm" className="mt-4" onClick={() => setCreating(true)}>
                <Plus className="size-4" />
                {t(($) => $.quick_replies.add)}
              </Button>
            )}
          </div>
        ) : (
          <ReplyList replies={replies} canManage={isAdmin} onEdit={setEditing} onDelete={setPendingDelete} />
        )}

        {isAdmin && replies.length > 0 && (
          <div className="flex justify-end">
            <Button variant="outline" size="sm" onClick={() => setCreating(true)}>
              <Plus className="size-4" />
              {t(($) => $.quick_replies.add)}
            </Button>
          </div>
        )}
      </div>

      <ReplyEditorDialog
        open={creating || Boolean(editing)}
        reply={editing}
        onOpenChange={(open) => {
          if (!open) {
            setCreating(false);
            setEditing(null);
          }
        }}
      />
      <DeleteReplyDialog reply={pendingDelete} onClose={() => setPendingDelete(null)} />
    </SettingsTab>
  );
}

function ReplyList({
  replies,
  canManage,
  onEdit,
  onDelete,
}: {
  replies: QuickReply[];
  canManage: boolean;
  onEdit: (reply: QuickReply) => void;
  onDelete: (reply: QuickReply) => void;
}) {
  const { t } = useT("settings");
  const reorder = useReorderQuickReplies();

  // Local order so the drag reads as instant even before the server confirms;
  // resynced whenever the server list changes.
  const [order, setOrder] = useState(replies);
  useEffect(() => setOrder(replies), [replies]);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
  );

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event;
    if (!over || active.id === over.id) return;
    const from = order.findIndex((r) => r.id === active.id);
    const to = order.findIndex((r) => r.id === over.id);
    if (from < 0 || to < 0) return;
    const next = arrayMove(order, from, to);
    setOrder(next);
    reorder.mutate(next.map((r) => r.id), {
      onError: (error) => {
        setOrder(replies);
        toast.error(
          error instanceof Error ? error.message : t(($) => $.quick_replies.reorder_failed),
        );
      },
    });
  };

  const canReorder = canManage && order.length > 1;

  return (
    <div className="overflow-hidden rounded-lg border border-surface-border bg-card">
      <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
        <SortableContext items={order.map((r) => r.id)} strategy={verticalListSortingStrategy}>
          {order.map((reply) => (
            <ReplyRow
              key={reply.id}
              reply={reply}
              canManage={canManage}
              canReorder={canReorder}
              onEdit={() => onEdit(reply)}
              onDelete={() => onDelete(reply)}
            />
          ))}
        </SortableContext>
      </DndContext>
    </div>
  );
}

function ReplyRow({
  reply,
  canManage,
  canReorder,
  onEdit,
  onDelete,
}: {
  reply: QuickReply;
  canManage: boolean;
  canReorder: boolean;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const { t } = useT("settings");
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: reply.id,
    disabled: !canReorder,
  });

  return (
    <div
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      className={`group/row relative flex min-h-12 items-center gap-3 border-b border-surface-border px-4 py-2 last:border-b-0 ${isDragging ? "z-10 bg-card shadow-[var(--surface-shadow)]" : ""}`}
    >
      {canReorder && (
        <button
          type="button"
          aria-label={t(($) => $.quick_replies.actions.reorder, { name: reply.name })}
          className="absolute left-0 top-1/2 flex w-4 -translate-y-1/2 cursor-grab justify-center text-faint-foreground opacity-0 transition-opacity group-hover/row:opacity-100 focus-visible:opacity-100 active:cursor-grabbing"
          {...attributes}
          {...listeners}
        >
          <GripVertical className="size-4" />
        </button>
      )}
      <div className="min-w-0 flex-1">
        <p className="truncate text-body font-medium">{reply.name}</p>
        {/* Content preview: what the member actually gets filled into their
            composer. First line only — long template bodies are the norm. */}
        <p className="truncate text-caption text-muted-foreground">
          {reply.content.split("\n")[0]}
        </p>
      </div>
      {canManage && (
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={t(($) => $.quick_replies.actions.open, { name: reply.name })}
              >
                <MoreHorizontal className="size-4" />
              </Button>
            }
          />
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={onEdit}>
              <Pencil className="size-4" />
              {t(($) => $.quick_replies.actions.edit)}
            </DropdownMenuItem>
            <DropdownMenuItem variant="destructive" onClick={onDelete}>
              <Trash2 className="size-4" />
              {t(($) => $.quick_replies.actions.delete)}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  );
}

function ReplyEditorDialog({
  open,
  reply,
  onOpenChange,
}: {
  open: boolean;
  reply: QuickReply | null;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("settings");
  const create = useCreateQuickReply();
  const update = useUpdateQuickReply();
  const [draft, setDraft] = useState<ReplyDraft>(EMPTY_DRAFT);

  useEffect(() => {
    if (!open) return;
    setDraft(reply ? { name: reply.name, content: reply.content } : { ...EMPTY_DRAFT });
  }, [reply, open]);

  const submit = () => {
    const name = draft.name.trim();
    const content = draft.content.trim();
    if (!name || !content) return;
    const onError = (error: unknown) =>
      toast.error(
        error instanceof Error ? error.message : t(($) => $.quick_replies.editor.save_failed),
      );

    if (reply) {
      update.mutate(
        { id: reply.id, data: { name, content } },
        { onSuccess: () => onOpenChange(false), onError },
      );
      return;
    }
    create.mutate(
      { name, content },
      { onSuccess: () => onOpenChange(false), onError },
    );
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {reply
              ? t(($) => $.quick_replies.editor.edit_title)
              : t(($) => $.quick_replies.editor.create_title)}
          </DialogTitle>
          <DialogDescription>{t(($) => $.quick_replies.editor.hint)}</DialogDescription>
        </DialogHeader>
        <div className="space-y-5 py-2">
          <div className="space-y-2">
            <FieldLabel htmlFor="quick-reply-name">
              {t(($) => $.quick_replies.editor.name)}
            </FieldLabel>
            <Input
              id="quick-reply-name"
              autoFocus
              maxLength={64}
              value={draft.name}
              onChange={(event) =>
                setDraft((current) => ({ ...current, name: event.target.value }))
              }
              placeholder={t(($) => $.quick_replies.editor.name_placeholder)}
            />
          </div>
          <div className="space-y-2">
            <FieldLabel htmlFor="quick-reply-content">
              {t(($) => $.quick_replies.editor.content)}
            </FieldLabel>
            <Textarea
              id="quick-reply-content"
              rows={6}
              maxLength={10000}
              value={draft.content}
              onChange={(event) =>
                setDraft((current) => ({ ...current, content: event.target.value }))
              }
              placeholder={t(($) => $.quick_replies.editor.content_placeholder)}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t(($) => $.quick_replies.editor.cancel)}
          </Button>
          <Button
            onClick={submit}
            disabled={!draft.name.trim() || !draft.content.trim() || create.isPending || update.isPending}
          >
            {create.isPending || update.isPending
              ? t(($) => $.quick_replies.editor.saving)
              : t(($) => $.quick_replies.editor.save)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function DeleteReplyDialog({
  reply,
  onClose,
}: {
  reply: QuickReply | null;
  onClose: () => void;
}) {
  const { t } = useT("settings");
  const remove = useDeleteQuickReply();
  return (
    <AlertDialog open={Boolean(reply)} onOpenChange={(open) => !open && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t(($) => $.quick_replies.delete_dialog.title)}</AlertDialogTitle>
          <AlertDialogDescription>
            {t(($) => $.quick_replies.delete_dialog.description, { name: reply?.name ?? "" })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t(($) => $.quick_replies.delete_dialog.cancel)}</AlertDialogCancel>
          <AlertDialogAction
            onClick={() => {
              if (!reply) return;
              remove.mutate(reply.id, {
                onSuccess: onClose,
                onError: (error) =>
                  toast.error(
                    error instanceof Error
                      ? error.message
                      : t(($) => $.quick_replies.delete_dialog.failed),
                  ),
              });
            }}
          >
            {t(($) => $.quick_replies.delete_dialog.confirm)}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
