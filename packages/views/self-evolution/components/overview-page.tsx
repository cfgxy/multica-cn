"use client";

import { useQuery } from "@tanstack/react-query";
import { skillCatalogOptions } from "@multica/core/workspace/queries";
import {
  selfEvolutionModelConfigOptions,
} from "@multica/core/self-evolution";
import { useWorkspacePaths } from "@multica/core/paths";
import { SelfEvolutionShell } from "./self-evolution-shell";
import { OverviewTab } from "./overview-tab";
import { AttentionList, type AttentionItem } from "./attention-list";
import { useT } from "../../i18n";
import { useWorkspaceId } from "@multica/core/hooks";

/**
 * The module's landing page (RUYI-551 §2.2): the read-only overview lens,
 * fronted by a "needs attention" list that only ever names actionable
 * problems — each row's fix is a navigation, never a write, so the overview
 * tree stays inside the RUYI-284 read-only contract (walk #14).
 */
export function SelfEvolutionOverviewPage() {
  const { t } = useT("self-evolution");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const config = useQuery(selfEvolutionModelConfigOptions(wsId));
  const catalog = useQuery({
    ...skillCatalogOptions(wsId),
    enabled: wsId !== "",
    staleTime: 60_000,
  });

  const items: AttentionItem[] = [];
  if (config.data) {
    const status = config.data.resolved.status;
    if (status === "unconfigured") {
      items.push({
        tone: "danger",
        title: t(($) => $.attention.modelUnconfigured.title),
        description: t(($) => $.attention.modelUnconfigured.description),
        action: {
          label: t(($) => $.attention.modelUnconfigured.action),
          href: paths.selfEvolutionConfig(),
        },
      });
    } else if (status === "error") {
      items.push({
        tone: "danger",
        title: t(($) => $.attention.modelError.title),
        description: config.data.override?.last_validation_error || undefined,
        action: {
          label: t(($) => $.attention.modelError.action),
          href: paths.selfEvolutionConfig(),
        },
      });
    }
  }
  const pendingDiscoveries = (catalog.data ?? []).filter(
    (e) => e.kind === "discovery" && !!e.runtime_id && !e.matching_skill_id,
  ).length;
  if (pendingDiscoveries > 0) {
    items.push({
      tone: "warning",
      title: t(($) => $.attention.discoveries.title, { count: pendingDiscoveries }),
      action: {
        label: t(($) => $.attention.discoveries.action),
        href: paths.selfEvolutionSkills(),
      },
    });
  }

  return (
    <SelfEvolutionShell>
      <div className="flex min-w-0 flex-col gap-6 overflow-y-auto">
        <AttentionList items={items} />
        <OverviewTab wsId={wsId} />
      </div>
    </SelfEvolutionShell>
  );
}
