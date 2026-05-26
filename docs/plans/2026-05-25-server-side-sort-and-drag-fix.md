# Server-Side Sort + Drag-and-Drop Fix Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add server-side sorting to the issues API, fix the bug where dragging in non-manual sort mode silently corrupts position data, and add a column overlay to communicate sort state during drag.

**Architecture:** Backend adds `sort` + `direction` query params to `ListIssues` (converted from sqlc to hand-written SQL) and `ListGroupedIssues`. Frontend passes sort params from the view store through queries to the API, removes client-side sorting, and guards drag-and-drop to only write position when `sortBy === "position"`. Non-manual sort shows a translucent column overlay during drag.

**Tech Stack:** Go (Chi router, pgx), TypeScript, React, TanStack Query, dnd-kit v10, Zustand

---

## Task 1: Backend — Add sort support to ListIssues

**Files:**
- Modify: `server/internal/handler/issue.go:699-892`
- Modify: `server/pkg/db/queries/issue.sql:1-62` (remove sqlc ListIssues query)

The current `ListIssues` handler uses sqlc-generated code with hardcoded `ORDER BY i.position ASC, i.created_at DESC`. sqlc does not support dynamic ORDER BY, so we convert this to hand-written SQL (same pattern as `ListGroupedIssues`).

**Step 1: Add sort parameter parsing to ListIssues handler**

In `issue.go`, after the `scheduledFilter` parsing (line 834) and before the query call (line 836), add sort parameter parsing:

```go
// Parse sort parameters. Default to position ASC (manual order).
sortField := "i.position"
sortDir := "ASC"
if s := r.URL.Query().Get("sort"); s != "" {
    switch s {
    case "priority":
        sortField = "i.priority"
    case "title":
        sortField = "i.title"
    case "created_at":
        sortField = "i.created_at"
        sortDir = "DESC"
    case "start_date":
        sortField = "i.start_date"
    case "due_date":
        sortField = "i.due_date"
    case "position":
        // default, already set
    default:
        writeError(w, http.StatusBadRequest, "unsupported sort field")
        return
    }
}
if d := r.URL.Query().Get("direction"); d != "" {
    switch d {
    case "asc":
        sortDir = "ASC"
    case "desc":
        sortDir = "DESC"
    default:
        writeError(w, http.StatusBadRequest, "unsupported sort direction")
        return
    }
}

// NULLS LAST for nullable date fields so null values sort to the end.
nullsClause := ""
if sortField == "i.start_date" || sortField == "i.due_date" {
    nullsClause = " NULLS LAST"
}
```

**Step 2: Replace sqlc ListIssues call with hand-written SQL**

Replace the `h.Queries.ListIssues(ctx, ...)` call (lines 836-849) with dynamic SQL. Build the WHERE clause the same way `ListGroupedIssues` does, reusing the existing parsed filter variables:

```go
where := []string{"i.workspace_id = $1"}
args := []any{wsUUID}
addArg := func(v any) string {
    args = append(args, v)
    return "$" + strconv.Itoa(len(args))
}

if statusFilter.Valid {
    where = append(where, fmt.Sprintf("i.status = %s", addArg(statusFilter.String)))
}
if priorityFilter.Valid {
    where = append(where, fmt.Sprintf("i.priority = %s", addArg(priorityFilter.String)))
}
if assigneeFilter.Valid {
    where = append(where, fmt.Sprintf("i.assignee_id = %s::uuid", addArg(assigneeFilter)))
}
if len(assigneeIdsFilter) > 0 {
    where = append(where, fmt.Sprintf("i.assignee_id = ANY(%s::uuid[])", addArg(assigneeIdsFilter)))
}
if creatorFilter.Valid {
    where = append(where, fmt.Sprintf("i.creator_id = %s::uuid", addArg(creatorFilter)))
}
if projectFilter.Valid {
    where = append(where, fmt.Sprintf("i.project_id = %s::uuid", addArg(projectFilter)))
}
if scheduledFilter.Valid {
    where = append(where, "(i.start_date IS NOT NULL OR i.due_date IS NOT NULL)")
}
if metadataFilter.Valid {
    where = append(where, fmt.Sprintf("i.metadata @> %s::jsonb", addArg(metadataFilter.Bytes)))
}
if involvesUserFilter.Valid {
    // Reuse the existing involves_user_id SQL subquery logic
    where = append(where, fmt.Sprintf(`(
        (i.assignee_type = 'agent' AND i.assignee_id IN (
            SELECT a.id FROM agent a WHERE a.workspace_id = $1 AND a.owner_id = %[1]s::uuid
        ))
        OR (i.assignee_type = 'squad' AND i.assignee_id IN (
            SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id
             WHERE s.workspace_id = $1 AND sm.member_type = 'member' AND sm.member_id = %[1]s::uuid
            UNION
            SELECT s.id FROM squad s JOIN agent a ON a.id = s.leader_id
             WHERE s.workspace_id = $1 AND a.workspace_id = $1 AND a.owner_id = %[1]s::uuid
            UNION
            SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id JOIN agent a ON a.id = sm.member_id
             WHERE s.workspace_id = $1 AND sm.member_type = 'agent' AND a.workspace_id = $1 AND a.owner_id = %[1]s::uuid
        ))
    )`, addArg(involvesUserFilter)))
}

orderBy := fmt.Sprintf("ORDER BY %s %s%s, i.created_at DESC", sortField, sortDir, nullsClause)
query := fmt.Sprintf(`
SELECT i.id, i.workspace_id, i.title, i.description, i.status, i.priority,
       i.assignee_type, i.assignee_id, i.creator_type, i.creator_id,
       i.parent_issue_id, i.position, i.start_date, i.due_date, i.created_at, i.updated_at, i.number, i.project_id, i.metadata
