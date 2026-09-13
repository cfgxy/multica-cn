import type { ClientTarget, InstallOutcome, McpEntry } from "./types.js";

export interface InstallReportEntry {
  target: ClientTarget;
  outcome: InstallOutcome;
}

/** Applies every target and returns one outcome per client, in order. */
export function runInstall(targets: ClientTarget[], entry: McpEntry): InstallReportEntry[] {
  return targets.map((target) => ({ target, outcome: target.apply(entry) }));
}

/** Renders a human-readable, one-line-per-client summary for CLI output. */
export function formatInstallReport(results: InstallReportEntry[]): string {
  return results
    .map(({ target, outcome }) => {
      switch (outcome.status) {
        case "installed":
          return `[已安装] ${target.label} -> ${outcome.path}`;
        case "skipped-not-detected":
          return `[跳过] ${target.label}：未检测到本机安装`;
        case "skipped-parse-error":
          return `[跳过] ${target.label}：${outcome.path} ${outcome.reason}，未修改原文件`;
        default: {
          const exhaustive: never = outcome;
          return exhaustive;
        }
      }
    })
    .join("\n");
}
