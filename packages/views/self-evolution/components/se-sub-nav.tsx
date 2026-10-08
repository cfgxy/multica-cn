"use client";

import { AppLink, useNavigation } from "../../navigation";
import { useT } from "../../i18n";
import { cn } from "@multica/ui/lib/utils";
import { useQuery } from "@tanstack/react-query";
import { useWorkspacePaths } from "@multica/core/paths";
import { selfEvolutionOverviewOptions } from "@multica/core/self-evolution";

/**
 * The module's URL-driven left sub-navigation (RUYI-551 §2.1): 9 sub-nav
 * pages in 4 groups, one route per page, plus a detail route that
 * deliberately stays out of this list. Counts come from the overview
 * aggregate — whatever it has filled, the rest render without a badge
 * rather than with a fake zero.
 */
export type SeNavKey =
  | "overview"
  | "knowledge"
  | "skills"
  | "quality"
  | "quiz"
  | "proposals"
  | "versions"
  | "retrospective"
  | "config";

export const SE_NAV_GROUPS: { labelKey: "groupKnowledgeAssets" | "groupMeasures" | "groupLoop" | "groupConfig"; items: SeNavKey[] }[] = [
  { labelKey: "groupKnowledgeAssets", items: ["knowledge", "skills"] },
  { labelKey: "groupMeasures", items: ["quality", "quiz"] },
  { labelKey: "groupLoop", items: ["proposals", "versions", "retrospective"] },
  { labelKey: "groupConfig", items: ["config"] },
];

export function SeSubNav({ wsId }: { wsId: string }) {
  const { t } = useT("self-evolution");
  const { pathname } = useNavigation();
  const p = useWorkspacePaths();
  const overview = useQuery({
    ...selfEvolutionOverviewOptions(wsId),
    staleTime: 60_000,
  });

  const pathFor = (key: SeNavKey): string => {
    switch (key) {
      case "overview":
        return p.selfEvolution();
      case "knowledge":
        return p.selfEvolutionKnowledge();
      case "skills":
        return p.selfEvolutionSkills();
      case "quality":
        return p.selfEvolutionQuality();
      case "quiz":
        return p.selfEvolutionQuiz();
      case "proposals":
        return p.selfEvolutionProposals();
      case "versions":
        return p.selfEvolutionVersions();
      case "retrospective":
        return p.selfEvolutionRetrospective();
      case "config":
        return p.selfEvolutionConfig();
    }
  };

  const countFor = (key: SeNavKey): number | undefined => {
    const data = overview.data;
    if (!data) return undefined;
    if (key === "knowledge") return data.knowledge.entries;
    if (key === "skills") return data.skills.count;
    return undefined;
  };

  const isActive = (key: SeNavKey) => {
    const path = pathFor(key);
    if (key === "overview") return pathname === path;
    // The detail route lights its list parent: /skills/<id> belongs to 技能演进.
    if (key === "skills") return pathname === path || pathname.startsWith(`${path}/`);
    return pathname.startsWith(path);
  };

  return (
    <nav
      aria-label={t(($) => $.nav.label)}
      className="flex w-52 shrink-0 flex-col gap-5"
      data-testid="se-sub-nav"
    >
      <SeNavItem
        label={t(($) => $.nav.overview)}
        href={pathFor("overview")}
        active={isActive("overview")}
      />
      {SE_NAV_GROUPS.map((group) => (
        <div key={group.labelKey} className="flex flex-col gap-1">
          <div className="px-2 text-micro leading-5 text-muted-foreground">
            {t(($) => $.nav[group.labelKey])}
          </div>
          {group.items.map((key) => {
            const count = countFor(key);
            return (
              <SeNavItem
                key={key}
                label={t(($) => $.nav[key])}
                href={pathFor(key)}
                active={isActive(key)}
                count={count}
              />
            );
          })}
        </div>
      ))}
    </nav>
  );
}

function SeNavItem({
  label,
  href,
  active,
  count,
}: {
  label: string;
  href: string;
  active: boolean;
  count?: number;
}) {
  return (
    <AppLink
      href={href}
      className={cn(
        "flex items-center justify-between rounded-md px-2 py-1.5 text-body",
        active
          ? "bg-muted font-medium text-foreground"
          : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
      )}
      data-active={active || undefined}
    >
      <span>{label}</span>
      {typeof count === "number" && count > 0 ? (
        <span className="text-caption text-muted-foreground">{count}</span>
      ) : null}
    </AppLink>
  );
}