FROM issue i
WHERE %s
%s
LIMIT %s OFFSET %s`,
    strings.Join(where, " AND "), orderBy, addArg(int64(limit)), addArg(int64(offset)))
```

Then scan the rows manually (same pattern as `ListGroupedIssues`). Also build a corresponding COUNT query for `total`.

**Step 3: Remove the sqlc ListIssues query from issue.sql**

Comment out or remove lines 1-62 in `server/pkg/db/queries/issue.sql` (the `-- name: ListIssues :many` query). Run `make sqlc` to regenerate. The generated `ListIssues` function and `ListIssuesParams` struct will be removed — replace all usages with the new hand-written query.

**Step 4: Run backend tests**

Run: `cd server && go test ./internal/handler/ -v`
Expected: PASS

**Step 5: Commit**

```
feat(api): add sort + direction params to GET /api/issues
```

---

## Task 2: Backend — Add sort support to ListGroupedIssues

**Files:**
- Modify: `server/internal/handler/issue.go:957-1230`

**Step 1: Add sort parameter parsing**

After the existing filter parsing (around line 1195) and before the query construction (line 1197), add the same sort parsing logic from Task 1.

**Step 2: Update the ROW_NUMBER window function ORDER BY**

Change line 1209 from:
```sql
ORDER BY i.position ASC, i.created_at DESC
```
to use the dynamic sort:
```go
fmt.Sprintf("ORDER BY %s %s%s, i.created_at DESC", sortField, sortDir, nullsClause)
```

**Step 3: Run backend tests**

Run: `cd server && go test ./internal/handler/ -v`
Expected: PASS

**Step 4: Commit**

```
feat(api): add sort + direction params to GET /api/issues/grouped
```

---

## Task 3: Frontend — Add sort params to API types and client

**Files:**
- Modify: `packages/core/types/api.ts:38-67` (ListIssuesParams)
- Modify: `packages/core/types/api.ts:74-98` (ListGroupedIssuesParams)
- Modify: `packages/core/api/client.ts:432-454` (listIssues)
- Modify: `packages/core/api/client.ts:456-487` (listGroupedIssues)

**Step 1: Add sort fields to ListIssuesParams**

In `types/api.ts`, add to `ListIssuesParams` (after line 66):
```typescript
sort_by?: "position" | "priority" | "title" | "created_at" | "start_date" | "due_date";
sort_direction?: "asc" | "desc";
```

**Step 2: Add sort fields to ListGroupedIssuesParams**

In `types/api.ts`, add to `ListGroupedIssuesParams` (after line 97):
```typescript
sort_by?: "position" | "priority" | "title" | "created_at" | "start_date" | "due_date";
sort_direction?: "asc" | "desc";
```

**Step 3: Pass sort params in listIssues client method**

In `client.ts`, in the `listIssues` method (around line 448), add:
```typescript
if (params?.sort_by) search.set("sort", params.sort_by);
if (params?.sort_direction) search.set("direction", params.sort_direction);
```

**Step 4: Pass sort params in listGroupedIssues client method**

In `client.ts`, in the `listGroupedIssues` method, add the same two lines.

**Step 5: Run typecheck**

Run: `pnpm typecheck`
Expected: PASS

