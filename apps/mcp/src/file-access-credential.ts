import { readFileSync, statSync } from "node:fs";

export function readFileAccessCredential(path: string | undefined): string | undefined {
  if (!path) return undefined;
  const mode = statSync(path).mode & 0o777;
  if ((mode & 0o077) !== 0) {
    throw new Error("MCP file access credential permissions must be restricted to its owner");
  }
  const credential = readFileSync(path, "utf8").trim();
  if (!credential) throw new Error("MCP file access credential file is empty");
  return credential;
}
