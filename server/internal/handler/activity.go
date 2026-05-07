package handler

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TimelineEntry represents a single entry in the issue timeline, which can be
// either an activity log record or a comment.
type TimelineEntry struct {
	Type string `json:"type"` // "activity" or "comment"
	ID   string `json:"id"`

	ActorType string `json:"actor_type"`
	ActorID   string `json:"actor_id"`
	CreatedAt string `json:"created_at"`

	// Activity-only fields
	Action  *string         `json:"action,omitempty"`
	Details json.RawMessage `json:"details,omitempty"`

	// Comment-only fields
	Content     *string              `json:"content,omitempty"`
	ParentID    *string              `json:"parent_id,omitempty"`
	UpdatedAt   *string              `json:"updated_at,omitempty"`
	CommentType *string              `json:"comment_type,omitempty"`
	Reactions   []ReactionResponse   `json:"reactions,omitempty"`
	Attachments []AttachmentResponse `json:"attachments,omitempty"`
}

// TimelineResponse wraps the cursor-paginated timeline. Entries are sorted
// newest-first (created_at DESC, id DESC). NextCursor / PrevCursor are opaque
// strings; clients pass them back as ?before= / ?after= without inspection.
// HasMoreBefore indicates more entries older than the last in the page;
// HasMoreAfter indicates more entries newer than the first in the page.
type TimelineResponse struct {
	Entries       []TimelineEntry `json:"entries"`
	NextCursor    *string         `json:"next_cursor"`
	PrevCursor    *string         `json:"prev_cursor"`
	HasMoreBefore bool            `json:"has_more_before"`
	HasMoreAfter  bool            `json:"has_more_after"`
	// TargetIndex is set only in ?around=<id> mode, locating the anchor entry
	// within Entries so the client can scroll/highlight without searching.
	TargetIndex *int `json:"target_index,omitempty"`
}

const (
	timelineDefaultLimit = 50
	timelineMaxLimit     = 100

	// V2 (comment-anchored) pagination tunables. comment_limit caps how many
	// comments a single page carries; activities ride along in the page's
	// time window without occupying the comment quota. activityHardCap
	// bounds the per-page activity payload so a single dense issue can't
	// blow the response budget — clients lazy-load past the cap via the
	// dedicated /activities endpoint.
	timelineV2DefaultCommentLimit = 20
	timelineV2MaxCommentLimit     = 100
	timelineV2ActivityHardCap     = 500
	// timelineV2ActivityCeiling caps how high a client can raise the
	// activity quota when explicitly opting into a denser payload. Beyond
	// this we'd risk timeouts and OOM on the renderer side.
	timelineV2ActivityCeiling = 5000
	// fallbackActivityLimit is used by V2 latest mode when the issue has
	// zero comments (pure-automation issue) — the page degrades to "latest
	// N activities", same as V1 default.
	fallbackActivityLimit = 50
)

// timelineCursor encodes a (created_at, id) keyset position as opaque base64
// JSON. The format is intentionally hidden from clients so future schema
// evolution (e.g. switching to a sequence column) can replace the cursor
// payload without breaking API consumers.
type timelineCursor struct {
	CreatedAt time.Time `json:"t"`
	ID        string    `json:"i"`
}

func encodeTimelineCursor(t pgtype.Timestamptz, id pgtype.UUID) string {
	c := timelineCursor{CreatedAt: t.Time, ID: uuidToString(id)}
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeTimelineCursor(s string) (pgtype.Timestamptz, pgtype.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, err
	}
	var c timelineCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, err
	}
	id, err := parseUUIDStrict(c.ID)
	if err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, err
	}
	return pgtype.Timestamptz{Time: c.CreatedAt, Valid: true}, id, nil
}

// parseUUIDStrict mirrors util.ParseUUID but returns a pgtype.UUID directly
// without panicking on bad input. Used for cursor decoding where invalid data
// is a 400, not a 500.
func parseUUIDStrict(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	if !u.Valid {
		return pgtype.UUID{}, errors.New("invalid uuid")
	}
	return u, nil
}