**Step 6: Commit**

```
feat(core): add sort_by and sort_direction to issue API params
```

---

## Task 4: Frontend — Wire sort into query keys and fetchers

**Files:**
- Modify: `packages/core/issues/queries.ts`

**Step 1: Update query key factories to include sort**

Change `issueKeys.list` to accept sort params:
```typescript
list: (wsId: string, sort?: { by: string; dir: string }) =>
  [...issueKeys.all(wsId), "list", sort ?? { by: "position", dir: "asc" }] as const,
```

Do the same for `issueKeys.myList` — add the sort object into the key tuple.

Also update `issueKeys.assigneeGroups` and `issueKeys.myAssigneeGroups` to include sort in their filter identity.

**Step 2: Update fetchFirstPages to accept and pass sort params**

Change the `fetchFirstPages` signature:
```typescript
async function fetchFirstPages(
  filter: MyIssuesFilter = {},
  sort?: { by: string; dir: string },
): Promise<ListIssuesCache> {
  const responses = await Promise.all(
    PAGINATED_STATUSES.map((status) =>
      api.listIssues({
        status,
        limit: ISSUE_PAGE_SIZE,
        offset: 0,
        sort_by: sort?.by as ListIssuesParams["sort_by"],
        sort_direction: sort?.dir as ListIssuesParams["sort_direction"],
        ...filter,
      }),
    ),
  );
  // ... rest unchanged
}
```

**Step 3: Update issueListOptions and myIssueListOptions**

Pass sort from the view store through to `fetchFirstPages`. The sort comes from the component via a parameter:
```typescript
export function issueListOptions(wsId: string, sort?: { by: string; dir: string }) {
  return queryOptions({
    queryKey: issueKeys.list(wsId, sort),
    queryFn: () => fetchFirstPages({}, sort),
    select: flattenIssueBuckets,
  });
}
```

Same for `myIssueListOptions` — add `sort` parameter and thread it through.

**Step 4: Update the pages that call these options**

In `issues-page.tsx`, `my-issues-page.tsx`, and `project-detail.tsx` — read `sortBy` and `sortDirection` from the view store and pass `{ by: sortBy, dir: sortDirection }` to the query options.

**Step 5: Remove client-side sortIssues calls from board-view.tsx and list-view.tsx**

In `board-view.tsx` `buildColumns()` (line 158), change `sortIssues(filtered, sortBy, sortDirection)` to just `filtered` — the data is already sorted by the server.

In `list-view.tsx` (line 52), change `sortIssues(filtered, sortBy, sortDirection)` to just `filtered`.

**Step 6: Run typecheck and tests**

Run: `pnpm typecheck && pnpm test`
Expected: PASS

**Step 7: Commit**

```
feat(core): wire server-side sort into query keys and fetchers
```

---

## Task 5: Frontend — Fix drag-and-drop for non-manual sort

**Files:**
- Modify: `packages/views/issues/components/board-view.tsx:370-460`

**Step 1: Guard handleDragOver against same-column moves in non-manual sort**

In `handleDragOver` (line 370), the existing check at line 381 is:
```typescript
if (!activeCol || !overCol || activeCol === overCol) return prev;
```

This already skips same-column moves in `handleDragOver`. No change needed here — cross-column moves will still work visually.

**Step 2: Guard handleDragEnd against position writes in non-manual sort**

In `handleDragEnd` (line 395), after computing `activeCol` and `overCol` (line 413-414), add:

```typescript
// Non-manual sort: same-column reorder is meaningless — reset and bail.
if (activeCol === overCol && sortBy !== "position") {
  resetColumns();
  return;
}
```

Then, after computing `finalGroup` (line 438), change the `onMoveIssue` call (line 457). For non-manual sort, preserve the original position:

```typescript
if (sortBy !== "position") {
  // Cross-column move: only update group (status/assignee), keep original position.
  const currentIssue = map.get(activeId);
  if (!currentIssue || issueMatchesGroup(currentIssue, finalGroup)) {
    resetColumns();
    return;
  }
  const updates = getMoveUpdates(finalGroup, currentIssue.position);
  onMoveIssue(activeId, updates);
  return;
}

// Manual sort: compute new position from visual neighbors.
const newPosition = computePosition(finalIds, activeId, map);
// ... existing logic
```

**Step 3: Run typecheck**

Run: `pnpm typecheck`
Expected: PASS

**Step 4: Commit**

```
fix(board): prevent position corruption when dragging in non-manual sort
```

---

