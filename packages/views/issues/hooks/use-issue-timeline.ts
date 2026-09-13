"use client";

import { useEffect, useRef, useCallback, useMemo } from "react";
import {
  useQuery,
  useQueryClient,
  useMutationState,
} from "@tanstack/react-query";
import type { QueryClient } from "@tanstack/react-query";
import type {
  Comment,
  TimelineEntry,
  Reaction,
} from "@multica/core/types";
import type {
  CommentCreatedPayload,
  CommentUpdatedPayload,
  CommentDeletedPayload,
  CommentResolvedPayload,
  CommentUnresolvedPayload,
  ActivityCreatedPayload,
  ReactionAddedPayload,
  ReactionRemovedPayload,
} from "@multica/core/types";
import {
  issueTimelineOptions,
  issueKeys,
} from "@multica/core/issues/queries";
import {
  useCreateComment,
  useUpdateComment,
  useDeleteComment,
  useResolveComment,
  useToggleCommentReaction,
  type ToggleCommentReactionVars,
} from "@multica/core/issues/mutations";
import { sortTimelineEntriesAsc } from "@multica/core/issues/timeline-sort";
import {
  crossedTimelineHardCap,
  effectiveTruncatedKinds,
  updateTimelineEntries,
  type TimelineQueryData,
  type TimelineTruncationKind,
} from "@multica/core/issues/timeline-query";
import {
  unhandledCommentTriggerOutcomes,
  mentionLabelsByTarget,
} from "@multica/core/issues/comment-trigger-outcomes";
import { useWSEvent, useWSReconnect } from "@multica/core/realtime";
import { toast } from "sonner";
import { useT } from "../../i18n";
import { blockedShortReasonLabel } from "../blocked-trigger-copy";

type TLCache = TimelineQueryData;

function commentToTimelineEntry(c: Comment): TimelineEntry {
  return {
    type: "comment",
    id: c.id,
    actor_type: c.author_type,
    actor_id: c.author_id,
    content: c.content,
    parent_id: c.parent_id,
    created_at: c.created_at,
    updated_at: c.updated_at,
    revision: c.revision,
    comment_type: c.type,
    reactions: c.reactions ?? [],
    attachments: c.attachments ?? [],
    resolved_at: c.resolved_at,
    resolved_by_type: c.resolved_by_type,
    resolved_by_id: c.resolved_by_id,
    source_task_id: c.source_task_id,
  };
}

function acceptsCommentRevision(
  current: TimelineEntry,
  incoming: Comment,
): boolean {
  return (
    current.revision === undefined ||
    (incoming.revision !== undefined && incoming.revision >= current.revision)
  );
}

function applyCommentSnapshot(
  qc: QueryClient,
  issueId: string,
  comment: Comment,
) {
  let missingRevision = false;
  qc.setQueryData<TLCache>(issueKeys.timeline(issueId), (old) =>
    updateTimelineEntries(old, (entries) =>
      entries.map((entry) => {
        if (entry.id !== comment.id) return entry;
        if (entry.revision !== undefined && comment.revision === undefined) {
          missingRevision = true;
          return entry;
        }
        return acceptsCommentRevision(entry, comment)
          ? {
              ...commentToTimelineEntry(comment),
              actor_name: entry.actor_name,
              actor_avatar_url: entry.actor_avatar_url,
            }
          : entry;
      }),
    ),
  );
  if (missingRevision) {
    // During a rolling deployment, an old server may emit an unversioned
    // snapshot after this cache has already observed a versioned one. Never
    // overwrite the ordered value; refetch from the authoritative API instead.
    qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
  }
}

function timelineTruncationKind(entry: TimelineEntry): TimelineTruncationKind | null {
  return entry.type === "comment" || entry.type === "activity" ? entry.type : null;
}

function appendTimelineEntry(
  qc: QueryClient,
  issueId: string,
  entry: TimelineEntry,
) {
  let crossedKind: TimelineTruncationKind | null = null;
  qc.setQueryData<TLCache>(issueKeys.timeline(issueId), (old) => {
    if (!old || old.entries.some((existing) => existing.id === entry.id)) return old;
    const nextEntries = sortTimelineEntriesAsc([...old.entries, entry]);
    const kind = timelineTruncationKind(entry);
    if (
      kind &&
      !old.truncatedKinds.includes(kind) &&
      crossedTimelineHardCap(old.entries, nextEntries, kind)
    ) {
      crossedKind = kind;
    }
    return updateTimelineEntries(old, () => nextEntries);
  });
  if (crossedKind) {
    qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
  }
}

