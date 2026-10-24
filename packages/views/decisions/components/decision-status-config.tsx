"use client";

import { CircleHelp, CircleSlash, ListChecks, type LucideIcon } from "lucide-react";
import type { IssueDecisionStatus } from "@multica/core/types";

// Decision status presentation (RUYI-547): shared by the list section
// headings and the status filter, shaped like the issues STATUS_CONFIG so the
// surfaces read as one product. Colors follow the issue categories the states
// behave as: open ≈ actionable (brand), answered ≈ done (info), cancelled ≈
// the neutral cancelled category.

export const DECISION_STATUS_ORDER: IssueDecisionStatus[] = [
  "open",
  "answered",
  "cancelled",
];

export interface DecisionStatusConfig {
  icon: LucideIcon;
  iconColor: string;
}

export const DECISION_STATUS_CONFIG: Record<
  IssueDecisionStatus,
  DecisionStatusConfig
> = {
  open: { icon: CircleHelp, iconColor: "text-brand" },
  answered: { icon: ListChecks, iconColor: "text-info" },
  cancelled: {
    icon: CircleSlash,
    iconColor: "text-muted-foreground",
  },
};
