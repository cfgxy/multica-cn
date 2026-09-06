/**
 * Credential resolution for the MCP server.
 *
 * Credentials are exactly the CLI's credentials — no second storage format
 * (Owner decision for RUYI-82). Resolution order, highest first:
 *
 *  1. Explicit overrides (`--token` / `--server-url` CLI flags).
 *  2. Environment: `MULTICA_TOKEN`, `MULTICA_SERVER_URL` (same vars the CLI
 *     honors), `MULTICA_PROFILE` selects a named CLI profile.
 *  3. The CLI config file, at the same path the Go CLI resolves
 *     (`server/internal/cli/config.go`): `~/.multica/config.json`, or
 *     `~/.multica/profiles/<name>/config.json`, or — when
 *     `MULTICA_TASK_CONFIG_ROOT` is set (daemon task sandboxing) —
 *     `<root>/config.json` / `<root>/profiles/<name>/config.json`.
 *
 * The default server URL matches the CLI's cloud default
 * (`server/cmd/multica/cmd_agent.go`: api.multica.ai).
 */

import { join } from "node:path";

export const DEFAULT_SERVER_URL = "https://api.multica.ai";

export interface ConfigEnv {
  MULTICA_TOKEN?: string | undefined;
  MULTICA_SERVER_URL?: string | undefined;
  MULTICA_PROFILE?: string | undefined;
  MULTICA_TASK_CONFIG_ROOT?: string | undefined;
  HOME?: string | undefined;
}

export interface CredentialOverrides {
  token?: string | undefined;
  serverUrl?: string | undefined;
  profile?: string | undefined;
}

export interface McpCredentials {
  token: string;
  serverUrl: string;
  /** Where the token came from — safe to log (never contains the value). */
  tokenSource: "flag" | "env" | "cli-config";
}

interface CliConfigFile {
  server_url?: string;
  token?: string;
}

export function cliConfigPath(env: ConfigEnv, profileOverride?: string): string {
  const taskRoot = env.MULTICA_TASK_CONFIG_ROOT?.trim();
  const profile = (profileOverride ?? env.MULTICA_PROFILE ?? "").trim();
  if (taskRoot) {
    return profile
      ? join(taskRoot, "profiles", profile, "config.json")
      : join(taskRoot, "config.json");
  }
  const home = env.HOME?.trim() || ".";
  return profile
    ? join(home, ".multica", "profiles", profile, "config.json")
    : join(home, ".multica", "config.json");
}

export class MissingCredentialsError extends Error {
  constructor(triedPaths: string[]) {
    const where =
      triedPaths.length > 0
        ? ` (looked at: ${triedPaths.join(", ")}, and MULTICA_TOKEN)`
        : " (and MULTICA_TOKEN is not set)";
    super(
      "No Multica credentials found" +
        where +
        ". Run `multica login`, or pass MULTICA_TOKEN / --token.",
    );
    this.name = "MissingCredentialsError";
  }
}

export function resolveCredentials(
  env: ConfigEnv,
  readFile: (path: string) => string,
  overrides: CredentialOverrides = {},
): McpCredentials {
  const triedPaths: string[] = [];
  const path = cliConfigPath(env, overrides.profile);
  triedPaths.push(path);
  let fileConfig: CliConfigFile | undefined;
  try {
    fileConfig = parseCliConfig(readFile(path), path);
  } catch {
    // Config absent, unreadable or unparsable: credentials may still come
    // from env. Errors never include file content — only the path above.
    fileConfig = undefined;
  }

  const envToken = env.MULTICA_TOKEN?.trim();
  const flagToken = overrides.token?.trim();
  const fileToken = fileConfig?.token?.trim();

  const token = flagToken || envToken || fileToken;
  if (!token) {
    throw new MissingCredentialsError(triedPaths);
  }
  const tokenSource: McpCredentials["tokenSource"] =
    flagToken !== undefined && flagToken !== ""
      ? "flag"
      : envToken
        ? "env"
        : "cli-config";

  const envUrl = env.MULTICA_SERVER_URL?.trim();
  const flagUrl = overrides.serverUrl?.trim();
  const fileUrl = fileConfig?.server_url?.trim();
  const rawUrl = flagUrl || envUrl || fileUrl || DEFAULT_SERVER_URL;
  const serverUrl = normalizeServerUrl(rawUrl);

  return { token, serverUrl, tokenSource };
}

function parseCliConfig(raw: string, path: string): CliConfigFile {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    throw new Error(`Multica CLI config at ${path} is not valid JSON`);
  }
  if (typeof parsed !== "object" || parsed === null) {
    throw new Error(`Multica CLI config at ${path} is not an object`);
  }
  return parsed as CliConfigFile;
}

export function normalizeServerUrl(raw: string): string {
  const trimmed = raw.trim().replace(/\/+$/, "");
  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    throw new Error(`Invalid server URL: ${trimmed}`);
  }
  if (parsed.protocol !== "https:" && parsed.protocol !== "http:") {
    throw new Error(`Server URL must be http(s): ${trimmed}`);
  }
  return parsed.toString().replace(/\/+$/, "");
}