// ListTimeline returns a cursor-paginated, newest-first slice of the issue
// timeline (comments + activities merged). The query string accepts at most
// one of: ?before=<cursor>, ?after=<cursor>, ?around=<entry_id>. With none,
// the latest page is returned.
func (h *Handler) ListTimeline(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, id)
	if !ok {
		return
	}

	q := r.URL.Query()

	// Backwards-compat: pre-#2128 clients (Multica.app ≤ v0.2.25 and any cached
	// web build older than the matching server) call /timeline with no query
	// string and consume the response body as TimelineEntry[] directly. The
	// new client always sends ?limit=... or ?comment_limit=..., so absence of
	// every pagination param uniquely identifies a legacy caller. Drop this
	// branch once the desktop auto-update has rolled the user base past
	// v0.2.26.
	if q.Get("limit") == "" && q.Get("comment_limit") == "" && q.Get("before") == "" &&
		q.Get("after") == "" && q.Get("around") == "" {
		h.listTimelineLegacy(w, r, issue)
		return
	}

	before, after, around := q.Get("before"), q.Get("after"), q.Get("around")
	modes := 0
	for _, s := range []string{before, after, around} {
		if s != "" {
			modes++
		}
	}
	if modes > 1 {
		writeError(w, http.StatusBadRequest, "before, after, and around are mutually exclusive")
		return
	}

	// V2 (comment-anchored) dispatch. Selected by comment_limit query param;
	// returns a {comments, activities, ...} response shape instead of the V1
	// {entries: [...]} mixed list. Cursors emitted by V2 endpoints encode a
	// comment's (created_at, id) and can only be passed back to V2 endpoints.
	if rawCL := q.Get("comment_limit"); rawCL != "" {
		commentLimit, err := strconv.Atoi(rawCL)
		if err != nil || commentLimit <= 0 {
			writeError(w, http.StatusBadRequest, "invalid comment_limit")
			return
		}
		if commentLimit > timelineV2MaxCommentLimit {
			writeError(w, http.StatusBadRequest, "comment_limit exceeds maximum of 100")
			return
		}
		// Optional activity quota override. The frontend bumps this when the
		// user explicitly opts into loading more activities (the "load all
		// system events" affordance Phase 4 wires up). Defaults to the
		// per-page hard cap so unprivileged paths can't blow the payload.
		activityLimit := timelineV2ActivityHardCap
		if rawAL := q.Get("activity_limit"); rawAL != "" {
			n, err := strconv.Atoi(rawAL)
			if err != nil || n <= 0 {
				writeError(w, http.StatusBadRequest, "invalid activity_limit")
				return
			}
			if n > timelineV2ActivityCeiling {
				writeError(w, http.StatusBadRequest, "activity_limit exceeds ceiling")
				return
			}
			activityLimit = n
		}
		switch {
		case around != "":
			h.listTimelineAroundV2(w, r, issue, around, commentLimit, activityLimit)
		case before != "":
			h.listTimelineBeforeV2(w, r, issue, before, commentLimit, activityLimit)
		case after != "":
			h.listTimelineAfterV2(w, r, issue, after, commentLimit, activityLimit)
		default:
			h.listTimelineLatestV2(w, r, issue, commentLimit, activityLimit)
		}
		return
	}

	limit := timelineDefaultLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if n > timelineMaxLimit {
			writeError(w, http.StatusBadRequest, "limit exceeds maximum of 100")
			return
		}
		limit = n
	}

	switch {
	case around != "":
		h.listTimelineAround(w, r, issue, around, limit)
	case before != "":
		h.listTimelineBefore(w, r, issue, before, limit)
	case after != "":
		h.listTimelineAfter(w, r, issue, after, limit)
	default:
		h.listTimelineLatest(w, r, issue, limit)
	}
}

