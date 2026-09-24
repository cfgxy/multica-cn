import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { createClientTargets } from "../src/install/clients.js";
import {
  formatInstallReport,
  formatStatusReport,
  formatUninstallReport,
  runInstall,
  runStatus,
  runUninstall,
  runUpdate,
} from "../src/install/installer.js";

const DIST_PATH = "/checkout/apps/mcp/dist/index.js";

let home: string;

beforeEach(() => {
  home = mkdtempSync(join(tmpdir(), "multica-mcp-install-"));
});

afterEach(() => {
  rmSync(home, { recursive: true, force: true });
});

function targetById(id: string) {
  const target = createClientTargets(home).find((t) => t.id === id);
  if (!target) throw new Error(`no target for ${id}`);
  return target;
}

describe("client detection", () => {
  it("skips every client when nothing is installed", () => {
    const results = runInstall(createClientTargets(home), { distPath: DIST_PATH });
    for (const { outcome } of results) {
      expect(outcome.status).toBe("skipped-not-detected");
    }
    const report = formatInstallReport(results);
    expect(report).toContain("未检测到本机安装");
  });

  it("does not create a config file for an undetected client", () => {
    const target = targetById("claude-code");
    target.apply({ distPath: DIST_PATH });
    expect(() => readFileSync(join(home, ".claude.json"), "utf8")).toThrow();
  });
});

describe("Claude Code / Kimi / Cursor (shared stdio JSON shape)", () => {
  const cases: Array<{ id: string; markerDir: string; configFile: string[] }> = [
    { id: "claude-code", markerDir: ".claude", configFile: [".claude.json"] },
    { id: "kimi", markerDir: ".kimi-code", configFile: [".kimi-code", "mcp.json"] },
    { id: "cursor", markerDir: ".cursor", configFile: [".cursor", "mcp.json"] },
  ];

  for (const { id, markerDir, configFile } of cases) {
    it(`${id}: writes a stdio multica entry and preserves other servers`, () => {
      mkdirSync(join(home, markerDir), { recursive: true });
      const configPath = join(home, ...configFile);
      mkdirSync(join(configPath, ".."), { recursive: true });
      writeFileSync(
        configPath,
        JSON.stringify({ mcpServers: { other: { command: "npx", args: ["other-mcp"] } } }, null, 2),
      );

      const outcome = targetById(id).apply({ distPath: DIST_PATH });
      expect(outcome.status).toBe("installed");

      const written = JSON.parse(readFileSync(configPath, "utf8"));
      expect(written.mcpServers.other).toEqual({ command: "npx", args: ["other-mcp"] });
      expect(written.mcpServers.multica).toEqual({ type: "stdio", command: "node", args: [DIST_PATH] });
    });

    it(`${id}: creates the config file fresh when none exists yet`, () => {
      mkdirSync(join(home, markerDir), { recursive: true });
      const configPath = join(home, ...configFile);

      const outcome = targetById(id).apply({ distPath: DIST_PATH });
      expect(outcome.status).toBe("installed");
      const written = JSON.parse(readFileSync(configPath, "utf8"));
      expect(written.mcpServers.multica).toEqual({ type: "stdio", command: "node", args: [DIST_PATH] });
    });

    it(`${id}: second run is idempotent (no duplicate entries)`, () => {
      mkdirSync(join(home, markerDir), { recursive: true });
      const target = targetById(id);
      target.apply({ distPath: DIST_PATH });
      const configPath = join(home, ...configFile);
      const firstRun = readFileSync(configPath, "utf8");

      target.apply({ distPath: DIST_PATH });
      const secondRun = readFileSync(configPath, "utf8");
      expect(secondRun).toEqual(firstRun);
      expect(Object.keys(JSON.parse(secondRun).mcpServers)).toEqual(["multica"]);
    });

    it(`${id}: leaves the original file untouched when it fails to parse`, () => {
      mkdirSync(join(home, markerDir), { recursive: true });
      const configPath = join(home, ...configFile);
      const corrupt = "{ not valid json";
      writeFileSync(configPath, corrupt);

      const outcome = targetById(id).apply({ distPath: DIST_PATH });
      expect(outcome.status).toBe("skipped-parse-error");
      expect(readFileSync(configPath, "utf8")).toBe(corrupt);
    });
  }
});