## Task 6: Frontend — Column overlay during drag in non-manual sort

**Files:**
- Modify: `packages/views/issues/components/board-column.tsx:35-136`
- Modify: `packages/views/issues/components/board-view.tsx:462-530` (pass props down)
- Modify: `packages/views/locales/en/issues.json`
- Modify: `packages/views/locales/zh-Hans/issues.json`

**Step 1: Add i18n keys**

In `en/issues.json`, in the `"board"` section (line 123), add:
```json
"ordered_by": "Board ordered by {{field}}"
```

In `zh-Hans/issues.json`, in the corresponding `"board"` section, add:
```json
"ordered_by": "按{{field}}排序"
```

**Step 2: Add overlay props to BoardColumn**

In `board-column.tsx`, add two new props to the `BoardColumn` component:

```typescript
export function BoardColumn({
  group,
  issueIds,
  issueMap,
  childProgressMap,
  totalCount,
  footer,
  projectId,
  isDragging,       // NEW
  sortLabel,        // NEW: null when manual sort (no overlay)
}: {
  // ... existing props ...
  isDragging?: boolean;
  sortLabel?: string | null;
}) {
```

**Step 3: Render the overlay inside the droppable area**

In the droppable `<div>` (line 116-133), add a conditional overlay as the first child:

```tsx
<div
  ref={setNodeRef}
  className={`relative min-h-[200px] flex-1 space-y-2 overflow-y-auto rounded-lg p-1 transition-colors ${
    isOver ? "bg-accent/60" : ""
  }`}
>
  {isDragging && sortLabel && (
    <div className="absolute inset-0 z-10 flex items-center justify-center rounded-lg bg-background/60 backdrop-blur-[1px]">
      <p className="text-sm font-medium text-foreground">
        {sortLabel}
      </p>
    </div>
  )}
  <SortableContext items={issueIds} strategy={verticalListSortingStrategy}>
    {/* ... existing cards ... */}
  </SortableContext>
  {/* ... existing empty state and footer ... */}
</div>
```

**Step 4: Pass isDragging and sortLabel from BoardView**

In `board-view.tsx`, compute `sortLabel`:

```typescript
const { t } = useT("issues");
const sortLabel = sortBy !== "position"
  ? t(($) => $.board.ordered_by, { field: t(($) => $.display[`sort_${sortBy === "created_at" ? "created" : sortBy}`]) })
  : null;
```

Then pass to each `BoardColumn` / `PaginatedBoardColumn` / `PaginatedAssigneeBoardColumn`:

```tsx
<BoardColumn
  // ... existing props ...
  isDragging={isDraggingRef.current && activeIssue !== null}
  sortLabel={sortLabel}
/>
```

Note: `isDraggingRef.current` won't trigger re-renders. Instead, derive from `activeIssue !== null` (state-driven):

```tsx
isDragging={activeIssue !== null}
sortLabel={sortLabel}
```

**Step 5: Run typecheck**

Run: `pnpm typecheck`
Expected: PASS

**Step 6: Commit**

```
feat(board): show sort overlay on columns during drag in non-manual sort
```

---

## Task 7: Verify end-to-end

**Step 1: Start dev environment**

Run: `make dev`

**Step 2: Manual verification checklist**

1. Open Issues board view
2. Default (Manual sort): drag within column → position updates ✓
3. Default (Manual sort): drag across columns → status + position update ✓
4. Switch to Title sort: verify cards re-sort (server-side)
5. Title sort: drag within column → card snaps back, no position written ✓
6. Title sort: drag across columns → status changes, position preserved ✓
7. Title sort: during drag, columns show overlay "Board ordered by Title" ✓
8. Switch back to Manual: verify original position order restored ✓
9. Repeat checks on My Issues page
10. Repeat checks on Project detail board

**Step 3: Run full check suite**

Run: `make check`
Expected: All pass

**Step 4: Final commit (if any fixups needed)**

---

## Summary

| Task | Scope | Files |
|------|-------|-------|
| 1 | Backend: ListIssues sort | `issue.go`, `issue.sql` |
| 2 | Backend: ListGroupedIssues sort | `issue.go` |
| 3 | Frontend: API types + client | `types/api.ts`, `client.ts` |
| 4 | Frontend: Query keys + fetchers | `queries.ts`, page files |
| 5 | Frontend: Drag-and-drop guard | `board-view.tsx` |
| 6 | Frontend: Column overlay | `board-column.tsx`, `board-view.tsx`, i18n |
| 7 | E2E verification | manual testing |
