"use client";

import { cloneElement } from "react";
import { ArrowDown, ArrowUp } from "lucide-react";

import { cn } from "../../lib/utils";

// Linear-style list grid. The container declares every column track once via
// a literal `grid-cols-[...]` class (plus responsive variants); the header and
// each row span the full template with `grid-cols-subgrid`, so column widths
// have a single source of truth and never drift between header, rows, and
// skeletons.
//
// Conventions the container class must follow:
// - First and last tracks are edge-padding columns (e.g. 1.25rem) so row
//   hover backgrounds stay full-bleed while content aligns with page chrome.
//   ListGridHeader/ListGridRow render the matching placeholder cells.
// - Responsive column hiding is CONTAINER-query driven: wrap the ListGrid in
//   a `@container` element and use `@<bp>:` variants (not viewport `sm:`/
//   `lg:`), so sidebars and split panes narrow the list correctly. Change the
//   template per container breakpoint AND mark the corresponding cells
//   `hidden @<bp>:flex`; display:none cells drop out of subgrid
//   auto-placement so the remaining cells fill the right tracks.
// - Columns drop by priority as the container narrows (most expendable
//   first); never fall back to horizontal scrolling.
// - Keep the class a literal string in the page source so Tailwind sees it.

export type ListGridSortDirection = "asc" | "desc";

function ListGrid({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      role="table"
      className={cn("grid w-full min-w-0 content-start gap-x-3", className)}
      {...props}
    />
  );
}

function ListGridHeader({
  className,
  children,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      role="row"
      className={cn(
        "group/header sticky top-0 z-10 col-span-full grid h-9 grid-cols-subgrid items-center bg-background after:pointer-events-none after:absolute after:inset-x-0 after:top-full after:h-3 after:bg-gradient-to-b after:from-background after:to-transparent",
        className,
      )}
      {...props}
    >
      <span aria-hidden="true" />
      {children}
      <span aria-hidden="true" />
    </div>
  );
}

interface ListGridHeaderCellProps
  extends React.HTMLAttributes<HTMLDivElement> {
  /** Current sort state of this column; `false` when not the active sort. */
  sorted?: ListGridSortDirection | false;
  /** When provided the header renders as a sort button. */
  onSort?: () => void;
  align?: "left" | "right";
}

function ListGridHeaderCell({
  sorted = false,
  onSort,
  align = "left",
  className,
  children,
  ...props
}: ListGridHeaderCellProps) {
  if (!onSort) {
    return (
      <div
        className={cn(
          "flex min-w-0 items-center px-2 text-xs text-muted-foreground",
          align === "right" && "justify-end",
          className,
        )}
        {...props}
      >
        {children}
      </div>
    );
  }
  const Arrow = sorted === "asc" ? ArrowUp : ArrowDown;
  return (
    <div
      className={cn(
        "flex min-w-0 items-center px-2",
        align === "right" && "justify-end",
        className,
      )}
      {...props}
    >
      <button
        type="button"
        onClick={onSort}
        className={cn(
          "group/sort flex h-6 items-center gap-0.5 rounded-md text-xs transition-colors",
          // Active sort column: emphasis via weight + full foreground color
          // only — no background, so the header row stays quiet.
          sorted
            ? "font-medium text-foreground"
            : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
          align === "right" ? "-mr-1.5 flex-row-reverse pl-1 pr-1.5" : "-ml-1.5 pl-1.5 pr-1",
        )}
      >
        {children}
        <Arrow
          className={cn(
            "size-3 shrink-0",
            sorted ? "opacity-100" : "opacity-0 group-hover/sort:opacity-50",
          )}
        />
      </button>
    </div>
  );
}

// Scrollable rows area. Lets the scrollbar start BELOW the header (Linear's
// list-wrapper structure) instead of running alongside it: give the ListGrid
// container `h-full grid-rows-[auto_minmax(0,1fr)]` and put all rows inside
// this body. Nested subgrid keeps the column tracks aligned with the header.
// Known limitation: a non-overlay scrollbar consumes body width, shifting
// row tracks ~10px relative to the header (invisible with macOS overlay
// scrollbars).
function ListGridBody({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        "col-span-full grid min-h-0 grid-cols-subgrid content-start overflow-x-hidden overflow-y-auto",
        className,
      )}
      {...props}
    />
  );
}

interface ListGridRowProps extends React.HTMLAttributes<HTMLElement> {
  /**
   * Base UI-style render prop: pass an element (e.g. `<AppLink href={...} />`)
   * to use it as the row root; the row classes and cells are merged onto it.
   */
  render?: React.ReactElement<{
    className?: string;
    children?: React.ReactNode;
  }>;
}

function ListGridRow({ render, className, children, ...props }: ListGridRowProps) {
  const rowClassName = cn(
    "group/row col-span-full grid h-12 grid-cols-subgrid items-center transition-colors hover:bg-accent/40",
    className,
  );
  const content = (
    <>
      <span aria-hidden="true" />
      {children}
      <span aria-hidden="true" />
    </>
  );
  if (render) {
    return cloneElement(
      render,
      { ...props, className: cn(rowClassName, render.props.className) },
      content,
    );
  }
  return (
    <div className={rowClassName} {...props}>
      {content}
    </div>
  );
}

// Cells and header cells carry the same default horizontal padding so the
// header can never drift out of alignment with row content. Structural
// columns (checkbox, kebab) opt out with `px-0`.
function ListGridCell({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn("flex min-w-0 items-center px-2", className)}
      {...props}
    />
  );
}

export {
  ListGrid,
  ListGridBody,
  ListGridHeader,
  ListGridHeaderCell,
  ListGridRow,
  ListGridCell,
};
