"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { ChevronRight } from "lucide-react";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import {
  skillUsageOptions,
} from "@multica/core/self-evolution";
import {
  skillCatalogOptions,
  skillListOptions,
} from "@multica/core/workspace/queries";
import type { SkillCatalogEntry, SkillSummary } from "@multica/core/types";
import { useLocale, useT } from "../../i18n";
import { AppLink, useRowLink } from "../../navigation";
import { useCatalogSkillImport } from "../../skills/lib/use-catalog-skill-import";
import { useWorkspacePaths } from "@multica/core/paths";
import { EntitySegToolbar } from "./entity-seg-toolbar";
import { SelfEvolutionShell } from "./self-evolution-shell";
import { useWorkspaceId } from "@multica/core/hooks";

type SkillsSegment = "managed" | "discoveries";

/**
 * The skill-evolution list (RUYI-551 §2.4, fig 5): managed skills and
 * not-yet-imported runtime discoveries live in separate segments — no more
 * dropdown-as-list. A row navigates to the detail route; the page holds no
 * detail state of its own. Editing content stays on the /skills management
 * page, linked from the rows' header action and the detail page.
 */
export function SkillsEvolutionPage() {
  const wsId = useWorkspaceId();
  return (
    <SelfEvolutionShell>
      <SkillsEvolutionBody wsId={wsId} />
    </SelfEvolutionShell>
  );
}

