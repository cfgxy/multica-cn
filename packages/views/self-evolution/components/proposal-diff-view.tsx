"use client";

import type { LegislationDiffLine } from "@multica/core/types";

/** Sandbox full-carrier diff, one highlighted line per add/del. */
export function DiffView({ diff }: { diff: LegislationDiffLine[] }) {
  return (
    <div className="max-h-80 overflow-y-auto rounded-md border font-mono text-caption">
      {diff.map((line, i) => (
        <div
          key={i}
          className={
            line.kind === "add"
              ? "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400"
              : line.kind === "del"
                ? "bg-red-500/10 text-red-700 dark:text-red-400"
                : "text-muted-foreground"
          }
        >
          <span className="inline-block w-6 select-none text-center opacity-60">
            {line.kind === "add" ? "+" : line.kind === "del" ? "-" : " "}
          </span>
          <span className="whitespace-pre-wrap">{line.text}</span>
        </div>
      ))}
    </div>
  );
}