describe("ZCode (nested mcp.servers.multica)", () => {
  it("writes under mcp.servers and preserves sibling servers", () => {
    mkdirSync(join(home, ".zcode", "cli"), { recursive: true });
    const configPath = join(home, ".zcode", "cli", "config.json");
    writeFileSync(
      configPath,
      JSON.stringify({ model: "glm", mcp: { servers: { other: { type: "stdio", command: "other" } } } }),
    );

    const outcome = targetById("zcode").apply({ distPath: DIST_PATH });
    expect(outcome.status).toBe("installed");

    const written = JSON.parse(readFileSync(configPath, "utf8"));
    expect(written.model).toBe("glm");
    expect(written.mcp.servers.other).toEqual({ type: "stdio", command: "other" });
    expect(written.mcp.servers.multica).toEqual({ type: "stdio", command: "node", args: [DIST_PATH] });
  });
});

describe("OpenCode (mcp.multica, local command array)", () => {
  it("writes a local mcp entry", () => {
    mkdirSync(join(home, ".config", "opencode"), { recursive: true });
    const outcome = targetById("opencode").apply({ distPath: DIST_PATH });
    expect(outcome.status).toBe("installed");

    const configPath = join(home, ".config", "opencode", "opencode.json");
    const written = JSON.parse(readFileSync(configPath, "utf8"));
    expect(written.mcp.multica).toEqual({ type: "local", command: ["node", DIST_PATH], enabled: true });
  });
});

describe("Codex (TOML block replace)", () => {
  function configPath() {
    return join(home, ".codex", "config.toml");
  }

  it("appends a [mcp_servers.multica] table and preserves other tables", () => {
    mkdirSync(join(home, ".codex"), { recursive: true });
    const original = ['model = "gpt-5.5"', "", "[mcp_servers.other]", 'command = "npx"', 'args = ["other"]', ""].join(
      "\n",
    );
    writeFileSync(configPath(), original);

    const outcome = targetById("codex").apply({ distPath: DIST_PATH });
    expect(outcome.status).toBe("installed");

    const written = readFileSync(configPath(), "utf8");
    expect(written).toContain("[mcp_servers.other]");
    expect(written).toContain('command = "npx"');
    expect(written).toContain("[mcp_servers.multica]");
    expect(written).toContain('command = "node"');
    expect(written).toContain(`args = ["${DIST_PATH}"]`);
  });

  it("replaces an existing [mcp_servers.multica] table in place instead of duplicating it", () => {
    mkdirSync(join(home, ".codex"), { recursive: true });
    const stale = [
      "[mcp_servers.multica]",
      'command = "node"',
      'args = ["/old/path/dist/index.js"]',
      'type = "stdio"',
      "",
      "[mcp_servers.other]",
      'command = "npx"',
      "",
    ].join("\n");
    writeFileSync(configPath(), stale);

    targetById("codex").apply({ distPath: DIST_PATH });

    const written = readFileSync(configPath(), "utf8");
    expect(written.match(/\[mcp_servers\.multica\]/g)?.length).toBe(1);
    expect(written).not.toContain("/old/path/dist/index.js");
    expect(written).toContain(`args = ["${DIST_PATH}"]`);
    expect(written).toContain("[mcp_servers.other]");
  });

  it("second run is idempotent (byte-for-byte identical output)", () => {
    mkdirSync(join(home, ".codex"), { recursive: true });
    const target = targetById("codex");
    target.apply({ distPath: DIST_PATH });
    const firstRun = readFileSync(configPath(), "utf8");

    target.apply({ distPath: DIST_PATH });
    const secondRun = readFileSync(configPath(), "utf8");
    expect(secondRun).toEqual(firstRun);
    expect(secondRun.match(/\[mcp_servers\.multica\]/g)?.length).toBe(1);
  });

  it("creates the file fresh when Codex is detected but has no config yet", () => {
    mkdirSync(join(home, ".codex"), { recursive: true });
    const outcome = targetById("codex").apply({ distPath: DIST_PATH });
    expect(outcome.status).toBe("installed");
    expect(readFileSync(configPath(), "utf8")).toContain("[mcp_servers.multica]");
  });

  it("skips and leaves the file untouched when brackets are unbalanced", () => {
    mkdirSync(join(home, ".codex"), { recursive: true });
    const corrupt = "[mcp_servers.broken\ncommand = \"x\"\n";
    writeFileSync(configPath(), corrupt);

    const outcome = targetById("codex").apply({ distPath: DIST_PATH });
    expect(outcome.status).toBe("skipped-parse-error");
    expect(readFileSync(configPath(), "utf8")).toBe(corrupt);
  });
});

