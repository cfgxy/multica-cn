"use client";

import { CircleHelp, CircleSlash, ListChecks, type LucideIcon } from "lucide-react";
import type { IssueDecisionStatus } from "@multica/core/types";

// Decision status presentation (RUYI-547): the board columns and list section
// headings share one config, shaped like the issues STATUS_CONFIG so the two
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
  columnBg: string;
}

export const DECISION_STATUS_CONFIG: Record<
  IssueDecisionStatus,
  DecisionStatusConfig
> = {
  open: { icon: CircleHelp, iconColor: "text-brand", columnBg: "bg-brand/5" },
  answered: { icon: ListChecks, iconColor: "text-info", columnBg: "bg-info/5" },
  cancelled: {
    icon: CircleSlash,
    iconColor: "text-muted-foreground",
    columnBg: "bg-muted/40",
  },
};