export function SkillsEvolutionBody({ wsId }: { wsId: string }) {
  const { t } = useT("self-evolution");
  const locale = useLocale();
  const paths = useWorkspacePaths();
  const [segment, setSegment] = useState<SkillsSegment>("managed");
  const [search, setSearch] = useState("");
  const [sourceFilter, setSourceFilter] = useState("all");
  const skills = useQuery(skillListOptions(wsId));
  const catalog = useQuery(skillCatalogOptions(wsId));
  const { importSkill, importingKey } = useCatalogSkillImport(wsId);

  // Catalog-derived classification (RUYI-288): source badges for authored
  // skills, plus the not-yet-imported runtime sightings that join the
  // managed list through import.
  const catalogSourceLabel = (source: string) => {
    switch (source) {
      case "workspace": return t(($) => $.skills.catalogSourceWorkspace);
      case "runtime": return t(($) => $.skills.catalogSourceRuntime);
      case "plugin": return t(($) => $.skills.catalogSourcePlugin);
      default: return source;
    }
  };
  const authoredSource = useMemo(() => {
    const map = new Map<string, string>();
    for (const entry of catalog.data ?? []) {
      if (entry.kind === "skill") map.set(entry.id ?? "", entry.source);
    }
    return map;
  }, [catalog.data]);
  const discoveries = useMemo(
    () => (catalog.data ?? []).filter(
      (e): e is SkillCatalogEntry & { runtime_id: string; key: string } =>
        e.kind === "discovery" && !!e.runtime_id && !!e.key,
    ),
    [catalog.data],
  );
  const trimmedSearch = search.trim().toLowerCase();
  const matchesSearch = (name: string) =>
    !trimmedSearch || name.toLowerCase().includes(trimmedSearch);
  const matchesSource = (source: string) =>
    sourceFilter === "all" || source === sourceFilter;
  const managed = (skills.data ?? []).filter(
    (s) => matchesSearch(s.name) && matchesSource(authoredSource.get(s.id) ?? "workspace"),
  );
  const visibleDiscoveries = discoveries.filter(
    (d) => matchesSearch(d.name) && matchesSource("runtime"),
  );
  const handleImport = async (entry: SkillCatalogEntry) => {
    try {
      await importSkill(entry);
      toast.success(t(($) => $.skills.discoveryImportedToast));
    } catch {
      toast.error(t(($) => $.skills.discoveryImportFailedToast));
    }
  };

  if (skills.isPending) {
    return <div className="space-y-4"><Skeleton className="h-9 w-56" /><Skeleton className="h-48 w-full" /></div>;
  }
  if (skills.isError) {
    return (
      <p role="alert" className="text-body text-destructive">
        {t(($) => $.skills.error)}{" "}
        <Button variant="outline" size="sm" onClick={() => skills.refetch()}>{t(($) => $.skills.retry)}</Button>
      </p>
    );
  }

  const segments = [
    { value: "managed" as const, label: t(($) => $.skillsSeg.managed), count: managed.length },
    { value: "discoveries" as const, label: t(($) => $.skillsSeg.discoveries), count: discoveries.length },
  ];

  return (
    <div className="flex min-w-0 flex-col gap-4 overflow-y-auto">
      <EntitySegToolbar
        segments={segments}
        value={segment}
        onValueChange={(next) => setSegment(next as SkillsSegment)}
        filters={
          <>
            <Input
              className="w-44"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={t(($) => $.skills.catalogSearchPlaceholder)}
              aria-label={t(($) => $.skills.catalogSearchLabel)}
            />
            <Select
              items={[
                { value: "all", label: t(($) => $.skills.catalogSourceAll) },
                { value: "workspace", label: t(($) => $.skills.catalogSourceWorkspace) },
                { value: "runtime", label: t(($) => $.skills.catalogSourceRuntime) },
                { value: "plugin", label: t(($) => $.skills.catalogSourcePlugin) },
              ]}
              value={sourceFilter}
              onValueChange={(value) => { if (typeof value === "string") setSourceFilter(value); }}
            >
              <SelectTrigger className="w-36" size="sm"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">{t(($) => $.skills.catalogSourceAll)}</SelectItem>
                <SelectItem value="workspace">{t(($) => $.skills.catalogSourceWorkspace)}</SelectItem>
                <SelectItem value="runtime">{t(($) => $.skills.catalogSourceRuntime)}</SelectItem>
                <SelectItem value="plugin">{t(($) => $.skills.catalogSourcePlugin)}</SelectItem>
              </SelectContent>
            </Select>
          </>
        }
        action={
          <Button size="sm" variant="outline" render={<AppLink href={paths.skills()} />}>
            {t(($) => $.skills.manage)}
          </Button>
        }
      />

      {segment === "managed" ? (
        managed.length === 0 ? (
          <div className="space-y-2 text-body">
            <p>{t(($) => $.skills.empty)}</p>
            <AppLink href={paths.skills()} className="text-primary">{t(($) => $.skills.manage)}</AppLink>
          </div>
        ) : (
          <div className="overflow-hidden rounded-lg border" data-testid="skills-managed-table">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-[30%]">{t(($) => $.skillsSeg.colSkill)}</TableHead>
                  <TableHead>{t(($) => $.skillsSeg.colSource)}</TableHead>
                  <TableHead>{t(($) => $.skillsSeg.colLatestVersion)}</TableHead>
                  <TableHead>{t(($) => $.skillsSeg.colInvocations30d)}</TableHead>
                  <TableHead className="w-[2rem]"><span className="sr-only">{t(($) => $.knowledgeSeg.colActions)}</span></TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {managed.map((skill) => (
                  <ManagedSkillRow
                    key={skill.id}
                    wsId={wsId}
                    skill={skill}
                    sourceLabel={catalogSourceLabel(authoredSource.get(skill.id) ?? "workspace")}
                    detailHref={paths.selfEvolutionSkillDetail(skill.id)}
                    locale={locale}
                  />
                ))}
              </TableBody>
            </Table>
          </div>
        )
      ) : (
        <DiscoveriesSegment
          entries={visibleDiscoveries}
          importingKey={importingKey}
          onImport={handleImport}
          locale={locale}
          noMatch={discoveries.length > 0 && visibleDiscoveries.length === 0}
        />
      )}
    </div>
  );
}

/**
 * One managed-skill row: identity left, the 30-day usage metrics right.
 * The per-row metrics ride their own usage query — the list is small and
 * each query is the same one the detail page reads, so a row can never
 * disagree with its detail page by construction.
 */
