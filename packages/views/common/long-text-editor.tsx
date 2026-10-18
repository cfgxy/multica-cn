"use client";

import { useRef, useState } from "react";
import { Loader2, Maximize2 } from "lucide-react";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Markdown } from "@multica/ui/markdown";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import { SegmentedToggle } from "./segmented-toggle";

/**
 * Unified long-text field editor (RUYI-735): an inline textarea whose height
 * is capped so a long value scrolls inside the field instead of stretching
 * the settings page, plus an "expand" affordance that opens a large dialog
 * editor for comfortable writing.
 *
 * The component is fully controlled and holds no draft of its own — every
 * keystroke in either surface flows through `onChange` into the caller's
 * state, so expanding, previewing, or closing can never silently lose text.
 *
 * Commit semantics stay with the caller:
 *   - `onBlur` fires when the inline field blurs or the dialog commits
 *     (Done / Save / Escape / close button). Auto-save callers pass their
 *     flush here, exactly like they did with the bare `<Textarea>`.
 *   - `onSave`, when provided, replaces the dialog footer's primary button
 *     with a Save action: the dialog closes only after it resolves, and
 *     stays open (caller surfaces the error) when it rejects. Callers with
 *     an explicit save button below the field (e.g. agent instructions)
 *     wire their real save here so saving is always reachable while the
 *     large editor is open.
 *   - Cancel is the only discard: it restores the value captured when the
 *     dialog opened. Overlay dismissal and Escape keep the edits.
 *
 * Preview mode renders the draft through the shared Markdown renderer
 * (headings, lists, code blocks, quotes, links, emphasis). Toggling only
 * swaps the view; the underlying draft is untouched.
 */
export interface LongTextEditorProps {
  value: string;
  onChange: (value: string) => void;
  /** Fires on inline blur or dialog commit — auto-save callers pass their flush. */
  onBlur?: () => void;
  /** Dialog footer save action. When absent the footer primary is "Done". */
  onSave?: () => void | Promise<void>;
  ariaLabel: string;
  /** Dialog title — also the accessible name of the expanded surface. */
  dialogTitle: string;
  placeholder?: string;
  disabled?: boolean;
  /** Offer the expand-to-dialog affordance. Default true. */
  expandable?: boolean;
  /** Offer the Markdown preview toggle inside the dialog. Default true. */
  preview?: boolean;
  /** Inline min height in px. Default 96. */
  minHeight?: number;
  /** Inline max height in px — content scrolls internally past this. Default 360. */
  maxHeight?: number;
  /** Forwarded to the inline textarea so an external `<label htmlFor>` keeps
   *  working; the dialog textarea never carries it. */
  id?: string;
  /** Caller-owned caption (char counters, format hints) shown under the
   *  inline field and inside the dialog footer area. */
  hint?: React.ReactNode;
}

