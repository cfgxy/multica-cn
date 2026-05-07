import type { Reaction } from "./comment";
import type { Attachment } from "./attachment";

export interface AssigneeFrequencyEntry {
  assignee_type: string;
  assignee_id: string;
  frequency: number;
}

export interface TimelineEntry {
  type: "activity" | "comment";
  id: string;
  actor_type: string;
  actor_id: string;
  created_at: string;
  // Activity fields
  action?: string;
  details?: Record<string, unknown>;
  // Comment fields
  content?: string;
  parent_id?: string | null;
  updated_at?: string;
  comment_type?: string;
  reactions?: Reaction[];
  attachments?: Attachment[];
  /** Set by frontend coalescing when consecutive identical activities are merged. */
  coalesced_count?: number;
}

/**
 * Cursor-paginated timeline page. Entries are newest-first
 * (created_at DESC, id DESC). Cursors are opaque base64 strings — pass them
 * back unchanged via TimelinePageParam.
 *
 * Frontend cache shape: comment-anchored V2 responses are translated into
 * this shape (entries = comments + activities merged DESC) at the query
 * layer so the existing hook and component code keep working unchanged.
 * V2-specific signals ride along on the optional fields below.
 */
export interface TimelinePage {
  entries: TimelineEntry[];
  next_cursor: string | null;
  prev_cursor: string | null;
  has_more_before: boolean;
  has_more_after: boolean;
  /** Set only in around-id mode; index of the anchor entry within `entries`. */
  target_index?: number;
  /** V2 around-mode anchor metadata. When the type is "activity" the
   *  consumer must auto-expand the folded group containing target.id
   *  before scrolling — otherwise the row sits inside a collapsed fold
   *  and the user sees the wrong context. */
  target?: { id: string; type: "comment" | "activity" } | null;
  /** V2 activity-window cap signal. Non-null means at least one activity
   *  was trimmed from the page; surfaces as "N+ system events" in the UI
   *  and hooks the Phase 4 lazy-load entry point. */
  activity_truncated_count?: number | null;
}

export type TimelinePageParam =
  | { mode: "latest" }
  | { mode: "before"; cursor: string }
  | { mode: "after"; cursor: string }
  | { mode: "around"; id: string };

/**
 * V2 (comment-anchored) timeline response. The pagination cursor walks the
 * comment stream; activities ride along inside the time window of the
 * returned comments. See docs/timeline-redesign-plan.md.
 */
export interface TimelineTarget {
  id: string;
  type: "comment" | "activity";
}

export interface TimelineV2Page {
  comments: TimelineEntry[];
  activities: TimelineEntry[];
  next_cursor: string | null;
  prev_cursor: string | null;
  has_more_before: boolean;
  has_more_after: boolean;
  /** Set when the page hit the per-window activity hard cap (≥1 unloaded). */
  activity_truncated_count?: number | null;
  /** Set in around-mode; identifies the anchor entry the client should
   *  scroll to (and, for activity anchors, expand the folded group of). */
  target?: TimelineTarget | null;
}