describe("credential hygiene", () => {
  it("never writes token/credential-shaped fields into any client's multica entry", () => {
    for (const id of ["claude-code", "kimi", "zcode", "cursor", "opencode"]) {
      const markerDirs: Record<string, string[]> = {
        "claude-code": [".claude"],
        kimi: [".kimi-code"],
        zcode: [".zcode", "cli"],
        cursor: [".cursor"],
        opencode: [".config", "opencode"],
      };
      mkdirSync(join(home, ...markerDirs[id]!), { recursive: true });
    }
    mkdirSync(join(home, ".codex"), { recursive: true });

    const results = runInstall(createClientTargets(home), { distPath: DIST_PATH });
    for (const { outcome } of results) {
      expect(outcome.status).toBe("installed");
    }

    const dump = [
      readFileSync(join(home, ".claude.json"), "utf8"),
      readFileSync(join(home, ".kimi-code", "mcp.json"), "utf8"),
      readFileSync(join(home, ".zcode", "cli", "config.json"), "utf8"),
      readFileSync(join(home, ".cursor", "mcp.json"), "utf8"),
      readFileSync(join(home, ".config", "opencode", "opencode.json"), "utf8"),
      readFileSync(join(home, ".codex", "config.toml"), "utf8"),
    ].join("\n");

    expect(dump.toLowerCase()).not.toMatch(/mul_[a-z0-9]/);
    expect(dump.toLowerCase()).not.toContain("token");
    expect(dump.toLowerCase()).not.toContain("password");
  });
});

describe("status (read-only scan)", () => {
  it("reports not-detected for an undetected client", () => {
    const target = targetById("claude-code");
    expect(target.read()).toEqual({ status: "not-detected" });
  });

  it("reports not-installed for a detected client with no multica entry", () => {
    mkdirSync(join(home, ".claude"), { recursive: true });
    expect(targetById("claude-code").read()).toEqual({ status: "not-installed" });
  });

  it("reports registered + stale:false when the recorded distPath exists on disk", () => {
    mkdirSync(join(home, ".claude"), { recursive: true });
    const realDist = join(home, "real-dist.js");
    writeFileSync(realDist, "// fake build output\n");
    targetById("claude-code").apply({ distPath: realDist });

    expect(targetById("claude-code").read()).toEqual({
      status: "registered",
      path: join(home, ".claude.json"),
      distPath: realDist,
      stale: false,
    });
  });

  it("reports registered + stale:true when the recorded distPath no longer exists (moved/removed checkout)", () => {
    mkdirSync(join(home, ".claude"), { recursive: true });
    targetById("claude-code").apply({ distPath: DIST_PATH });

    const outcome = targetById("claude-code").read();
    expect(outcome).toEqual({
      status: "registered",
      path: join(home, ".claude.json"),
      distPath: DIST_PATH,
      stale: true,
    });
  });

  it("reports parse-error without throwing, for a corrupt config", () => {
    mkdirSync(join(home, ".claude"), { recursive: true });
    writeFileSync(join(home, ".claude.json"), "{ not valid json");
    expect(targetById("claude-code").read()).toEqual({
      status: "parse-error",
      path: join(home, ".claude.json"),
      reason: expect.stringContaining("JSON 解析失败"),
    });
  });

  it("formatStatusReport flags a stale entry", () => {
    mkdirSync(join(home, ".claude"), { recursive: true });
    targetById("claude-code").apply({ distPath: DIST_PATH });
    const report = formatStatusReport(runStatus([targetById("claude-code")]));
    expect(report).toContain("失效");
    expect(report).toContain(DIST_PATH);
  });

  describe("Codex", () => {
    function configPath() {
      return join(home, ".codex", "config.toml");
    }

    it("reports not-installed when Codex is detected but has no multica table", () => {
      mkdirSync(join(home, ".codex"), { recursive: true });
      expect(targetById("codex").read()).toEqual({ status: "not-installed" });
    });

    it("reports registered + the distPath parsed back out of the table", () => {
      mkdirSync(join(home, ".codex"), { recursive: true });
      targetById("codex").apply({ distPath: DIST_PATH });
      expect(targetById("codex").read()).toEqual({
        status: "registered",
        path: configPath(),
        distPath: DIST_PATH,
        stale: true,
      });
    });
  });
});

