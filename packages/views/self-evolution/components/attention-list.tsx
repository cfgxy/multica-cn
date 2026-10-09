"use client";

import { AlertTriangle, CircleAlert } from "lucide-react";
import { buttonVariants } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";

/**
 * The overview's "needs attention" list (RUYI-551 §2.2 / walk #14): only
 * actionable items render — each row names the problem and carries the fix.
 * Once the condition clears the item disappears; the overview tree stays
 * read-only, actions navigate rather than write.
 */
export interface AttentionItem {
  /** red = blocking (module unusable), amber = degrading. */
  tone: "danger" | "warning";
  title: string;
  description?: string;
  action: {
    label: string;
    /** In-module target path; rendered as an AppLink primary/outline button. */
    href: string;
  };
}

export function AttentionList({ items }: { items: AttentionItem[] }) {
  const { t } = useT("self-evolution");
  if (!items.length) return null;
  return (
    <section
      aria-label={t(($) => $.attention.title)}
      className="flex flex-col gap-2"
      data-testid="se-attention-list"
    >
      <div className="flex items-center justify-between">
        <h2 className="text-body font-semibold">{t(($) => $.attention.title)}</h2>
        <span className="text-caption text-muted-foreground">
          {t(($) => $.attention.hint)}
        </span>
      </div>
      {items.map((item) => (
        <div
          key={item.title}
          className={cn(
            "flex items-center justify-between gap-4 rounded-lg border px-4 py-3",
            item.tone === "danger"
              ? "border-destructive/30 bg-destructive/5"
              : "border-amber-500/30 bg-amber-500/5",
          )}
        >
          <div className="flex min-w-0 items-center gap-2.5">
            {item.tone === "danger" ? (
              <CircleAlert className="size-4 shrink-0 text-destructive" aria-hidden />
            ) : (
              <AlertTriangle className="size-4 shrink-0 text-amber-500" aria-hidden />
            )}
            <div className="min-w-0">
              <span className="text-body font-medium">{item.title}</span>
              {item.description ? (
                <span className="ml-2 text-caption text-muted-foreground">
                  {item.description}
                </span>
              ) : null}
            </div>
          </div>
          <AppLink
            href={item.action.href}
            className={buttonVariants({
              variant: item.tone === "danger" ? "default" : "outline",
              size: "sm",
            })}
          >
            {item.action.label}
          </AppLink>
        </div>
      ))}
    </section>
  );
}
