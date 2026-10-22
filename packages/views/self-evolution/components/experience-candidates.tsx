"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { FilePlus2 } from "lucide-react";
import { toast } from "sonner";
import { selfEvolutionOverviewOptions, useCreatePromptProposal } from "@multica/core/self-evolution";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import type { SelfEvolutionOverviewExperience } from "@multica/core/types/self-evolution-overview";
import type { PromptProposalDraftRequest } from "@multica/core/types";
import { AppLink } from "../../navigation";
import { useLocale, useT } from "../../i18n";

export function ExperienceCandidates({ wsId }: { wsId: string }) {
  const { t } = useT("self-evolution");
  const overview = useQuery(selfEvolutionOverviewOptions(wsId));

  if (overview.isPending) {
    return <Skeleton className="h-24 w-full" aria-label={t(($) => $.overview.experiences.loading)} />;
  }
  if (overview.isError) {
    return (
      <div role="alert" className="flex items-center gap-3 text-caption text-destructive">
        <span>{t(($) => $.overview.experiences.error)}</span>
        <Button size="sm" variant="outline" onClick={() => void overview.refetch()}>
          {t(($) => $.overview.experiences.retry)}
        </Button>
      </div>
    );
  }

  return <ExperienceCandidateList wsId={wsId} experiences={overview.data.experiences} />;
}

function ExperienceCandidateList({ wsId, experiences }: { wsId: string; experiences: SelfEvolutionOverviewExperience[] }) {
  const { t } = useT("self-evolution");
  const paths = useWorkspacePaths();
  return (
    <section className="space-y-3" aria-label={t(($) => $.overview.experiences.title)}>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-title font-medium">{t(($) => $.overview.experiences.title)}</h2>
        <AppLink href={paths.selfEvolutionProposals()} className="text-caption text-primary">
          {t(($) => $.overview.experiences.proposalsLink)}
        </AppLink>
      </div>
      {experiences.length === 0 ? (
        <p className="text-body text-muted-foreground">{t(($) => $.overview.experiences.empty)}</p>
      ) : (
        <ul className="divide-y divide-border">
          {experiences.map((entry) => (
            <ExperienceCandidateItem key={entry.candidate_id} wsId={wsId} entry={entry} />
          ))}
        </ul>
      )}
    </section>
  );
}

function ExperienceCandidateItem({ wsId, entry }: { wsId: string; entry: SelfEvolutionOverviewExperience }) {
  const { t } = useT("self-evolution");
  const locale = useLocale();
  const paths = useWorkspacePaths();
  const createProposal = useCreatePromptProposal(wsId);
  const [created, setCreated] = useState(false);
  const draft = async () => {
    const taskIDs = entry.source_tasks.map((source) => source.task_id);
    const data: PromptProposalDraftRequest = {
      carrier_scope: "workspace",
      carrier_scope_id: wsId,
      target_section: "",
      change_kind: "add_clause",
      clause_name: t(($) => $.overview.experiences.proposalName, { count: taskIDs.length, candidate: entry.candidate_id.slice(0, 8) }),
      clause_text: t(($) => $.overview.experiences.proposalBody, {
        trigger: entry.trigger,
        scope: entry.applicability,
        strategy: entry.suggested_strategy,
        actions: entry.allowed_actions.join(", "),
        tasks: taskIDs.join(", "),
        limitation: t(($) => $.overview.experiences.repeatedLimit),
      }),
      gate_answer_layer: "",
      gate_answer_retention: "",
      gate_answer_cost: "",
      gate_answer_conflict: "",
      gate_answer_dedup: "",
      evidence_anchors: entry.source_tasks.map((source) => ({
        ...(source.issue_id ? { issue_id: source.issue_id } : {}),
        task_id: source.task_id,
        ref: `capability_adaptation_candidate:${entry.candidate_id}`,
        note: `${source.policy_id}@${source.policy_version}; ${source.outcome}`,
      })),
    };
    try {
      await createProposal.mutateAsync(data);
      setCreated(true);
    } catch {
      toast.error(t(($) => $.overview.experiences.createFailed));
    }
  };

  return (
    <li className="flex min-w-0 flex-col gap-1.5 py-3 first:pt-0 last:pb-0">
      <p className="break-words text-body font-medium">{entry.suggested_strategy}</p>
      <p className="break-words text-caption text-muted-foreground">
        {t(($) => $.overview.experiences.currentStrategy, { strategy: entry.current_strategy })}
      </p>
      <p className="break-words text-caption text-muted-foreground">
        {t(($) => $.overview.experiences.sourceCount, { count: entry.source_tasks.length })}
      </p>
      <ul className="flex flex-wrap gap-x-3 gap-y-1 text-caption text-muted-foreground">
        {entry.source_tasks.map((source) => (
          <li key={source.task_id}>
            {source.issue_id ? (
              <AppLink href={paths.issueDetail(source.issue_id)} className="text-primary">
                {t(($) => $.overview.experiences.sourceTask, {
                  task: source.task_id.slice(0, 8),
                  date: new Date(source.completed_at).toLocaleString(locale),
                  policy: `${source.policy_id}@${source.policy_version}`,
                })}
              </AppLink>
            ) : t(($) => $.overview.experiences.sourceTask, {
              task: source.task_id.slice(0, 8),
              date: new Date(source.completed_at).toLocaleString(locale),
              policy: `${source.policy_id}@${source.policy_version}`,
            })}
          </li>
        ))}
      </ul>
      <p className="break-words text-caption text-muted-foreground">
        {t(($) => $.overview.experiences.scope, { scope: entry.applicability })}: {entry.trigger}
      </p>
      <p className="break-words text-caption text-muted-foreground">
        {t(($) => $.overview.experiences.allowedActions, { actions: entry.allowed_actions.join(", ") })}
      </p>
      <p className="break-words text-caption text-muted-foreground">
        {t(($) => $.overview.experiences.limitation)}: {t(($) => $.overview.experiences.repeatedLimit)}
      </p>
      {created ? (
        <AppLink href={paths.selfEvolutionProposals()} className="w-fit text-caption text-primary">
          {t(($) => $.overview.experiences.proposalCreated)}
        </AppLink>
      ) : (
        <Button size="sm" variant="outline" className="w-fit" disabled={createProposal.isPending} onClick={() => void draft()}>
          <FilePlus2 aria-hidden="true" />
          {createProposal.isPending
            ? t(($) => $.overview.experiences.creatingProposal)
            : t(($) => $.overview.experiences.draftProposal)}
        </Button>
      )}
    </li>
  );
}
