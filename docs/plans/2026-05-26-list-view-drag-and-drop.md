# List View Drag-and-Drop Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add drag-and-drop reordering to the issues list view, matching board view behavior — including dragging items into collapsed status groups.

**Architecture:** Wrap `ListView` in a `DndContext` (same pattern as `BoardView`). Each expanded status group becomes a `SortableContext`; each collapsed group header becomes a `useDroppable` target. `ListRow` gains a `useSortable` hook with a visible drag handle (GripVertical icon) to avoid conflicting with the existing checkbox/link interactions. The `onMoveIssue` callback (already used by board view) is threaded through to `ListView` from all 4 consumer pages. Position calculation, settling logic, and collision detection are extracted from `board-view.tsx` into a shared module.

**Tech Stack:** @dnd-kit/core, @dnd-kit/sortable, @dnd-kit/utilities (already in packages/views)

---

## Blocker Assessment

**No blockers found.** All infrastructure exists:
- dnd-kit is already a dependency of `packages/views`
- `useUpdateIssue` mutation handles position/status updates with optimistic cache
- All 3 consumer pages (issues-page, my-issues-page, project-detail) already have `handleMoveIssue` for board view
- The 4th consumer (`actor-issues-panel`) needs a `handleMoveIssue` added — trivial, same pattern

**One design consideration:** The board view uses the entire card as a drag target. For list rows this would conflict with the checkbox (selection) and the AppLink (navigation). Solution: add a **GripVertical drag handle** on the left edge, visible on hover. This matches Linear's list view pattern.

---

## Task 1: Extract shared drag utilities from board-view

Board-view contains pure functions (`computePosition`, `findColumn`, `buildColumns`, `makeKanbanCollision`, group ID helpers) that list view needs verbatim. Extract them into a shared module to avoid duplication.

**Files:**
- Create: `packages/views/issues/utils/drag-utils.ts`
- Modify: `packages/views/issues/components/board-view.tsx`

**Step 1: Create the shared drag utilities module**

Extract these pure functions from `board-view.tsx` into `packages/views/issues/utils/drag-utils.ts`:
- `computePosition(ids, activeId, issueMap)` — float position from neighbors
- `findColumn(columns, id, columnIds)` — locate which group contains an ID
- `buildColumns(issues, groups, grouping)` — build `Record<string, string[]>` from issues
- `makeKanbanCollision(columnIds)` — custom collision detection
- `statusGroupId(status)` — `status:${status}` helper
- `getIssueGroupId(issue, grouping)` — resolve issue → group ID
- `issueMatchesGroup(issue, group)` — check if issue already in target group
- `getMoveUpdates(group, position)` — build the status/assignee/position update payload

