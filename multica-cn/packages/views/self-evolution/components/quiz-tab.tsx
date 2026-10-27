"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@multica/ui/components/ui/empty";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { promptQuizBaselineOptions, toQuizView } from "@multica/core/self-evolution";
import { useCurrentMember } from "@multica/core/permissions";
import { agentListOptions } from "@multica/core/workspace/queries";
import type { Agent } from "@multica/core/types";
import { useT } from "../../i18n";
import { QuizBankPanel } from "./quiz-bank-panel";
import { QuizGradedPanel } from "./quiz-graded-panel";
import { QuizReadingCard } from "./quiz-reading-card";

/**
 * The quiz tab (RUYI-185).
 *
 * Two halves that do not gate each other: the reading for one prompt scope, and
 * the bank the reading is produced from. Picking no subject hides the reading
 * and leaves the bank visible — the bank is workspace-wide and editable whether
 * or not anything has been measured yet.
 *
 * The tab lives inside the page, so no route and no registry entry is added.
 */
export function QuizTab({
  wsId,
  initialAgentId = "",
}: {
  wsId: string;
  /** Preselects the subject. The page leaves it empty; tests supply one. */
  initialAgentId?: string;
}) {
  const { t } = useT("self-evolution");
  const [agentId, setAgentId] = useState(initialAgentId);

  const currentMember = useCurrentMember(wsId);
  // Bank writes are owner-only on the server; admin is deliberately not
  // included, because editing a question body changes what every future
  // version is measured against.
  const canManage = currentMember.role === "owner";

  const agents = useQuery(agentListOptions(wsId));
  const baseline = useQuery(promptQuizBaselineOptions(wsId, "agent", agentId));

  const agentItems = useMemo(
    () => (agents.data ?? []).map((a: Agent) => ({ value: a.id, label: a.name })),
    [agents.data],
  );

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1.5">
        <span className="text-caption text-muted-foreground">
          {t(($) => $.quality.scopePicker.subjectLabel)}
        </span>
        <Select
          items={agentItems}
          value={agentId}
          onValueChange={(next) => {
            if (typeof next === "string") setAgentId(next);
          }}
        >
          <SelectTrigger size="sm" className="w-56">
            <SelectValue>
              {agentItems.find((i) => i.value === agentId)?.label ??
                t(($) => $.quality.scopePicker.subjectPlaceholder)}
            </SelectValue>
          </SelectTrigger>
          <SelectContent align="start" className="max-h-72">
            {agentItems.map((item) => (
              <SelectItem key={item.value} value={item.value}>
                {item.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      {agentId === "" ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>{t(($) => $.quality.noSubject.title)}</EmptyTitle>
            <EmptyDescription>{t(($) => $.quiz.noSubject)}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : baseline.isPending ? (
        <Skeleton className="h-40 w-full rounded-xl" />
      ) : baseline.isError ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>{t(($) => $.quiz.reading.error)}</EmptyTitle>
          </EmptyHeader>
        </Empty>
      ) : (
        <QuizReadingCard view={toQuizView(baseline.data)} />
      )}

      {/* The graded side reflects the rubrics — owner-only private halves — so
          it mounts exactly where the bank editor does, and never for a member. */}
      {canManage && agentId !== "" && baseline.isSuccess ? (
        <QuizGradedPanel wsId={wsId} agentId={agentId} baseline={baseline.data} />
      ) : null}

      <QuizBankPanel wsId={wsId} canManage={canManage} />
    </div>
  );
}
