/**
 * Structured relations sidebar section (RUYI-689).
 *
 * Renders the five edge views of `GET /api/issues/{id}/relations` —
 * blocks / blocked_by / relates_to / supersedes / superseded_by — as
 * collapsible groups of tappable rows (status icon + identifier + title),
 * reusing the exact row visual the Parent and Pull requests sections
 * established. Parent and children deliberately stay in their dedicated
 * sections (Parent block above, sub-issues panel below); duplicating them
 * here would spend sidebar space on data that is already on screen.
 *
 * Self-contained like QuickActionsSection: owns its query and collapse
 * state, hides itself entirely while loading, on error, or when none of
 * the five groups has entries — no relations, no sidebar footprint.
 *
 * Freshness: relation writes emit no WS event (the server only bumps the
 * anchor revisions), so the query re-pulls on every mount
 * (`refetchOnMount: "always"`); changes made elsewhere surface on the
 * next visit to the page. Markdown @mentions are a different feature —
 * they aggregate in IssueReferenceTail below the description and share
 * nothing with this section (data source, component, or copy).
 */
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight } from "lucide-react";
import { issueRelationsOptions } from "@multica/core/issues/queries";
import type { IssueRelationRef } from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";
import { StatusIcon } from ".";

type EdgeGroupKey =
  | "blocks"
  | "blocked_by"
  | "relates_to"
  | "supersedes"
  | "superseded_by";

function RelationRow({ relation }: { relation: IssueRelationRef }) {
  const paths = useWorkspacePaths();
  return (
    <div className="flex items-center rounded-md px-2 -mx-2 hover:bg-accent/50 transition-colors">
      <AppLink
        href={paths.issueDetail(relation.id)}
        className="flex flex-1 min-w-0 items-center gap-1.5 py-1.5 text-caption"
      >
        <StatusIcon
          status={relation.status}
          className="h-3.5 w-3.5 shrink-0"
        />
        <span className="text-muted-foreground shrink-0">{relation.identifier}</span>
        <span className="truncate">{relation.title}</span>
      </AppLink>
    </div>
  );
}

export function IssueRelationsSection({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const [open, setOpen] = useState(true);
  const { data: relations } = useQuery({
    ...issueRelationsOptions(wsId ?? "", issueId),
    enabled: !!wsId,
  });

  if (!wsId || !relations) return null;
  const allGroups: {
    key: EdgeGroupKey;
    label: string;
    refs: IssueRelationRef[];
  }[] = [
    { key: "blocks", label: t(($) => $.detail.relations_blocks), refs: relations.blocks },
    { key: "blocked_by", label: t(($) => $.detail.relations_blocked_by), refs: relations.blocked_by },
    { key: "relates_to", label: t(($) => $.detail.relations_relates_to), refs: relations.relates_to },
    { key: "supersedes", label: t(($) => $.detail.relations_supersedes), refs: relations.supersedes },
    { key: "superseded_by", label: t(($) => $.detail.relations_superseded_by), refs: relations.superseded_by },
  ];
  const groups = allGroups.filter((g) => g.refs.length > 0);
  if (groups.length === 0) return null;

  const total = groups.reduce((sum, g) => sum + g.refs.length, 0);

  return (
    <div>
      <button
        type="button"
        className={`flex w-full items-center gap-1 rounded-md px-2 py-1 text-caption font-medium transition-colors mb-2 hover:bg-accent/70 ${open ? "" : "text-muted-foreground hover:text-foreground"}`}
        onClick={() => setOpen(!open)}
      >
        {t(($) => $.detail.section_relations)}
        <span className="text-muted-foreground font-normal">{total}</span>
        <ChevronRight className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`} />
      </button>
      {open && (
        <div className="pl-2">
          {groups.map((group, gi) => (
            <div key={group.key} className={gi > 0 ? "mt-2" : undefined}>
              <div className="px-2 mb-0.5 text-muted-foreground">
                {group.label}
              </div>
              {group.refs.map((relation) => (
                <RelationRow key={relation.id} relation={relation} />
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