Also export the shared types:
- `BoardMoveUpdates` (rename to `DragMoveUpdates` since it's now shared)
- Re-export `BoardColumnGroup` from board-column (already exported)

**Step 2: Update board-view.tsx to import from shared module**

Replace inline definitions with imports from `../utils/drag-utils`. Board-view should have zero logic changes — only import paths change.

**Step 3: Run typecheck to verify refactor**

Run: `pnpm typecheck`
Expected: PASS — no behavior change, only import reorganization.

**Step 4: Commit**

```
refactor(issues): extract shared drag utilities from board-view
```

---

## Task 2: Make ListRow draggable with a grip handle

Add `useSortable` to `ListRow` and render a `GripVertical` drag handle. The handle appears on hover (same reveal pattern as the existing checkbox).

**Files:**
- Modify: `packages/views/issues/components/list-row.tsx`

**Step 1: Add useSortable hook and drag handle to ListRow**

Changes to `ListRow`:
1. Accept new optional prop `draggable?: boolean` (default `false` for backwards compat).
2. When `draggable` is true:
   - Call `useSortable({ id: issue.id, animateLayoutChanges })` (reuse the same `animateLayoutChanges` from board-card.tsx — extract to drag-utils if needed, or just inline the same logic).
   - Apply `transform`/`transition` style to the row container.
   - Render `GripVertical` icon as the drag handle (left of priority icon). The handle is invisible by default, visible on row hover (`opacity-0 group-hover/row:opacity-100`).
   - Spread `listeners` onto the grip handle element (NOT the whole row — so checkbox and link still work).
   - Spread `attributes` onto the row container.
   - When `isDragging` is true, apply `opacity-30` (same as board card).
3. When `draggable` is false, render exactly as today (no hook call — can't conditionally call hooks, so use a wrapper pattern: `DraggableListRow` wraps `ListRow` with `useSortable`, or pass handle props down).

**Design decision on hook call:** Since hooks can't be conditional, use the **wrapper component pattern**:
- `ListRow` stays unchanged (pure presentational).
- Create `DraggableListRow` that calls `useSortable`, then renders `ListRow` with additional drag handle + transform style.
- This mirrors the `DraggableBoardCard` / `BoardCardContent` split in board-card.tsx.

Detailed implementation:

```tsx
// In list-row.tsx — add at the bottom

export const DraggableListRow = memo(function DraggableListRow({
  issue,
  childProgress,
  disableSorting,
}: {
  issue: Issue;
  childProgress?: ChildProgress;
  disableSorting?: boolean;
}) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({
    id: issue.id,
    animateLayoutChanges,
    disabled: disableSorting ? { droppable: true } : undefined,
  });

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
  };

  // Render ListRow contents inline but with drag handle prepended
  // and sortable ref/style applied to the container
  ...
});
```

The `DraggableListRow` renders the same visual structure as `ListRow` but:
- Wraps in a `div` with `ref={setNodeRef}` and `style`.
- Prepends a `GripVertical` icon with `{...listeners}` as the drag handle.
- The existing priority icon / checkbox area shifts right by the grip handle width.

**Step 2: Import new dependencies**

Add to list-row.tsx:
```tsx
import { useSortable, defaultAnimateLayoutChanges } from "@dnd-kit/sortable";
import type { AnimateLayoutChanges } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { GripVertical } from "lucide-react";
```

**Step 3: Run typecheck**

Run: `pnpm typecheck`
Expected: PASS

**Step 4: Commit**

```
feat(list): add DraggableListRow with grip handle for drag-and-drop
```

---

## Task 3: Add DndContext and SortableContext to ListView

Wire up the full drag-and-drop context in `list-view.tsx`, mirroring board-view's state management pattern (local columns state, drag/settle refs, frozen issue map).

**Files:**
- Modify: `packages/views/issues/components/list-view.tsx`

**Step 1: Add onMoveIssue prop to ListView**

```tsx
export function ListView({
  issues,
  visibleStatuses,
  childProgressMap = EMPTY_PROGRESS_MAP,
  myIssuesScope,
  myIssuesFilter,
  projectId,
  onMoveIssue,  // NEW — optional, drag disabled when absent
}: {
  // ... existing props ...
  onMoveIssue?: (issueId: string, updates: DragMoveUpdates, onSettled?: () => void) => void;
})
```

Making `onMoveIssue` optional means `actor-issues-panel` (the only consumer without it) just doesn't get drag — no breaking change.

**Step 2: Add DndContext wrapping the accordion**

When `onMoveIssue` is provided, wrap the accordion content in `DndContext`. The pattern mirrors board-view:

1. **Local state:** `columns` = `Record<string, string[]>` mapping `statusGroupId(status)` → issue IDs. Synced from server data via `useEffect` when not dragging/settling.
2. **Refs:** `isDraggingRef`, `isSettlingRef`, `recentlyMovedRef`, `columnsRef`, `issueMapRef` — all same as board-view.
3. **Sensors:** `PointerSensor` with `distance: 5` (same as board).
4. **Collision detection:** `makeKanbanCollision(groupIds)` from shared utils.
5. **Handlers:**
   - `handleDragStart`: set `activeIssue` for overlay, set `isDraggingRef`.
   - `handleDragOver`: cross-group moves — update local columns (only when `sortBy === "position"`).
   - `handleDragEnd`: compute final position, call `onMoveIssue`.
6. **DragOverlay:** render a simplified list row preview (just identifier + title, rotated 1deg, shadow, same visual language as board overlay).

**Step 3: Wrap each expanded status group's issues in SortableContext**

Inside `StatusAccordionItem`, wrap the issue list with:
```tsx
<SortableContext items={issueIds} strategy={verticalListSortingStrategy}>
  {issues.map((issue) => (
    <DraggableListRow key={issue.id} issue={issue} ... />
  ))}
</SortableContext>
```

Replace `ListRow` with `DraggableListRow` when `onMoveIssue` is provided. Pass `issueIds` from the local columns state (not from server data directly), so drag reordering is reflected instantly.

**Step 4: Handle collapsed groups as droppable targets**

For collapsed status groups (not in `expandedStatuses`), render a simplified header that:
- Uses `useDroppable({ id: statusGroupId(status) })` to accept drops.
- Shows visual feedback when `isOver` is true: `ring-2 ring-brand/25 bg-accent/15` (same style as board column).
- Does NOT expand the group on drop — just updates the issue's status.

Implementation: In the `visibleStatuses.map(...)`, when a status is collapsed, render a `CollapsedDropTarget` component instead of the full `StatusAccordionItem`:

```tsx
function CollapsedDropTarget({ status }: { status: IssueStatus }) {
  const { setNodeRef, isOver } = useDroppable({
    id: statusGroupId(status),
  });
  // Render same header chrome as current collapsed state,
  // but with ref={setNodeRef} and isOver highlight
}
```

Actually — the Accordion already renders collapsed headers. The trick is to put `useDroppable` on the Accordion.Header for collapsed items. Since the Accordion.Panel is closed, only the header is visible, and it becomes the drop zone.

Better approach: **Always** put `useDroppable({ id: statusGroupId(status) })` on each `StatusAccordionItem`'s container div. When collapsed, the entire item (just the header) is droppable. When expanded, the SortableContext inside handles individual row positioning. The collision detection (`makeKanbanCollision`) already prefers cards over containers, so expanded groups will sort to cards first and fall back to the group only when hovering the header.

**Step 5: Add sortBy awareness**

Read `sortBy` from `useViewStore`. When `sortBy !== "position"`:
- Disable row-level sorting (`disableSorting={true}` on `DraggableListRow`).
- Cross-group moves only update status, keep original position.
- Show the sort label overlay on the target group (same as board: `ring-2 ring-brand/25` + centered label badge).

**Step 6: DragOverlay rendering**

```tsx
<DragOverlay dropAnimation={null}>
  {activeIssue ? (
    <div className="w-full max-w-2xl rotate-1 cursor-grabbing opacity-90 shadow-lg shadow-black/10 rounded-md border border-border bg-card px-4 py-2">
      <span className="text-xs text-muted-foreground mr-2">{activeIssue.identifier}</span>
      <span className="text-sm">{activeIssue.title}</span>
    </div>
  ) : null}
</DragOverlay>
```

**Step 7: Run typecheck**

Run: `pnpm typecheck`
Expected: PASS

**Step 8: Commit**

```
feat(list): wire DndContext + SortableContext for list view drag-and-drop
```

---

## Task 4: Thread onMoveIssue to all ListView consumers

Pass `onMoveIssue` from each page that renders `ListView`.

**Files:**
- Modify: `packages/views/issues/components/issues-page.tsx:220`
- Modify: `packages/views/my-issues/components/my-issues-page.tsx:259`
- Modify: `packages/views/projects/components/project-detail.tsx:228`
- Modify: `packages/views/common/actor-issues-panel.tsx:210` (no move handler — leave as-is, drag auto-disabled)

**Step 1: issues-page.tsx**

Line 220, add `onMoveIssue={handleMoveIssue}`:
```tsx
<ListView
  issues={issues}
  visibleStatuses={visibleStatuses}
  childProgressMap={childProgressMap}
  onMoveIssue={handleMoveIssue}
/>
```

`handleMoveIssue` already exists at line 150.

**Step 2: my-issues-page.tsx**

Line 259, add `onMoveIssue={handleMoveIssue}`:
```tsx
<ListView
  issues={issues}
  visibleStatuses={visibleStatuses}
  childProgressMap={childProgressMap}
  myIssuesScope={scope}
  myIssuesFilter={filter}
  onMoveIssue={handleMoveIssue}
/>
```

`handleMoveIssue` already exists at line 162.

**Step 3: project-detail.tsx**

Line 228, add `onMoveIssue={handleMoveIssue}`:
```tsx
<ListView
  issues={issues}
  visibleStatuses={visibleStatuses}
  childProgressMap={childProgressMap}
  myIssuesScope={scope}
  myIssuesFilter={filter}
  projectId={projectId}
  onMoveIssue={handleMoveIssue}
/>
```

`handleMoveIssue` already exists at line 166.

**Step 4: actor-issues-panel.tsx — no change needed**

`onMoveIssue` is optional. When omitted, `ListView` renders plain `ListRow` without drag. No code change.

**Step 5: Run typecheck**

Run: `pnpm typecheck`
Expected: PASS

**Step 6: Commit**

```
feat(list): thread onMoveIssue to all ListView consumers
```

---

## Task 5: Visual polish and edge cases

**Files:**
- Modify: `packages/views/issues/components/list-view.tsx`
- Modify: `packages/views/issues/components/list-row.tsx`

**Step 1: Drag handle visibility**

The `GripVertical` handle should:
- Be invisible by default: `opacity-0`
- Appear on row hover: `group-hover/row:opacity-100`
- Have `cursor-grab` (and `cursor-grabbing` while dragging via DragOverlay)
- Be `text-muted-foreground` colored, `size-3.5`
- Sit in the leftmost position, before the priority icon / checkbox area

**Step 2: Collapsed group drop feedback**

When `isOver` on a collapsed group:
- Add `ring-2 ring-brand/25 bg-accent/15` to the header (same as board column)
- Animate the status icon with a subtle pulse or scale

**Step 3: Prevent accordion toggle during drag**

When `isDraggingRef.current` is true, block `onValueChange` on the Accordion.Root so hovering over a collapsed header during drag doesn't accidentally expand/collapse it.

**Step 4: Ensure row context menu still works**

`IssueActionsContextMenu` wraps `ListRow`. Verify that `useSortable` + context menu don't conflict. Since drag is isolated to the grip handle via `listeners`, right-click on the row body should still open the context menu. Test this manually.

**Step 5: Keyboard accessibility**

`useSortable` gives keyboard support for free (Enter to pick up, arrow keys to move, Escape to cancel). The `attributes` spread on the row already includes `role`, `tabIndex`, and `aria-*` from dnd-kit. No extra work needed, but verify the experience.

**Step 6: Run full check**

Run: `make check`
Expected: PASS

**Step 7: Commit**

```
feat(list): polish drag-and-drop visuals and edge cases
```

---

## Task 6: Manual testing checklist

Start the dev environment and test all scenarios in the browser.

Run: `make dev` (or `pnpm dev:web` if backend is already running)

**Test matrix:**

| Scenario | Expected |
|---|---|
| Drag row within same status group (manual sort) | Row reorders, position updates |
| Drag row to different expanded status group (manual sort) | Row moves to target group at drop position, status updates |
| Drag row to collapsed status group | Row disappears into collapsed group, status updates, group count increments |
| Drag row when sort ≠ manual (e.g. priority sort) | Cross-group moves update status only; within-group sorting disabled; sort label overlay shows on hover target |
| Click row (not grip) | Navigates to issue detail (no drag initiated) |
| Click checkbox | Toggles selection (no drag initiated) |
| Right-click row | Context menu opens (no drag initiated) |
| Hover row → grip appears | Grip handle fades in on hover |
| Expand/collapse status group (click chevron) | Works normally when not dragging |
| Drag in my-issues page | Same behavior as workspace issues page |
| Drag in project detail page | Same behavior, project_id preserved |
| Actor issues panel (agent/member detail) | No drag handles shown, no DndContext wrapping |
| Empty status group → drag item into it | Item appears, empty message disappears |
| Infinite scroll sentinel | Load-more still triggers when scrolling within a group |

---

## Summary

| Task | Scope | Risk |
|---|---|---|
| 1. Extract shared drag utils | Refactor only, zero behavior change | Low |
| 2. DraggableListRow component | New component, no existing code changes | Low |
| 3. DndContext in ListView | Core feature — most complex task | Medium |
| 4. Thread onMoveIssue | 3 one-line prop additions | Low |
| 5. Visual polish | CSS + edge case handling | Low |
| 6. Manual testing | Verification only | N/A |

Estimated total: ~4-6 hours of implementation.
