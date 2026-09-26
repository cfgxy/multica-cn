"use client";

import { useState } from "react";
import { Sprout } from "lucide-react";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@multica/ui/components/ui/empty";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { useWorkspaceId } from "@multica/core/hooks";
import { CollectionPageHeader } from "../../layout/collection-page";
import { PAGE_GUTTER } from "../../layout/page-header";
import { useT } from "../../i18n";
import { cn } from "@multica/ui/lib/utils";
import { QualityTab } from "./quality-tab";

/**
 * Prompt-governance / self-evolution page.
 *
 * Phase 1 (RUYI-183) wired up the nav entry, route and locale surface; the
 * quality tab (RUYI-184) is the first one with data behind it. The tabs live
 * inside the page rather than as sibling routes, so no new route or page key
 * enters the three registries.
 */
export function SelfEvolutionPage() {
  const { t } = useT("self-evolution");
  const wsId = useWorkspaceId();
  const [tab, setTab] = useState("quality");

  return (
    <div className="flex h-full min-h-0 flex-col gap-0">
      <CollectionPageHeader
        icon={Sprout}
        title={t(($) => $.title)}
        description={t(($) => $.description)}
      />
      <div className={cn(PAGE_GUTTER, "min-h-0 flex-1 overflow-y-auto py-6")}>
        <Tabs
          value={tab}
          onValueChange={(next) => {
            if (typeof next === "string") setTab(next);
          }}
        >
          <TabsList>
            <TabsTrigger value="overview">{t(($) => $.tabs.overview)}</TabsTrigger>
            <TabsTrigger value="quality">{t(($) => $.tabs.quality)}</TabsTrigger>
          </TabsList>
          <TabsContent value="overview" className="pt-6">
            <Empty>
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <Sprout className="h-4 w-4" />
                </EmptyMedia>
                <EmptyTitle>{t(($) => $.empty.title)}</EmptyTitle>
                <EmptyDescription>{t(($) => $.empty.description)}</EmptyDescription>
              </EmptyHeader>
            </Empty>
          </TabsContent>
          <TabsContent value="quality" className="pt-6">
            <QualityTab wsId={wsId} />
          </TabsContent>
        </Tabs>
      </div>
    </div>
  );
}