function ManagedSkillRow({
  wsId,
  skill,
  sourceLabel,
  detailHref,
  locale,
}: {
  wsId: string;
  skill: SkillSummary;
  sourceLabel: string;
  detailHref: string;
  locale: string;
}) {
  const { t } = useT("self-evolution");
  const rowLink = useRowLink();
  const usage = useQuery({ ...skillUsageOptions(wsId, skill.id), staleTime: 60_000 });
  const latest = usage.data?.versions[0];
  return (
    <TableRow
      className="cursor-pointer"
      data-testid={`skills-row-${skill.id}`}
      {...rowLink(detailHref, skill.name)}
    >
      <TableCell>
        <span className="block max-w-72 truncate text-body font-medium">{skill.name}</span>
        {skill.description ? (
          <span className="block max-w-72 truncate text-caption text-muted-foreground">
            {skill.description}
          </span>
        ) : null}
      </TableCell>
      <TableCell>
        <Badge variant="outline">{sourceLabel}</Badge>
      </TableCell>
      <TableCell className="text-caption">
        {latest ? t(($) => $.skills.version, { version: latest.version }) : "—"}
      </TableCell>
      <TableCell className="text-caption text-muted-foreground">
        {usage.isPending
          ? "…"
          : usage.data
            ? t(($) => $.skillsSeg.invocationsValue, {
                count: usage.data.last_30_days ?? 0,
                date: usage.data.since
                  ? new Date(usage.data.since).toLocaleDateString(locale)
                  : "",
              })
            : "—"}
      </TableCell>
      <TableCell className="text-right">
        <ChevronRight className="ml-auto size-4 text-muted-foreground" aria-hidden />
      </TableCell>
    </TableRow>
  );
}

/**
 * Runtime-discovered skills that are not imported yet (RUYI-288). Metadata
 * only: each row explains its state — importable, or already covered by an
 * authored skill of the same name — and the import action reuses the
 * existing runtime-local import flow.
 */
function DiscoveriesSegment({
  entries,
  importingKey,
  onImport,
  locale,
  noMatch,
}: {
  entries: (SkillCatalogEntry & { runtime_id: string; key: string })[];
  importingKey: string | null;
  onImport: (entry: SkillCatalogEntry) => void;
  locale: string;
  noMatch: boolean;
}) {
  const { t } = useT("self-evolution");
  if (entries.length === 0) {
    return (
      <p className="text-body text-muted-foreground">
        {noMatch
          ? t(($) => $.skills.catalogNoMatch)
          : t(($) => $.skillsSeg.emptyDiscoveries)}
      </p>
    );
  }
  return (
    <section className="space-y-2" aria-label={t(($) => $.skills.discoveriesTitle)}>
      <div className="divide-y rounded-lg border bg-card" data-testid="skills-discoveries">
        {entries.map((entry) => (
          <div key={`${entry.runtime_id}:${entry.key}`} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2">
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-1.5">
                <span className="truncate text-body font-medium">{entry.name}</span>
                {entry.matching_skill_id
                  ? <Badge variant="outline" className="shrink-0 px-1.5 py-0 text-micro font-normal">{t(($) => $.skills.discoveryExists)}</Badge>
                  : <Badge variant="outline" className="shrink-0 px-1.5 py-0 text-micro font-normal">{t(($) => $.skills.catalogSourceRuntime)}</Badge>}
              </div>
              <p className="truncate text-caption text-muted-foreground">
                {entry.description || entry.source_path}
                {` · ${t(($) => $.skills.discoveryLastSeen, { date: new Date(entry.last_seen_at ?? Date.now()).toLocaleString(locale) })}`}
              </p>
            </div>
            {entry.matching_skill_id ? null : (
              <Button variant="outline" size="sm" disabled={importingKey !== null} onClick={() => onImport(entry)}>
                {importingKey === entry.key ? t(($) => $.skills.discoveryImporting) : t(($) => $.skills.discoveryImport)}
              </Button>
            )}
          </div>
        ))}
      </div>
    </section>
  );
}