// listTimelineLegacy serves clients that predate cursor pagination (#2128) —
// notably Multica.app ≤ v0.2.25, where the renderer reads the response body
// as TimelineEntry[] directly and would crash with "timeline.filter is not a
// function" against the new wrapped shape (#2143, #2147). Returned bounded
// at legacyTimelineCap to honour the spirit of #1968 — old clients couldn't
// render thousands of entries without freezing the tab anyway.
func (h *Handler) listTimelineLegacy(w http.ResponseWriter, r *http.Request, issue db.Issue) {
	const legacyTimelineCap = 200
	ctx := r.Context()
	comments, err := h.Queries.ListCommentsLatest(ctx, db.ListCommentsLatestParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID, Limit: legacyTimelineCap,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list comments")
		return
	}
	activities, err := h.Queries.ListActivitiesLatest(ctx, db.ListActivitiesLatestParams{
		IssueID: issue.ID, Limit: legacyTimelineCap,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list activities")
		return
	}
	entries := h.mergeTimelineDesc(r, comments, activities, legacyTimelineCap)
	// Old contract: ASC (oldest → newest).
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	// Old client does `data: timeline = []` which defaults undefined, not
	// null — render an empty issue as "[]" not "null".
	if entries == nil {
		entries = []TimelineEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// listTimelineLatest fetches the most recent <limit> entries (no cursor).
// Both tables are queried for <limit> rows each; the merge picks the top
// <limit> overall. Any item the merge didn't include cannot rank higher than
// the worst kept item in either pool, so this is exact, not approximate.
func (h *Handler) listTimelineLatest(w http.ResponseWriter, r *http.Request, issue db.Issue, limit int) {
	ctx := r.Context()
	comments, err := h.Queries.ListCommentsLatest(ctx, db.ListCommentsLatestParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID, Limit: int32(limit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list comments")
		return
	}
	activities, err := h.Queries.ListActivitiesLatest(ctx, db.ListActivitiesLatestParams{
		IssueID: issue.ID, Limit: int32(limit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list activities")
		return
	}

	entries := h.mergeTimelineDesc(r, comments, activities, limit)
	resp := TimelineResponse{Entries: entries}
	resp.HasMoreBefore = hasMoreBeyond(len(comments), len(activities), len(entries), limit)
	if resp.HasMoreBefore && len(entries) > 0 {
		c := encodeTimelineCursor(entryTimestamp(entries[len(entries)-1]), entryID(entries[len(entries)-1]))
		resp.NextCursor = &c
	}
	if len(entries) > 0 {
		c := encodeTimelineCursor(entryTimestamp(entries[0]), entryID(entries[0]))
		resp.PrevCursor = &c
	}
	// has_more_after is always false on the latest page by definition.
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) listTimelineBefore(w http.ResponseWriter, r *http.Request, issue db.Issue, cursor string, limit int) {
	ctx := r.Context()
	t, id, err := decodeTimelineCursor(cursor)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return
	}

	comments, err := h.Queries.ListCommentsBefore(ctx, db.ListCommentsBeforeParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
		Column3: t, Column4: id, Limit: int32(limit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list comments")
		return
	}
	activities, err := h.Queries.ListActivitiesBefore(ctx, db.ListActivitiesBeforeParams{
		IssueID: issue.ID, Column2: t, Column3: id, Limit: int32(limit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list activities")
		return
	}

	entries := h.mergeTimelineDesc(r, comments, activities, limit)
	resp := TimelineResponse{
		Entries:      entries,
		HasMoreAfter: true, // we're paging older from a known position, so newer exists
	}
	resp.HasMoreBefore = hasMoreBeyond(len(comments), len(activities), len(entries), limit)
	if resp.HasMoreBefore && len(entries) > 0 {
		c := encodeTimelineCursor(entryTimestamp(entries[len(entries)-1]), entryID(entries[len(entries)-1]))
		resp.NextCursor = &c
	}
	if len(entries) > 0 {
		c := encodeTimelineCursor(entryTimestamp(entries[0]), entryID(entries[0]))
		resp.PrevCursor = &c
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) listTimelineAfter(w http.ResponseWriter, r *http.Request, issue db.Issue, cursor string, limit int) {
	ctx := r.Context()
	t, id, err := decodeTimelineCursor(cursor)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return
	}

	comments, err := h.Queries.ListCommentsAfter(ctx, db.ListCommentsAfterParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
		Column3: t, Column4: id, Limit: int32(limit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list comments")
		return
	}
	activities, err := h.Queries.ListActivitiesAfter(ctx, db.ListActivitiesAfterParams{
		IssueID: issue.ID, Column2: t, Column3: id, Limit: int32(limit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list activities")
		return
	}

	// Both queries returned ASC (older→newer). Merge ASC, take the limit
	// closest to the cursor (i.e. the oldest of the "after" set), then
	// reverse to DESC for the response.
	entries := h.mergeTimelineAscThenReverse(r, comments, activities, limit)
	resp := TimelineResponse{Entries: entries, HasMoreBefore: true}
	resp.HasMoreAfter = hasMoreBeyond(len(comments), len(activities), len(entries), limit)
	if resp.HasMoreAfter && len(entries) > 0 {
		c := encodeTimelineCursor(entryTimestamp(entries[0]), entryID(entries[0]))
		resp.PrevCursor = &c
	}
	if len(entries) > 0 {
		c := encodeTimelineCursor(entryTimestamp(entries[len(entries)-1]), entryID(entries[len(entries)-1]))
		resp.NextCursor = &c
	}
	writeJSON(w, http.StatusOK, resp)
}

// listTimelineAround anchors a window of size <limit> on a target entry,
// returning roughly half before and half after plus the target itself.
// This is the Inbox-jump / deep-link path: the target entry can be deep in
// the timeline, but the response is bounded so the browser never freezes.
func (h *Handler) listTimelineAround(w http.ResponseWriter, r *http.Request, issue db.Issue, targetID string, limit int) {
	ctx := r.Context()
	target, err := parseUUIDStrict(targetID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid around id")
		return
	}

	// Resolve the target's (created_at, id). It can be either a comment or
	// an activity; we don't ask the client to disambiguate.
	var anchorTime pgtype.Timestamptz
	var anchorID pgtype.UUID
	if c, cErr := h.Queries.GetCommentInWorkspace(ctx, db.GetCommentInWorkspaceParams{
		ID: target, WorkspaceID: issue.WorkspaceID,
	}); cErr == nil && c.IssueID == issue.ID {
		anchorTime, anchorID = c.CreatedAt, c.ID
	} else if a, aErr := h.Queries.GetActivity(ctx, target); aErr == nil &&
		a.IssueID == issue.ID && a.WorkspaceID == issue.WorkspaceID {
		anchorTime, anchorID = a.CreatedAt, a.ID
	} else {
		// Neither comment nor activity matched (or wrong workspace/issue).
		// Don't leak existence — return 404 like other resource lookups.
		if cErr != nil && !errors.Is(cErr, pgx.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, "failed to resolve target")
			return
		}
		writeError(w, http.StatusNotFound, "timeline entry not found")
		return
	}

	half := limit / 2
	if half < 1 {
		half = 1
	}
	beforeLimit := half
	afterLimit := limit - half - 1 // -1 for the anchor itself
	if afterLimit < 0 {
		afterLimit = 0
	}

	// Older half: keyset Before (anchor exclusive).
	olderComments, err := h.Queries.ListCommentsBefore(ctx, db.ListCommentsBeforeParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
		Column3: anchorTime, Column4: anchorID, Limit: int32(beforeLimit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list comments")
		return
	}
	olderActivities, err := h.Queries.ListActivitiesBefore(ctx, db.ListActivitiesBeforeParams{
		IssueID: issue.ID, Column2: anchorTime, Column3: anchorID, Limit: int32(beforeLimit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list activities")
		return
	}
	olderEntries := h.mergeTimelineDesc(r, olderComments, olderActivities, beforeLimit)

	// Newer half: keyset After (anchor exclusive).
	newerComments, err := h.Queries.ListCommentsAfter(ctx, db.ListCommentsAfterParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
		Column3: anchorTime, Column4: anchorID, Limit: int32(afterLimit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list comments")
		return
	}
	newerActivities, err := h.Queries.ListActivitiesAfter(ctx, db.ListActivitiesAfterParams{
		IssueID: issue.ID, Column2: anchorTime, Column3: anchorID, Limit: int32(afterLimit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list activities")
		return
	}
	newerEntries := h.mergeTimelineAscThenReverse(r, newerComments, newerActivities, afterLimit)

	// Build the anchor entry inline using the existing single-entry path.
	anchorEntry, ok := h.fetchSingleEntry(r, issue, target)
	if !ok {
		writeError(w, http.StatusInternalServerError, "failed to fetch anchor")
		return
	}

	// Final stitch: newer (DESC) + anchor + older (DESC).
	entries := make([]TimelineEntry, 0, len(newerEntries)+1+len(olderEntries))
	entries = append(entries, newerEntries...)
	entries = append(entries, anchorEntry)
	entries = append(entries, olderEntries...)
	targetIdx := len(newerEntries)

	resp := TimelineResponse{
		Entries:       entries,
		HasMoreBefore: hasMoreBeyond(len(olderComments), len(olderActivities), len(olderEntries), beforeLimit),
		HasMoreAfter:  hasMoreBeyond(len(newerComments), len(newerActivities), len(newerEntries), afterLimit),
		TargetIndex:   &targetIdx,
	}
	if resp.HasMoreBefore {
		c := encodeTimelineCursor(entryTimestamp(entries[len(entries)-1]), entryID(entries[len(entries)-1]))
		resp.NextCursor = &c
	}
	if resp.HasMoreAfter {
		c := encodeTimelineCursor(entryTimestamp(entries[0]), entryID(entries[0]))
		resp.PrevCursor = &c
	}
	writeJSON(w, http.StatusOK, resp)
}

// fetchSingleEntry materializes a single TimelineEntry (comment or activity)
// for the around-mode anchor. Reactions/attachments come from the same batch
// helpers so the rendering is identical to the merge path.
func (h *Handler) fetchSingleEntry(r *http.Request, issue db.Issue, id pgtype.UUID) (TimelineEntry, bool) {
	ctx := r.Context()
	if c, err := h.Queries.GetCommentInWorkspace(ctx, db.GetCommentInWorkspaceParams{
		ID: id, WorkspaceID: issue.WorkspaceID,
	}); err == nil && c.IssueID == issue.ID {
		return h.commentsToEntries(r, []db.Comment{c})[0], true
	}
	if a, err := h.Queries.GetActivity(ctx, id); err == nil &&
		a.IssueID == issue.ID && a.WorkspaceID == issue.WorkspaceID {
		return activityToEntry(a), true
	}
	return TimelineEntry{}, false
}

// hasMoreBeyond reports whether entries exist beyond the page on the side the
// caller is paginating away from (older for "before", newer for "after").
//
// Three independent signals, any of which means "more rows exist":
//  1. comments >= limit — the comments query was capped, DB has more.
//  2. activities >= limit — the activities query was capped, DB has more.
//  3. comments+activities > entries — the in-memory merge dropped rows that
//     could not all fit in the page (#2192). This is the case the original
//     formula missed, which made older comments unreachable when neither
//     individual query hit the limit but their combined total exceeded it.
func hasMoreBeyond(comments, activities, entries, limit int) bool {
	if limit <= 0 {
		return false
	}
	return comments >= limit || activities >= limit || comments+activities > entries
}

// mergeTimelineDesc takes comments + activities sorted DESC by (created_at, id)
// and returns the top <limit> merged entries, also DESC. Items the merge does
// not include cannot rank higher than the worst kept item in either pool, so
// the result is exact.
func (h *Handler) mergeTimelineDesc(r *http.Request, comments []db.Comment, activities []db.ActivityLog, limit int) []TimelineEntry {
	out := make([]TimelineEntry, 0, len(comments)+len(activities))
	out = append(out, h.commentsToEntries(r, comments)...)
	for _, a := range activities {
		out = append(out, activityToEntry(a))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// mergeTimelineAscThenReverse takes comments + activities sorted ASC by
// (created_at, id) — the natural shape of an "after" keyset query — picks
// the <limit> closest to the cursor (i.e. earliest of the after-set), and
// returns them DESC for response consistency.
func (h *Handler) mergeTimelineAscThenReverse(r *http.Request, comments []db.Comment, activities []db.ActivityLog, limit int) []TimelineEntry {
	out := make([]TimelineEntry, 0, len(comments)+len(activities))
	out = append(out, h.commentsToEntries(r, comments)...)
	for _, a := range activities {
		out = append(out, activityToEntry(a))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	// Reverse to DESC.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// commentsToEntries fetches reactions + attachments for the given comments in
// one batch each and returns enriched TimelineEntry slices preserving order.
func (h *Handler) commentsToEntries(r *http.Request, comments []db.Comment) []TimelineEntry {
	if len(comments) == 0 {
		return nil
	}
	ids := make([]pgtype.UUID, len(comments))
	for i, c := range comments {
		ids[i] = c.ID
	}
	reactions := h.groupReactions(r, ids)
	attachments := h.groupAttachments(r, ids)

	out := make([]TimelineEntry, len(comments))
	for i, c := range comments {
		content := c.Content
		commentType := c.Type
		updatedAt := timestampToString(c.UpdatedAt)
		cid := uuidToString(c.ID)
		out[i] = TimelineEntry{
			Type:        "comment",
			ID:          cid,
			ActorType:   c.AuthorType,
			ActorID:     uuidToString(c.AuthorID),
			Content:     &content,
			CommentType: &commentType,
			ParentID:    uuidToPtr(c.ParentID),
			CreatedAt:   timestampToString(c.CreatedAt),
			UpdatedAt:   &updatedAt,
			Reactions:   reactions[cid],
			Attachments: attachments[cid],
		}
	}
	return out
}

func activityToEntry(a db.ActivityLog) TimelineEntry {
	action := a.Action
	actorType := ""
	if a.ActorType.Valid {
		actorType = a.ActorType.String
	}
	return TimelineEntry{
		Type:      "activity",
		ID:        uuidToString(a.ID),
		ActorType: actorType,
		ActorID:   uuidToString(a.ActorID),
		Action:    &action,
		Details:   a.Details,
		CreatedAt: timestampToString(a.CreatedAt),
	}
}

// entryTimestamp / entryID extract the cursor components for an emitted
// TimelineEntry. CreatedAt is already an RFC3339 string at this point;
// re-parse it for cursor encoding.
func entryTimestamp(e TimelineEntry) pgtype.Timestamptz {
	t, _ := time.Parse(time.RFC3339Nano, e.CreatedAt)
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func entryID(e TimelineEntry) pgtype.UUID {
	id, _ := parseUUIDStrict(e.ID)
	return id
}

// =============================================================================
// V2 timeline: comment-anchored pagination
// =============================================================================
//
// V1 paged a mixed entries[] stream by DB-row count, which let activity bursts
// crowd comments out of the visible window (#2192). V2 paginates by comment
// count and returns activities as a separate array bounded by the time window
// of the returned comments. The frontend interleaves them and folds dense
// activity runs (Phase 2). See docs/timeline-redesign-plan.md.

// TimelineTarget locates the around-mode anchor inside the response. The
// anchor can be a comment or an activity; the frontend uses Type to decide
// whether to scroll to a comment row or to expand a folded activity group
// before scrolling.
type TimelineTarget struct {
	ID   string `json:"id"`
	Type string `json:"type"` // "comment" | "activity"
}

// TimelineResponseV2 carries comments and activities as separate arrays so
// the frontend can interleave + fold without losing track of which entries
// the pagination cursor advances through. Cursors encode a comment's
// (created_at, id) — opaque to the client, but only valid against V2
// endpoints (passing one to a V1 ?limit=... call will resolve to the wrong
// page).
type TimelineResponseV2 struct {
	Comments               []TimelineEntry `json:"comments"`
	Activities             []TimelineEntry `json:"activities"`
	NextCursor             *string         `json:"next_cursor"`
	PrevCursor             *string         `json:"prev_cursor"`
	HasMoreBefore          bool            `json:"has_more_before"`
	HasMoreAfter           bool            `json:"has_more_after"`
	ActivityTruncatedCount *int            `json:"activity_truncated_count,omitempty"`
	Target                 *TimelineTarget `json:"target,omitempty"`
}

// activitiesToEntries maps DB activity rows to TimelineEntry, preserving
// order. The V1 `mergeTimelineDesc` does this inline via activityToEntry;
// V2 keeps the two streams separate so we need a small helper.
func activitiesToEntries(activities []db.ActivityLog) []TimelineEntry {
	out := make([]TimelineEntry, 0, len(activities))
	for _, a := range activities {
		out = append(out, activityToEntry(a))
	}
	return out
}

// fetchActivitiesWithCap runs an activity query that has been padded with
// limit+1 to detect truncation. Returns the trimmed activities and a non-nil
// pointer when the result was capped. The truncated count is exposed as a
// minimum ("≥1 more") rather than the exact remaining count — getting the
// exact count would require a second COUNT(*) query, which is wasted IO when
// the frontend only renders "N+ system events" anyway.
func fetchActivitiesWithCap(activities []db.ActivityLog, cap int) ([]db.ActivityLog, *int) {
	if len(activities) <= cap {
		return activities, nil
	}
	one := 1
	return activities[:cap], &one
}

// listTimelineLatestV2 returns the latest <commentLimit> comments and every
// activity newer-or-equal to the oldest of those comments. When the issue
// has zero comments the page degrades to the latest <fallbackActivityLimit>
// activities — the V1 default — so pure-automation issues still render.
//
// activityLimit caps the activity slice independently of the comment quota
// so a single dense issue can't blow the response budget; clients raise it
// only when the user explicitly opts into a larger payload (Phase 4 "load
// more system events" affordance).
func (h *Handler) listTimelineLatestV2(w http.ResponseWriter, r *http.Request, issue db.Issue, commentLimit, activityLimit int) {
	ctx := r.Context()

	comments, err := h.Queries.ListCommentsLatest(ctx, db.ListCommentsLatestParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID, Limit: int32(commentLimit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list comments")
		return
	}

	var activities []db.ActivityLog
	var truncated *int
	if len(comments) == 0 {
		activities, err = h.Queries.ListActivitiesLatest(ctx, db.ListActivitiesLatestParams{
			IssueID: issue.ID, Limit: int32(fallbackActivityLimit),
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list activities")
			return
		}
	} else {
		oldest := comments[len(comments)-1]
		raw, err := h.Queries.ListActivitiesSince(ctx, db.ListActivitiesSinceParams{
			IssueID: issue.ID, Column2: oldest.CreatedAt,
			Limit: int32(activityLimit + 1),
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list activities")
			return
		}
		activities, truncated = fetchActivitiesWithCap(raw, activityLimit)
	}

	resp := TimelineResponseV2{
		Comments:               h.commentsToEntries(r, comments),
		Activities:             activitiesToEntries(activities),
		ActivityTruncatedCount: truncated,
	}
	// has_more_before is comment-anchored: another page of comments may exist
	// if we filled the comment quota. Conservative — a partial page can still
	// flip true on the boundary, but the client just sees an empty next page.
	if len(comments) >= commentLimit {
		resp.HasMoreBefore = true
		oldest := comments[len(comments)-1]
		c := encodeTimelineCursor(oldest.CreatedAt, oldest.ID)
		resp.NextCursor = &c
	}
	if len(comments) > 0 {
		newest := comments[0]
		c := encodeTimelineCursor(newest.CreatedAt, newest.ID)
		resp.PrevCursor = &c
	}
	// has_more_after is always false on the latest page by definition.
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) listTimelineBeforeV2(w http.ResponseWriter, r *http.Request, issue db.Issue, cursor string, commentLimit, activityLimit int) {
	ctx := r.Context()
	t, id, err := decodeTimelineCursor(cursor)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return
	}

	comments, err := h.Queries.ListCommentsBefore(ctx, db.ListCommentsBeforeParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
		Column3: t, Column4: id, Limit: int32(commentLimit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list comments")
		return
	}

	resp := TimelineResponseV2{
		HasMoreAfter: true, // we paged older from a known position, newer always exists
	}

	if len(comments) == 0 {
		// No more comments — return an empty page. has_more_before stays false.
		resp.Comments = []TimelineEntry{}
		resp.Activities = []TimelineEntry{}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Activities for this page sit in (oldest_in_page.t .. cursor) keyset
	// window, with the upper boundary excluded so activities exactly at
	// cursor.t (which were on the previous page) don't double-count.
	oldest := comments[len(comments)-1]
	raw, err := h.Queries.ListActivitiesInBeforeWindow(ctx, db.ListActivitiesInBeforeWindowParams{
		IssueID: issue.ID,
		Column2: t, Column3: id,
		Column4: oldest.CreatedAt,
		Limit:   int32(activityLimit + 1),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list activities")
		return
	}
	activities, truncated := fetchActivitiesWithCap(raw, activityLimit)

	resp.Comments = h.commentsToEntries(r, comments)
	resp.Activities = activitiesToEntries(activities)
	resp.ActivityTruncatedCount = truncated

	if len(comments) >= commentLimit {
		resp.HasMoreBefore = true
		c := encodeTimelineCursor(oldest.CreatedAt, oldest.ID)
		resp.NextCursor = &c
	}
	newest := comments[0]
	c := encodeTimelineCursor(newest.CreatedAt, newest.ID)
	resp.PrevCursor = &c
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) listTimelineAfterV2(w http.ResponseWriter, r *http.Request, issue db.Issue, cursor string, commentLimit, activityLimit int) {
	ctx := r.Context()
	t, id, err := decodeTimelineCursor(cursor)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return
	}

	// Walk newer in ASC order, then re-orient to DESC for the response.
	commentsAsc, err := h.Queries.ListCommentsAfter(ctx, db.ListCommentsAfterParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
		Column3: t, Column4: id, Limit: int32(commentLimit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list comments")
		return
	}

	resp := TimelineResponseV2{
		HasMoreBefore: true, // we paged newer from a known position, older always exists
	}

	if len(commentsAsc) == 0 {
		resp.Comments = []TimelineEntry{}
		resp.Activities = []TimelineEntry{}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// commentsAsc is ASC; for activity bounding we need newest+oldest, then
	// reverse to DESC for the response.
	oldest := commentsAsc[0]
	newest := commentsAsc[len(commentsAsc)-1]
	commentsDesc := make([]db.Comment, len(commentsAsc))
	for i, c := range commentsAsc {
		commentsDesc[len(commentsAsc)-1-i] = c
	}

	// Activities for this page span [oldest.t, newest.t]. Inclusive on both
	// sides — the cursor entry is a comment one step OLDER, so its timestamp
	// is strictly less than oldest.t and not in this window anyway.
	raw, err := h.Queries.ListActivitiesInRange(ctx, db.ListActivitiesInRangeParams{
		IssueID: issue.ID,
		Column2: oldest.CreatedAt,
		Column3: newest.CreatedAt,
		Limit:   int32(activityLimit + 1),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list activities")
		return
	}
	activities, truncated := fetchActivitiesWithCap(raw, activityLimit)

	resp.Comments = h.commentsToEntries(r, commentsDesc)
	resp.Activities = activitiesToEntries(activities)
	resp.ActivityTruncatedCount = truncated
	if len(commentsAsc) >= commentLimit {
		resp.HasMoreAfter = true
		c := encodeTimelineCursor(newest.CreatedAt, newest.ID)
		resp.PrevCursor = &c
	}
	c := encodeTimelineCursor(oldest.CreatedAt, oldest.ID)
	resp.NextCursor = &c
	writeJSON(w, http.StatusOK, resp)
}

// listTimelineAroundV2 anchors a window of <commentLimit> comments centered on
// the target (which may itself be a comment or an activity). Activity anchors
// are resolved to the nearest comment for windowing; the original anchor is
// preserved in resp.Target so the frontend can scroll to and auto-expand the
// folded group containing it.
func (h *Handler) listTimelineAroundV2(w http.ResponseWriter, r *http.Request, issue db.Issue, targetID string, commentLimit, activityLimit int) {
	ctx := r.Context()
	target, err := parseUUIDStrict(targetID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid around id")
		return
	}

	// Resolve target type + position. The around-anchor can be either a
	// comment or an activity; we don't ask the client to disambiguate.
	var (
		anchorTime pgtype.Timestamptz
		anchorID   pgtype.UUID
		targetType string
	)
	if c, cErr := h.Queries.GetCommentInWorkspace(ctx, db.GetCommentInWorkspaceParams{
		ID: target, WorkspaceID: issue.WorkspaceID,
	}); cErr == nil && c.IssueID == issue.ID {
		anchorTime, anchorID = c.CreatedAt, c.ID
		targetType = "comment"
	} else if a, aErr := h.Queries.GetActivity(ctx, target); aErr == nil &&
		a.IssueID == issue.ID && a.WorkspaceID == issue.WorkspaceID {
		anchorTime, anchorID = a.CreatedAt, a.ID
		targetType = "activity"
	} else {
		if cErr != nil && !errors.Is(cErr, pgx.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, "failed to resolve target")
			return
		}
		writeError(w, http.StatusNotFound, "timeline entry not found")
		return
	}

	half := commentLimit / 2
	if half < 1 {
		half = 1
	}

	// Older + newer comments around the anchor. For comment anchors we also
	// include the anchor itself in the response by querying Before with the
	// anchor's keyset (exclusive) and Get-ing the anchor separately. For
	// activity anchors the comment list is straightforward older/newer.
	olderComments, err := h.Queries.ListCommentsBefore(ctx, db.ListCommentsBeforeParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
		Column3: anchorTime, Column4: anchorID, Limit: int32(half),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list comments")
		return
	}
	newerCommentsAsc, err := h.Queries.ListCommentsAfter(ctx, db.ListCommentsAfterParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
		Column3: anchorTime, Column4: anchorID, Limit: int32(half),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list comments")
		return
	}
	// Newer is ASC from the query; flip to DESC so the response is
	// uniformly DESC.
	newerCommentsDesc := make([]db.Comment, len(newerCommentsAsc))
	for i, c := range newerCommentsAsc {
		newerCommentsDesc[len(newerCommentsAsc)-1-i] = c
	}

	// Stitch comments. Anchor comment goes between newer and older when it
	// exists; for activity anchors there's no anchor comment to insert, the
	// list is just newer + older.
	var stitched []db.Comment
	stitched = append(stitched, newerCommentsDesc...)
	if targetType == "comment" {
		c, err := h.Queries.GetCommentInWorkspace(ctx, db.GetCommentInWorkspaceParams{
			ID: target, WorkspaceID: issue.WorkspaceID,
		})
		if err == nil {
			stitched = append(stitched, c)
		}
	}
	stitched = append(stitched, olderComments...)

	// Activity time window. With comments around the anchor, span is
	// [oldest_in_window, newest_in_window]. With zero comments (pure-activity
	// issue) we fall back to ±half activities around the anchor.
	resp := TimelineResponseV2{
		Target:        &TimelineTarget{ID: targetID, Type: targetType},
		HasMoreBefore: len(olderComments) >= half,
		HasMoreAfter:  len(newerCommentsAsc) >= half,
	}

	if len(stitched) == 0 {
		// Fallback: pure-activity issue. Around-mode windows ±half activities
		// and lets the frontend show them all flat (no folding context exists).
		olderActs, _ := h.Queries.ListActivitiesBefore(ctx, db.ListActivitiesBeforeParams{
			IssueID: issue.ID, Column2: anchorTime, Column3: anchorID, Limit: int32(half),
		})
		newerActsAsc, _ := h.Queries.ListActivitiesAfter(ctx, db.ListActivitiesAfterParams{
			IssueID: issue.ID, Column2: anchorTime, Column3: anchorID, Limit: int32(half),
		})
		// Flip newer ASC → DESC.
		newerActsDesc := make([]db.ActivityLog, len(newerActsAsc))
		for i, a := range newerActsAsc {
			newerActsDesc[len(newerActsAsc)-1-i] = a
		}
		var allActs []db.ActivityLog
		allActs = append(allActs, newerActsDesc...)
		if targetType == "activity" {
			if a, aErr := h.Queries.GetActivity(ctx, target); aErr == nil {
				allActs = append(allActs, a)
			}
		}
		allActs = append(allActs, olderActs...)
		resp.Comments = []TimelineEntry{}
		resp.Activities = activitiesToEntries(allActs)
		// Pagination cursors point at the boundary activities; clients walk via
		// V1 endpoints in this fallback.
		resp.HasMoreBefore = len(olderActs) >= half
		resp.HasMoreAfter = len(newerActsAsc) >= half
		writeJSON(w, http.StatusOK, resp)
		return
	}

	oldestT := stitched[len(stitched)-1].CreatedAt
	newestT := stitched[0].CreatedAt
	// If the around-anchor is an activity outside the comment window (e.g.
	// activity newer than all returned comments), expand the activity window
	// to include it so the anchor lands in the response and the frontend can
	// scroll to + auto-expand the folded group containing it.
	if targetType == "activity" {
		if anchorTime.Time.Before(oldestT.Time) {
			oldestT = anchorTime
		}
		if anchorTime.Time.After(newestT.Time) {
			newestT = anchorTime
		}
	}
	rawActs, err := h.Queries.ListActivitiesInRange(ctx, db.ListActivitiesInRangeParams{
		IssueID: issue.ID,
		Column2: oldestT,
		Column3: newestT,
		Limit:   int32(activityLimit + 1),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list activities")
		return
	}
	activities, truncated := fetchActivitiesWithCap(rawActs, activityLimit)

	resp.Comments = h.commentsToEntries(r, stitched)
	resp.Activities = activitiesToEntries(activities)
	resp.ActivityTruncatedCount = truncated
	// Cursors anchor on the oldest/newest comments so V2 pagination continues
	// to walk by comment.
	if resp.HasMoreBefore {
		c := encodeTimelineCursor(stitched[len(stitched)-1].CreatedAt, stitched[len(stitched)-1].ID)
		resp.NextCursor = &c
	}
	if resp.HasMoreAfter {
		c := encodeTimelineCursor(stitched[0].CreatedAt, stitched[0].ID)
		resp.PrevCursor = &c
	}
	writeJSON(w, http.StatusOK, resp)
}

// AssigneeFrequencyEntry represents how often a user assigns to a specific target.
type AssigneeFrequencyEntry struct {
	AssigneeType string `json:"assignee_type"`
	AssigneeID   string `json:"assignee_id"`
	Frequency    int64  `json:"frequency"`
}

// GetAssigneeFrequency returns assignee usage frequency for the current user,
// combining data from assignee change activities and initial issue assignments.
func (h *Handler) GetAssigneeFrequency(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)

	// Aggregate frequency from both data sources.
	freq := map[string]int64{} // key: "type:id"

	// Source 1: assignee_changed activities by this user.
	activityCounts, err := h.Queries.CountAssigneeChangesByActor(r.Context(), db.CountAssigneeChangesByActorParams{
		WorkspaceID: parseUUID(workspaceID),
		ActorID:     parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get assignee frequency")
		return
	}
	for _, row := range activityCounts {
		aType, _ := row.AssigneeType.(string)
		aID, _ := row.AssigneeID.(string)
		if aType != "" && aID != "" {
			freq[aType+":"+aID] += row.Frequency
		}
	}

	// Source 2: issues created by this user with an assignee.
	issueCounts, err := h.Queries.CountCreatedIssueAssignees(r.Context(), db.CountCreatedIssueAssigneesParams{
		WorkspaceID: parseUUID(workspaceID),
		CreatorID:   parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get assignee frequency")
		return
	}
	for _, row := range issueCounts {
		if !row.AssigneeType.Valid || !row.AssigneeID.Valid {
			continue
		}
		key := row.AssigneeType.String + ":" + uuidToString(row.AssigneeID)
		freq[key] += row.Frequency
	}

	// Build sorted response.
	result := make([]AssigneeFrequencyEntry, 0, len(freq))
	for key, count := range freq {
		// Split "type:id" — type is always "member" or "agent" (no colons).
		var aType, aID string
		for i := 0; i < len(key); i++ {
			if key[i] == ':' {
				aType = key[:i]
				aID = key[i+1:]
				break
			}
		}
		result = append(result, AssigneeFrequencyEntry{
			AssigneeType: aType,
			AssigneeID:   aID,
			Frequency:    count,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Frequency > result[j].Frequency
	})

	writeJSON(w, http.StatusOK, result)
}
