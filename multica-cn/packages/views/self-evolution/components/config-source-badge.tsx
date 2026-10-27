"use client";

import { Badge } from "@multica/ui/components/ui/badge";
import { useT } from "../../i18n";

/**
 * Which configuration a card or field is currently effective by
 * (RUYI-551 §3): the workspace's own module config, the deployment-injected
 * env default, or a system-level source the workspace cannot configure.
 * Always neutral gray — an origin must never wear a health color (§5.3).
 */
export type ConfigSource = "module_config" | "deploy_default" | "system";

export function ConfigSourceBadge({
  source,
  className,
}: {
  source: ConfigSource;
  className?: string;
}) {
  const { t } = useT("self-evolution");
  const label =
    source === "module_config"
      ? t(($) => $.configSource.module)
      : source === "deploy_default"
        ? t(($) => $.configSource.deploy)
        : t(($) => $.configSource.system);
  return (
    <Badge
      variant="outline"
      className={
        className ??
        "border-surface-border bg-muted/40 font-normal text-muted-foreground"
      }
    >
      {t(($) => $.configSource.prefix)}
      {label}
    </Badge>
  );
}