describe("uninstall (remove the multica entry only)", () => {
  it("skips an undetected client without creating a config file", () => {
    const outcome = targetById("claude-code").remove();
    expect(outcome).toEqual({ status: "skipped-not-detected" });
  });

  it("reports not-installed and writes nothing when no multica entry exists", () => {
    mkdirSync(join(home, ".claude"), { recursive: true });
    const configPath = join(home, ".claude.json");
    writeFileSync(configPath, JSON.stringify({ mcpServers: { other: { command: "npx" } } }, null, 2));
    const before = readFileSync(configPath, "utf8");

    const outcome = targetById("claude-code").remove();

    expect(outcome).toEqual({ status: "not-installed" });
    expect(readFileSync(configPath, "utf8")).toBe(before);
  });

  it("removes only the multica entry and preserves every other entry", () => {
    mkdirSync(join(home, ".claude"), { recursive: true });
    const configPath = join(home, ".claude.json");
    writeFileSync(
      configPath,
      JSON.stringify({ mcpServers: { other: { command: "npx", args: ["other-mcp"] } } }, null, 2),
    );
    targetById("claude-code").apply({ distPath: DIST_PATH });

    const outcome = targetById("claude-code").remove();

    expect(outcome).toEqual({ status: "removed", path: configPath });
    const written = JSON.parse(readFileSync(configPath, "utf8"));
    expect(written.mcpServers.other).toEqual({ command: "npx", args: ["other-mcp"] });
    expect(written.mcpServers.multica).toBeUndefined();
  });

  it("leaves the file untouched when it fails to parse, instead of deleting or rewriting it", () => {
    mkdirSync(join(home, ".claude"), { recursive: true });
    const configPath = join(home, ".claude.json");
    const corrupt = "{ not valid json";
    writeFileSync(configPath, corrupt);

    const outcome = targetById("claude-code").remove();

    expect(outcome).toEqual({ status: "skipped-parse-error", path: configPath, reason: expect.any(String) });
    expect(readFileSync(configPath, "utf8")).toBe(corrupt);
  });

  describe("Codex", () => {
    function configPath() {
      return join(home, ".codex", "config.toml");
    }

    it("removes the [mcp_servers.multica] table and preserves other tables", () => {
      mkdirSync(join(home, ".codex"), { recursive: true });
      const original = [
        'model = "gpt-5.5"',
        "",
        "[mcp_servers.other]",
        'command = "npx"',
        'args = ["other"]',
        "",
      ].join("\n");
      writeFileSync(configPath(), original);
      targetById("codex").apply({ distPath: DIST_PATH });

      const outcome = targetById("codex").remove();

      expect(outcome).toEqual({ status: "removed", path: configPath() });
      const written = readFileSync(configPath(), "utf8");
      expect(written).not.toContain("[mcp_servers.multica]");
      expect(written).toContain("[mcp_servers.other]");
      expect(written).toContain('command = "npx"');
    });

    it("leaves the file untouched when brackets are unbalanced", () => {
      mkdirSync(join(home, ".codex"), { recursive: true });
      const corrupt = "[mcp_servers.broken\ncommand = \"x\"\n";
      writeFileSync(configPath(), corrupt);

      const outcome = targetById("codex").remove();

      expect(outcome).toEqual({ status: "skipped-parse-error", path: configPath(), reason: expect.any(String) });
      expect(readFileSync(configPath(), "utf8")).toBe(corrupt);
    });
  });
});

describe("update (refresh only already-registered clients)", () => {
  it("refreshes a client that already has a multica entry to a new distPath", () => {
    mkdirSync(join(home, ".claude"), { recursive: true });
    targetById("claude-code").apply({ distPath: "/old/checkout/dist/index.js" });

    const newDistPath = "/new/checkout/dist/index.js";
    const results = runUpdate([targetById("claude-code")], { distPath: newDistPath });

    expect(results[0]!.outcome).toEqual({ status: "installed", path: join(home, ".claude.json") });
    const written = JSON.parse(readFileSync(join(home, ".claude.json"), "utf8"));
    expect(written.mcpServers.multica.args).toEqual([newDistPath]);
  });

  it("does not register a client that was only detected, never installed", () => {
    mkdirSync(join(home, ".claude"), { recursive: true });

    const results = runUpdate([targetById("claude-code")], { distPath: DIST_PATH });

    expect(results[0]!.outcome).toEqual({ status: "skipped-not-registered" });
    expect(() => readFileSync(join(home, ".claude.json"), "utf8")).toThrow();
  });

  it("formatInstallReport renders the skipped-not-registered case", () => {
    mkdirSync(join(home, ".claude"), { recursive: true });
    const report = formatInstallReport(runUpdate([targetById("claude-code")], { distPath: DIST_PATH }));
    expect(report).toContain("尚未注册");
  });
});

describe("uninstall report formatting", () => {
  it("formatUninstallReport renders each outcome kind", () => {
    mkdirSync(join(home, ".claude"), { recursive: true });
    const configPath = join(home, ".claude.json");
    writeFileSync(configPath, JSON.stringify({ mcpServers: { multica: { command: "node" } } }, null, 2));

    const report = formatUninstallReport(runUninstall([targetById("claude-code"), targetById("codex")]));
    expect(report).toContain("已移除");
    expect(report).toContain("未检测到本机安装");
  });
});
