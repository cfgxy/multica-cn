"use client";

import { Sprout } from "lucide-react";
import type { ReactNode } from "react";
import { useWorkspaceId } from "@multica/core/hooks";
import { CollectionPageHeader } from "../../layout/collection-page";
import { PAGE_GUTTER } from "../../layout/page-header";
import { useT } from "../../i18n";
import { cn } from "@multica/ui/lib/utils";
import { SeSubNav } from "./se-sub-nav";

/**
 * The module shell every self-evolution page renders inside (RUYI-551 §2.1):
 * module header, then the URL-driven left sub-navigation next to the page
 * body. It replaces the old single-page 8-tab container — sub-pages are real
 * routes now, so a refresh or a share keeps the page.
 */
export function SelfEvolutionShell({ children }: { children: ReactNode }) {
  const { t } = useT("self-evolution");
  const wsId = useWorkspaceId();
  return (
    <div className="flex h-full min-h-0 flex-col gap-0">
      <CollectionPageHeader
        icon={Sprout}
        title={t(($) => $.title)}
        description={t(($) => $.description)}
      />
      <div className={cn(PAGE_GUTTER, "flex min-h-0 flex-1 gap-3 py-6 lg:gap-6")}>
        {wsId ? (
          <SeSubNav wsId={wsId} />
        ) : (
          <div className="w-40 shrink-0 lg:w-52" aria-hidden />
        )}
        {/* The body's scroll container: stretched to the row's definite
            height, so tall page content scrolls here instead of growing past
            the canvas and getting clipped with no scrollbar (RUYI-745). */}
        <div className="min-h-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto">{children}</div>
      </div>
    </div>
  );
}
