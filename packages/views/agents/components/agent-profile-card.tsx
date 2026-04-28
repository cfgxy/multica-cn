"use client";

import { useQuery } from "@tanstack/react-query";
import { useAgentPresenceDetail } from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import { useWorkspacePaths } from "@multica/core/paths";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { AppLink } from "../../navigation";
import { availabilityConfig } from "../presence";

interface AgentProfileCardProps {
  agentId: string;
}

export function AgentProfileCard({ agentId }: AgentProfileCardProps) {
  const wsId = useWorkspaceId();
  const p = useWorkspacePaths();
  const { data: agents = [], isLoading: agentsLoading } = useQuery(agentListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));

  const agent = agents.find((a) => a.id === agentId);

  if (agentsLoading && !agent) {
    return (
      <div className="flex items-center gap-3">
        <Skeleton className="h-10 w-10 rounded-full" />
        <div className="flex-1 space-y-1.5">
          <Skeleton className="h-4 w-28" />
          <Skeleton className="h-3 w-20" />
        </div>
      </div>
    );
  }

  if (!agent) {
    return (
      <div className="text-xs text-muted-foreground">Agent unavailable</div>
    );
  }

  const owner = agent.owner_id
    ? members.find((m) => m.user_id === agent.owner_id) ?? null
    : null;
  const isArchived = !!agent.archived_at;
  const initials = agent.name
    .split(" ")
    .map((w) => w[0])
    .join("")
    .toUpperCase()
    .slice(0, 2);

  return (
    <div className="flex flex-col gap-3 text-left">
      {/* Header — avatar + name + availability on the left, "Detail →" link
          on the right. The hover card stays minimal: only the 3-state
          availability dot is shown here. Last-task state lives in the
          agents list (where there's room) and the agent detail page —
          users click "Detail" to see logs and outcome history. */}
      <div className="flex items-start gap-3">
        <ActorAvatarBase
          name={agent.name}
          initials={initials}
          avatarUrl={agent.avatar_url}
          isAgent
          size={40}
          className="rounded-md"
        />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <p className="truncate text-sm font-semibold">{agent.name}</p>
            {isArchived && (
              <span className="rounded-md bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                Archived
              </span>
            )}
          </div>
          {!isArchived && (
            <AgentAvailabilityLine wsId={wsId} agentId={agent.id} />
          )}
        </div>
        {!isArchived && (
          <AppLink
            href={p.agentDetail(agent.id)}
            className="mt-0.5 shrink-0 text-xs font-normal text-brand transition-opacity hover:opacity-80"
          >
            Detail →
          </AppLink>
        )}
      </div>

      {/* Description */}
      {agent.description && (
        <p className="line-clamp-2 text-xs text-muted-foreground">
          {agent.description}
        </p>
      )}

      {/* Meta rows — only the workspace-defining ones. Runtime is implied
          by the provider/availability already shown above; Model is a
          power-user concern that lives on the detail page. */}
      <div className="flex flex-col gap-1.5 text-xs">
        {agent.skills.length > 0 && (
          <SkillsRow skills={agent.skills.map((s) => s.name)} />
        )}
        {owner && <MetaRow label="Owner" value={owner.name} />}
      </div>
    </div>
  );
}

// Compact availability line under the agent name — single 3-state signal
// (online / unstable / offline). Last-task state is intentionally NOT
// shown here; it belongs in the agents list and the detail page where
// there's room for icon + label + reason without crowding the popover.
function AgentAvailabilityLine({
  wsId,
  agentId,
}: {
  wsId: string | undefined;
  agentId: string;
}) {
  const detail = useAgentPresenceDetail(wsId, agentId);
  if (detail === "loading") {
    return <Skeleton className="mt-0.5 h-3 w-16" />;
  }
  const av = availabilityConfig[detail.availability];
  return (
    <div className="mt-0.5 inline-flex items-center gap-1.5">
      <span className={`h-1.5 w-1.5 rounded-full ${av.dotClass}`} />
      <span className={`text-xs ${av.textClass}`}>{av.label}</span>
    </div>
  );
}

function MetaRow({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="flex items-center gap-1.5">
      <span className="w-12 shrink-0 text-muted-foreground">{label}</span>
      <span className={`truncate ${mono ? "font-mono text-[11px]" : ""}`} title={value}>
        {value}
      </span>
    </div>
  );
}

function SkillsRow({ skills }: { skills: string[] }) {
  const visible = skills.slice(0, 3);
  const overflow = skills.length - visible.length;
  return (
    <div className="flex items-center gap-1.5">
      <span className="w-12 shrink-0 text-muted-foreground">Skills</span>
      <div className="flex min-w-0 flex-wrap gap-1">
        {visible.map((s) => (
          <span
            key={s}
            className="rounded-md bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground"
          >
            {s}
          </span>
        ))}
        {overflow > 0 && (
          <span className="rounded-md bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
            +{overflow}
          </span>
        )}
      </div>
    </div>
  );
}
