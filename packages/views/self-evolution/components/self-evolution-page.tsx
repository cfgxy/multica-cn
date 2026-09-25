"use client";

import { Sprout } from "lucide-react";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@multica/ui/components/ui/empty";
import { CollectionPageHeader } from "../../layout/collection-page";
import { PAGE_GUTTER } from "../../layout/page-header";
import { useT } from "../../i18n";
import { cn } from "@multica/ui/lib/utils";

/**
 * Prompt-governance / self-evolution overview (RUYI-183 phase 1 skeleton).
 *
 * This wires up the nav entry, route and locale surface only — version
 * history, diff and adoption views land in a later stage.
 */
export function SelfEvolutionPage() {
  const { t } = useT("self-evolution");

  return (
    <div className="flex h-full min-h-0 flex-col gap-0">
      <CollectionPageHeader
        icon={Sprout}
        title={t(($) => $.title)}
        description={t(($) => $.description)}
      />
      <div className={cn(PAGE_GUTTER, "py-6")}>
        <Empty>
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <Sprout className="h-4 w-4" />
            </EmptyMedia>
            <EmptyTitle>{t(($) => $.empty.title)}</EmptyTitle>
            <EmptyDescription>{t(($) => $.empty.description)}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      </div>
    </div>
  );
}
