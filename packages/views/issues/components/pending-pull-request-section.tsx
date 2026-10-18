"use client";

import { useQuery } from "@tanstack/react-query";
import { issuePullRequestsOptions, pendingPullRequests } from "@multica/core/github";
import { useT } from "../../i18n";
import { PullRequestRow } from "./pull-request-list";

export function PendingPullRequestSection({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const { data } = useQuery(issuePullRequestsOptions(issueId));
  const prs = pendingPullRequests(data?.pull_requests ?? []);

  if (prs.length === 0) return null;

  return (
    <section
      aria-labelledby="pending-pull-requests-heading"
      data-testid="pending-pull-requests-section"
      className="mt-6 border-t border-border/60 pt-4"
    >
      <h2
        id="pending-pull-requests-heading"
        className="mb-2 text-caption font-medium text-muted-foreground"
      >
        {t(($) => $.detail.section_pending_pull_requests)}
      </h2>
      <div className="space-y-1">
        {prs.map((pr) => <PullRequestRow key={`${pr.provider ?? "github"}:${pr.id}`} pr={pr} showTargetBranch />)}
      </div>
    </section>
  );
}
