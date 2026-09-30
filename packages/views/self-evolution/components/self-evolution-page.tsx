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
import { KnowledgeTab } from "./knowledge-tab";
import { ProposalTab } from "./proposal-tab";
import { QualityTab } from "./quality-tab";
import { QuizTab } from "./quiz-tab";
import { SkillTab } from "./skill-tab";
import { VersionsTab } from "./versions-tab";

/**
 * Prompt-governance / self-evolution page.
 *
 * Phase 1 (RUYI-183) wired up the nav entry, route and locale surface; the
 * quality tab (RUYI-184) is the first one with data behind it, and the quiz tab
 * (RUYI-185) reads the same scopes through a fixed question bank. Skill history
 * and observed use join them in phase four, and the proposal pool plus the
 * knowledge mirror (RUYI-265) close the loop from evidence to adoption. The
 * tabs live inside the page rather than as sibling routes, so no new route or
 * page key enters the three registries.
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
            <TabsTrigger value="versions">{t(($) => $.tabs.versions)}</TabsTrigger>
            <TabsTrigger value="quiz">{t(($) => $.tabs.quiz)}</TabsTrigger>
            <TabsTrigger value="skills">{t(($) => $.tabs.skills)}</TabsTrigger>
            <TabsTrigger value="proposals">{t(($) => $.tabs.proposals)}</TabsTrigger>
            <TabsTrigger value="knowledge">{t(($) => $.tabs.knowledge)}</TabsTrigger>
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
            <QualityTab wsId={wsId} onManageVersions={() => setTab("versions")} />
          </TabsContent>
          <TabsContent value="versions" className="pt-6">
            <VersionsTab wsId={wsId} />
          </TabsContent>
          <TabsContent value="quiz" className="pt-6">
            <QuizTab wsId={wsId} />
          </TabsContent>
          <TabsContent value="skills" className="pt-6">
            <SkillTab wsId={wsId} />
          </TabsContent>
          <TabsContent value="proposals" className="pt-6">
            <ProposalTab wsId={wsId} />
          </TabsContent>
          <TabsContent value="knowledge" className="pt-6">
            <KnowledgeTab wsId={wsId} />
          </TabsContent>
        </Tabs>
      </div>
    </div>
  );
}
