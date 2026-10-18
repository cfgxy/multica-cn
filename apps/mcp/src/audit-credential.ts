import { readFileSync, statSync } from "node:fs";

export function readAuditCredential(path: string | undefined): string {
  if (!path) {
    throw new Error(
      "MCP audit credential is not configured. Run `make mcp-install` after creating ~/.config/multica/mcp-audit-credential with mode 600.",
    );
  }

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
