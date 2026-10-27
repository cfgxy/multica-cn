import { readFileSync, statSync } from "node:fs";

/**
 * Env var the backend API process reads for the same service-to-service
 * token (server/cmd/server/router.go): one fixed value in the deployment
 * environment configures both sides of co-deployed stacks (self-hosted
 * compose), no credential file required.
 */
export const AUDIT_CREDENTIAL_ENV_VAR = "MULTICA_MCP_AUDIT_SERVICE_CREDENTIAL";

export function readAuditCredential(
  path: string | undefined,
  env: NodeJS.ProcessEnv = process.env,
): string | undefined {
  if (path) {
    try {
      if ((statSync(path).mode & 0o077) !== 0) {
        throw new Error("credential file permissions must be restricted to its owner (chmod 600)");
      }
      const credential = readFileSync(path, "utf8").trim();
      if (!credential) {
        throw new Error("credential file is empty");
      }
      return credential;
    } catch (error) {
      const detail = error instanceof Error ? error.message : "unreadable file";
      throw new Error(
        `Unable to load MCP audit credential from '${path}': ${detail}. Store the credential in a user-only file (chmod 600) and rerun the MCP client.`,
      );
    }
  }

  // No file requested (self-hosted compose form): fall back to the env var
  // the backend reads. Unset/blank leaves audit reporting disabled — the
  // service itself keeps running; Inbox operations fail closed.
  return env[AUDIT_CREDENTIAL_ENV_VAR]?.trim() || undefined;
}