export function useIssueTimeline(issueId: string, userId?: string) {
  const { t } = useT("issues");
  const qc = useQueryClient();

  const query = useQuery(issueTimelineOptions(issueId));
  const { data, isLoading: loading } = query;

  const timeline = useMemo<TimelineEntry[]>(() => data?.entries ?? [], [data]);
  const truncatedKinds = useMemo(() => effectiveTruncatedKinds(data), [data]);

  // Stable mutation handles. TanStack v5 returns a fresh result wrapper from
  // useMutation per render, but the inner mutateAsync / mutate functions are
  // stable. Pull just those so the useCallback identities downstream don't
  // flip on every parent re-render — listing the whole mutation object would
  // defeat React.memo on CommentCard.
  const { mutateAsync: createComment } = useCreateComment(issueId);
  const { mutateAsync: updateComment } = useUpdateComment(issueId);
  const { mutateAsync: deleteCommentAsync } = useDeleteComment(issueId);
  const { mutateAsync: resolveCommentAsync } = useResolveComment(issueId);
  const { mutate: toggleCommentReaction } = useToggleCommentReaction(issueId);

  // Reconnect recovery: invalidate so the next render refetches the full
  // timeline. Cheaper than diffing across a possibly-long disconnect.
  useWSReconnect(
    useCallback(() => {
      qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
    }, [qc, issueId]),
  );

  // --- WS event handlers ---

  useWSEvent(
    "comment:created",
    useCallback(
      (payload: unknown) => {
        const { comment } = payload as CommentCreatedPayload;
        if (comment.issue_id !== issueId) return;
        appendTimelineEntry(qc, issueId, commentToTimelineEntry(comment));
      },
      [qc, issueId],
    ),
  );

  useWSEvent(
    "comment:updated",
    useCallback(
      (payload: unknown) => {
        const { comment } = payload as CommentUpdatedPayload;
        if (comment.issue_id !== issueId) return;
        applyCommentSnapshot(qc, issueId, comment);
      },
      [qc, issueId],
    ),
  );

  // Granular handlers for comment:resolved / comment:unresolved. The payload
  // carries the full Comment with the new resolved_at/resolved_by_* fields,
  // which `commentToTimelineEntry` already preserves, so the existing
  // entry can simply be replaced in place. Without these handlers the only
  // path that updated the cache was `useRealtimeSync`'s global invalidate,
  // which forces a full timeline refetch and busts every CommentCard memo.
  useWSEvent(
    "comment:resolved",
    useCallback(
      (payload: unknown) => {
        const { comment } = payload as CommentResolvedPayload;
        if (comment.issue_id !== issueId) return;
        applyCommentSnapshot(qc, issueId, comment);
      },
      [qc, issueId],
    ),
  );

  useWSEvent(
    "comment:unresolved",
    useCallback(
      (payload: unknown) => {
        const { comment } = payload as CommentUnresolvedPayload;
        if (comment.issue_id !== issueId) return;
        applyCommentSnapshot(qc, issueId, comment);
      },
      [qc, issueId],
    ),
  );

  useWSEvent(
    "comment:deleted",
    useCallback(
      (payload: unknown) => {
        const { comment_id, issue_id } = payload as CommentDeletedPayload;
        if (issue_id !== issueId) return;
        qc.setQueryData<TLCache>(issueKeys.timeline(issueId), (old) => {
          if (!old) return old;
          // Cascade through replies (full timeline now lives in this single
          // cache, so a flat sweep is sufficient).
          const idsToRemove = new Set<string>([comment_id]);
          let changed = true;
          while (changed) {
            changed = false;
          for (const entry of old.entries) {
            if (
              entry.parent_id &&
              idsToRemove.has(entry.parent_id) &&
              !idsToRemove.has(entry.id)
            ) {
              idsToRemove.add(entry.id);
              changed = true;
            }
          }
        }
          return updateTimelineEntries(old, (entries) =>
            entries.filter((entry) => !idsToRemove.has(entry.id)),
          );
        });
      },
      [qc, issueId],
    ),
  );

  useWSEvent(
    "activity:created",
    useCallback(
      (payload: unknown) => {
        const p = payload as ActivityCreatedPayload;
        if (p.issue_id !== issueId) return;
        const entry = p.entry;
        if (!entry || !entry.id) return;
        appendTimelineEntry(qc, issueId, entry);
      },
      [qc, issueId],
    ),
  );

  useWSEvent(
    "reaction:added",
    useCallback(
      (payload: unknown) => {
        const { reaction, issue_id, comment_revision } = payload as ReactionAddedPayload;
        if (issue_id !== issueId) return;
        let missingRevision = false;
        qc.setQueryData<TLCache>(issueKeys.timeline(issueId), (old) =>
          updateTimelineEntries(old, (entries) =>
            entries.map((entry) => {
              if (entry.id !== reaction.comment_id) return entry;
              if (comment_revision === undefined && entry.revision !== undefined) {
                missingRevision = true;
                return entry;
              }
              if (
                comment_revision !== undefined &&
                entry.revision !== undefined &&
                comment_revision < entry.revision
              ) return entry;
              const existing = entry.reactions ?? [];
              if (existing.some((reactionEntry) => reactionEntry.id === reaction.id)) return entry;
              return {
                ...entry,
                revision: comment_revision ?? entry.revision,
                reactions: [...existing, reaction],
              };
            }),
          ),
        );
        if (missingRevision) {
          qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
        }
      },
      [qc, issueId],
    ),
  );

  useWSEvent(
    "reaction:removed",
    useCallback(
      (payload: unknown) => {
        const p = payload as ReactionRemovedPayload;
        if (p.issue_id !== issueId) return;
        let missingRevision = false;
        qc.setQueryData<TLCache>(issueKeys.timeline(issueId), (old) =>
          updateTimelineEntries(old, (entries) =>
            entries.map((entry) => {
              if (entry.id !== p.comment_id) return entry;
              if (p.comment_revision === undefined && entry.revision !== undefined) {
                missingRevision = true;
                return entry;
              }
              if (
                p.comment_revision !== undefined &&
                entry.revision !== undefined &&
                p.comment_revision < entry.revision
              ) return entry;
              return {
                ...entry,
                revision: p.comment_revision ?? entry.revision,
                reactions: (entry.reactions ?? []).filter(
                  (reaction) =>
                    !(
                      reaction.emoji === p.emoji &&
                      reaction.actor_type === p.actor_type &&
                      reaction.actor_id === p.actor_id
                    ),
                ),
              };
            }),
          ),
        );
        if (missingRevision) {
          qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
        }
      },
      [qc, issueId],
    ),
  );

  // --- Mutation functions ---

  // The comment saved, but a mention did not clearly trigger (blocked, or an
  // unknown/future status we must not assume succeeded). Warn instead of a
  // silent no-op (MUL-4525 §2): the comment IS posted, but N explicitly-named
  // targets were not triggered.
  const warnUnhandledTriggers = useCallback(
    (triggerOutcomes: unknown, content?: string) => {
      const unhandled = unhandledCommentTriggerOutcomes(triggerOutcomes);
      if (unhandled.length === 0) return;
      // Name the target when a single mention was refused — the posted comment's
      // markup carries the label the user typed (the wire outcome omits it for
      // enumeration-safety). Several refusals fall back to a count.
      if (unhandled.length === 1) {
        const outcome = unhandled[0]!;
        const name = mentionLabelsByTarget(content ?? "").get(
          `${outcome.target_type}:${outcome.target_id}`,
        );
        if (name) {
          toast.warning(
            t(($) => $.comment.posted_partial_trigger_named, {
              name,
              reason: blockedShortReasonLabel(outcome.reason_code, t),
            }),
          );
          return;
        }
      }
      toast.warning(
        t(($) => $.comment.posted_partial_trigger, { count: unhandled.length }),
      );
    },
    [t],
  );

  // Returns true on success, false on failure. The composer keeps the user's
  // text (editor locked + button spinning) until this settles and clears only
  // on success — so a slow send no longer leaves the box full next to an
  // already-posted comment, and a failed send keeps the draft.
  const submitComment = useCallback(
    async (content: string, attachmentIds?: string[], suppressAgentIds?: string[]): Promise<string | false> => {
      if (!content.trim() || !userId) return false;
      try {
        const comment = await createComment({ content, attachmentIds, suppressAgentIds });
        warnUnhandledTriggers(comment?.trigger_outcomes, comment?.content);
        return comment.id;
      } catch (err) {
        toast.error(
          err instanceof Error && err.message
            ? err.message
            : t(($) => $.comment.send_failed),
        );
        return false;
      }
    },
    [userId, createComment, warnUnhandledTriggers, t],
  );

  const submitReply = useCallback(
    async (parentId: string, content: string, attachmentIds?: string[], suppressAgentIds?: string[]): Promise<string | false> => {
      if (!content.trim() || !userId) return false;
      try {
        const comment = await createComment({
          content,
          type: "comment",
          parentId,
          attachmentIds,
          suppressAgentIds,
        });
        warnUnhandledTriggers(comment?.trigger_outcomes, comment?.content);
        return comment.id;
      } catch (err) {
        toast.error(
          err instanceof Error && err.message
            ? err.message
            : t(($) => $.comment.send_reply_failed),
        );
        return false;
      }
    },
    [userId, createComment, warnUnhandledTriggers, t],
  );

  const editComment = useCallback(
    async (commentId: string, content: string, attachmentIds: string[], suppressAgentIds?: string[], contentBase?: string) => {
      const comment = await updateComment({
        commentId,
        content,
        attachmentIds,
        suppressAgentIds,
        contentBase,
      });
      warnUnhandledTriggers(comment?.trigger_outcomes, comment?.content);
    },
    [updateComment, warnUnhandledTriggers],
  );

  const deleteComment = useCallback(
    async (commentId: string) => {
      try {
        await deleteCommentAsync(commentId);
      } catch (err) {
        toast.error(
          err instanceof Error && err.message
            ? err.message
            : t(($) => $.comment.delete_failed),
        );
      }
    },
    [deleteCommentAsync, t],
  );

  const toggleResolveComment = useCallback(
    async (commentId: string, resolved: boolean) => {
      try {
        await resolveCommentAsync({ commentId, resolved });
      } catch (err) {
        toast.error(
          err instanceof Error && err.message
            ? err.message
            : resolved
              ? t(($) => $.comment.resolve.resolve_failed)
              : t(($) => $.comment.resolve.unresolve_failed),
        );
      }
    },
    [resolveCommentAsync, t],
  );

  // --- Optimistic UI for comment reactions ---
  // Derive at render time from pending mutation variables instead of writing
  // temp data into the cache (which would race with WS events).

  const pendingReactionVars = useMutationState({
    filters: {
      mutationKey: ["toggleCommentReaction", issueId],
      status: "pending",
    },
    select: (m) =>
      m.state.variables as ToggleCommentReactionVars | undefined,
  });

  const optimisticTimeline = useMemo(() => {
    if (pendingReactionVars.length === 0) return timeline;

    return timeline.map((entry) => {
      const pendingForEntry = pendingReactionVars.filter(
        (v) => v && v.commentId === entry.id,
      );
      if (pendingForEntry.length === 0) return entry;

      let reactions = entry.reactions ?? [];
      for (const vars of pendingForEntry) {
        if (!vars) continue;
        if (vars.existing) {
          reactions = reactions.filter((r) => r.id !== vars.existing!.id);
        } else {
          const alreadyExists = reactions.some(
            (r) =>
              r.emoji === vars.emoji &&
              r.actor_type === "member" &&
              r.actor_id === userId,
          );
          if (!alreadyExists) {
            reactions = [
              ...reactions,
              {
                id: `optimistic-${vars.emoji}`,
                comment_id: vars.commentId,
                actor_type: "member",
                actor_id: userId ?? "",
                emoji: vars.emoji,
                created_at: "",
              },
            ];
          }
        }
      }
      return { ...entry, reactions };
    });
  }, [timeline, pendingReactionVars, userId]);

  // toggleReaction reads from a ref so its identity does not change with
  // every WS event. Without this every memoized CommentCard down-tree would
  // re-render on each timeline mutation, defeating the React.memo cost
  // savings on long timelines (#1968).
  const timelineRef = useRef(timeline);
  useEffect(() => {
    timelineRef.current = timeline;
  }, [timeline]);

  const toggleReaction = useCallback(
    async (commentId: string, emoji: string) => {
      if (!userId) return;
      const entry = timelineRef.current.find((e) => e.id === commentId);
      const existing: Reaction | undefined = (entry?.reactions ?? []).find(
        (r) =>
          r.emoji === emoji &&
          r.actor_type === "member" &&
          r.actor_id === userId,
      );
      toggleCommentReaction({ commentId, emoji, existing });
    },
    [userId, toggleCommentReaction],
  );

  return {
    timeline: optimisticTimeline,
    truncatedKinds,
    loading,
    submitComment,
    submitReply,
    editComment,
    deleteComment,
    toggleResolveComment,
    toggleReaction,
  };
}
