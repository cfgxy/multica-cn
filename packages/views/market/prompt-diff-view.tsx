"use client";

import { useMemo } from "react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import { diffPromptText } from "./prompt-diff";

/**
 * Renders the comparison a user reads before agreeing to overwrite a prompt.
 *
 * Colour is never the only signal: every line keeps a leading `+` / `−` and a
 * line number on the side it exists in, so the diff survives a colour-blind
 * reader, a monochrome screenshot, and a printed page.
 */
export function PromptDiffView({
  current,
  incoming,
  className,
}: {
  current: string;
  incoming: string;
  className?: string;
}) {
  const { t } = useT("prompt-market");
  const diff = useMemo(
    () => diffPromptText(current, incoming),
    [current, incoming],
  );

  return (
    <div className={cn("space-y-1.5", className)}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-caption text-muted-foreground">
        <span>
          {t(($) => $.apply.diff_summary, {
            added: diff.added,
            removed: diff.removed,
          })}
        </span>
        {diff.truncated ? (
          <span className="text-destructive">
            {t(($) => $.apply.diff_truncated)}
          </span>
        ) : null}
      </div>
      <div className="max-h-80 overflow-auto rounded-lg border bg-muted/20">
        <table className="w-full border-collapse font-mono text-caption leading-5">
          <tbody>
            {diff.lines.map((line, index) => (
              <tr
                key={index}
                className={cn(
                  line.kind === "added" && "bg-success/10",
                  line.kind === "removed" && "bg-destructive/10",
                )}
              >
                {/* Hidden below `sm`: on a phone the gutter costs more width
                    than the line numbers are worth, and the +/- prefix still
                    carries the meaning. */}
                <td className="hidden w-11 select-none px-2 text-right align-top text-micro text-faint-foreground sm:table-cell">
                  {line.currentLine ?? ""}
                </td>
                <td className="hidden w-11 select-none px-2 text-right align-top text-micro text-faint-foreground sm:table-cell">
                  {line.incomingLine ?? ""}
                </td>
                <td className="w-4 select-none pl-2 align-top text-faint-foreground">
                  {line.kind === "added" ? "+" : line.kind === "removed" ? "−" : " "}
                </td>
                <td className="whitespace-pre-wrap break-words px-2 align-top">
                  {line.text === "" ? " " : line.text}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
