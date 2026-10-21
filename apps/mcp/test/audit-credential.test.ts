import { afterEach, describe, expect, it } from "vitest";
import { chmodSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { AUDIT_CREDENTIAL_ENV_VAR, readAuditCredential } from "../src/audit-credential.js";

describe("readAuditCredential", () => {
  let dir: string | undefined;

  afterEach(() => {
    if (dir) rmSync(dir, { recursive: true, force: true });
  });

  function tempCredential(content: string, mode = 0o600): string {
    dir = mkdtempSync(join(tmpdir(), "mcp-audit-cred-"));
    const path = join(dir, "credential");
    writeFileSync(path, content);
    chmodSync(path, mode);
    return path;
  }

  it("prefers an explicit credential file over the shared env var", () => {
    const path = tempCredential("file-credential");
    expect(readAuditCredential(path, { [AUDIT_CREDENTIAL_ENV_VAR]: "env-credential" })).toBe(
      "file-credential",
    );
  });

  it("falls back to the shared env var when no file is requested", () => {
    expect(readAuditCredential(undefined, { [AUDIT_CREDENTIAL_ENV_VAR]: "  fixed-token  " })).toBe(
      "fixed-token",
    );
  });

  it("returns undefined when neither file nor env var is configured", () => {
    expect(readAuditCredential(undefined, {})).toBeUndefined();
  });

  it("treats a blank env var as not configured", () => {
    expect(readAuditCredential(undefined, { [AUDIT_CREDENTIAL_ENV_VAR]: "   " })).toBeUndefined();
  });

  it("reads a 600 credential file and trims surrounding whitespace", () => {
    expect(readAuditCredential(tempCredential("  file-token\n"), {})).toBe("file-token");
  });

  it("rejects a credential file readable beyond its owner", () => {
    const path = tempCredential("file-token", 0o644);
    expect(() => readAuditCredential(path, {})).toThrow(/permissions must be restricted/);
  });

  it("rejects an empty credential file", () => {
    const path = tempCredential("");
    expect(() => readAuditCredential(path, {})).toThrow(/empty/);
  });

  it("throws when the requested file does not exist", () => {
    expect(() => readAuditCredential("/definitely/not/here", {})).toThrow(/Unable to load/);
  });
});