export function LongTextEditor({
  value,
  onChange,
  onBlur,
  onSave,
  ariaLabel,
  dialogTitle,
  placeholder,
  disabled = false,
  expandable = true,
  preview = true,
  minHeight = 96,
  maxHeight = 360,
  id,
  hint,
}: LongTextEditorProps) {
  const { t } = useT("common");
  const [expanded, setExpanded] = useState(false);
  const [mode, setMode] = useState<"edit" | "preview">("edit");
  const [saving, setSaving] = useState(false);
  const snapshotRef = useRef(value);
  const inlineRef = useRef<HTMLTextAreaElement | null>(null);
  const expandButtonRef = useRef<HTMLButtonElement | null>(null);
  const expandedRef = useRef(false);
  const selectionRef = useRef<{ start: number; end: number } | null>(null);

  const openExpanded = () => {
    expandedRef.current = true;
    snapshotRef.current = value;
    const el = inlineRef.current;
    selectionRef.current = el
      ? { start: el.selectionStart, end: el.selectionEnd }
      : null;
    setMode("edit");
    setExpanded(true);
  };

  const updateSelection = (el: HTMLTextAreaElement) => {
    selectionRef.current = { start: el.selectionStart, end: el.selectionEnd };
  };

  const closeExpanded = () => {
    expandedRef.current = false;
    setExpanded(false);
    // Reset the view for the next open; a dialog reopened in preview mode
    // would hide freshly typed text behind a stale render.
    setMode("edit");
    // Hand focus (and the caret) back to the inline field so the collapse
    // doesn't dump the user's place in the page.
    requestAnimationFrame(() => {
      const el = inlineRef.current;
      if (!el) return;
      el.focus();
      const sel = selectionRef.current;
      if (sel) {
        const start = Math.min(sel.start, value.length);
        const end = Math.min(sel.end, value.length);
        el.setSelectionRange(start, end);
      }
    });
  };

  const handleCommit = () => {
    closeExpanded();
    onBlur?.();
  };

  // Cancel is the only discard path: restore the pre-open snapshot. For
  // auto-save callers this just rewrites the saved value into the draft, so
  // any later flush is a no-op rather than a surprise save.
  const handleCancel = () => {
    onChange(snapshotRef.current);
    closeExpanded();
  };

  const handleSave = async () => {
    if (!onSave) return;
    setSaving(true);
    try {
      await onSave();
      closeExpanded();
    } catch {
      // Keep the dialog open; the caller owns error surfacing (toast).
    } finally {
      setSaving(false);
    }
  };

  const hintNode = hint ? (
    <div className="text-caption text-muted-foreground">{hint}</div>
  ) : null;

  return (
    <div className="space-y-1">
      <div className="relative">
        <Textarea
          ref={inlineRef}
          id={id}
          aria-label={ariaLabel}
          value={value}
          onChange={(event) => onChange(event.target.value)}
          onBlur={(event) => {
            // Moving focus to the expand control is part of opening the editor,
            // and moving focus into the dialog is not an inline commit either.
            if (expandedRef.current) return;
            if (event.relatedTarget === expandButtonRef.current) return;
            onBlur?.();
          }}
          placeholder={placeholder}
          disabled={disabled}
          className="resize-none overflow-y-auto pr-9"
          style={{ minHeight, maxHeight }}
        />
        {expandable && !disabled && (
          <Button
            ref={expandButtonRef}
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={t(($) => $.long_text_editor.expand)}
            title={t(($) => $.long_text_editor.expand)}
            className="absolute top-1.5 right-1.5 text-muted-foreground hover:text-foreground"
            onPointerDown={(event) => event.preventDefault()}
            onBlur={() => {
              if (!expanded) onBlur?.();
            }}
            onClick={openExpanded}
          >
            <Maximize2 className="h-3.5 w-3.5" />
          </Button>
        )}
      </div>
      {hintNode}

      <Dialog
        open={expanded}
        onOpenChange={(open) => {
          // Any dismissal that isn't the footer Cancel lands here — overlay
          // click, Escape, header close button. All of them keep the draft.
          if (!open) handleCommit();
        }}
      >
        <DialogContent className="flex h-[min(85vh,760px)] w-[92vw] max-w-[calc(100%-2rem)] flex-col gap-3 sm:max-w-4xl">
          <DialogHeader className="flex-row items-center justify-between gap-3 space-y-0">
            <DialogTitle className="min-w-0 truncate">{dialogTitle}</DialogTitle>
            {preview && (
              <SegmentedToggle
                value={mode}
                onChange={setMode}
                options={[
                  ["edit", t(($) => $.long_text_editor.edit)] as const,
                  ["preview", t(($) => $.long_text_editor.preview)] as const,
                ]}
              />
            )}
          </DialogHeader>

          <div className="flex min-h-0 flex-1">
            {mode === "edit" ? (
              // A plain textarea on purpose: the shared <Textarea> ships
              // `field-sizing-content`, which is exactly the grow-with-content
              // behaviour this dialog must NOT have — the field fills the
              // dialog and scrolls internally instead.
              <textarea
                aria-label={ariaLabel}
                value={value}
                onChange={(event) => {
                  updateSelection(event.currentTarget);
                  onChange(event.currentTarget.value);
                }}
                onSelect={(event) => updateSelection(event.currentTarget)}
                placeholder={placeholder}
                className={cn(
                  "h-full w-full resize-none rounded-lg border border-input bg-transparent px-3 py-2",
                  "text-body outline-none placeholder:text-muted-foreground",
                  "focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50",
                  "overflow-y-auto",
                )}
              />
            ) : (
              <div
                aria-label={ariaLabel}
                className="h-full w-full overflow-y-auto rounded-lg border bg-background px-4 py-3"
              >
                <Markdown mode="full">{value}</Markdown>
              </div>
            )}
          </div>

          {hintNode}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={handleCancel}
              disabled={saving}
            >
              {t(($) => $.cancel)}
            </Button>
            {onSave ? (
              <Button type="button" onClick={() => void handleSave()} disabled={saving}>
                {saving && (
                  <Loader2
                    className="h-3.5 w-3.5 animate-spin motion-reduce:animate-none"
                    aria-hidden="true"
                  />
                )}
                {t(($) => $.save)}
              </Button>
            ) : (
              <Button type="button" onClick={handleCommit}>
                {t(($) => $.long_text_editor.done)}
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
