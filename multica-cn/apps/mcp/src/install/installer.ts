import type { ClientTarget, InstallOutcome, McpEntry, StatusOutcome, UninstallOutcome } from "./types.js";

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
        case "skipped-not-registered":
          return `[跳过] ${target.label}：尚未注册 multica，无需更新（先运行 mcp-install）`;
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

/**
 * Like `runInstall`, but only touches clients that already have a `multica`
 * entry registered — used by `mcp-update` so refreshing the dist path after
 * moving/rebuilding the checkout never silently opts a newly-detected
 * client into registration.
 */
export function runUpdate(targets: ClientTarget[], entry: McpEntry): InstallReportEntry[] {
  return targets.map((target) => {
    const status = target.read();
    if (status.status !== "registered") {
      return { target, outcome: { status: "skipped-not-registered" } };
    }
    return { target, outcome: target.apply(entry) };
  });
}

export interface StatusReportEntry {
  target: ClientTarget;
  outcome: StatusOutcome;
}

/** Read-only scan of every target's current registration state. */
export function runStatus(targets: ClientTarget[]): StatusReportEntry[] {
  return targets.map((target) => ({ target, outcome: target.read() }));
}

/** Renders a human-readable, one-line-per-client summary for CLI output. */
export function formatStatusReport(results: StatusReportEntry[]): string {
  return results
    .map(({ target, outcome }) => {
      switch (outcome.status) {
        case "registered":
          return outcome.stale
            ? `[已注册·失效] ${target.label} -> ${outcome.path}（指向 ${outcome.distPath}，该路径不存在，需重新 mcp-update/mcp-install）`
            : `[已注册] ${target.label} -> ${outcome.path}（指向 ${outcome.distPath}）`;
        case "not-installed":
          return `[未注册] ${target.label}：已检测到本机安装，但无 multica 条目`;
        case "not-detected":
          return `[未检测到] ${target.label}：本机未安装`;
        case "parse-error":
          return `[无法读取] ${target.label}：${outcome.path} ${outcome.reason}`;
        default: {
          const exhaustive: never = outcome;
          return exhaustive;
        }
      }
    })
    .join("\n");
}

export interface UninstallReportEntry {
  target: ClientTarget;
  outcome: UninstallOutcome;
}

/** Removes the `multica` entry from every target's config, in order. */
export function runUninstall(targets: ClientTarget[]): UninstallReportEntry[] {
  return targets.map((target) => ({ target, outcome: target.remove() }));
}

/** Renders a human-readable, one-line-per-client summary for CLI output. */
export function formatUninstallReport(results: UninstallReportEntry[]): string {
  return results
    .map(({ target, outcome }) => {
      switch (outcome.status) {
        case "removed":
          return `[已移除] ${target.label} -> ${outcome.path}`;
        case "not-installed":
          return `[跳过] ${target.label}：未注册 multica，无需移除`;
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
