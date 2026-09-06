import { describe, expect, it } from "vitest";

import {
  DEFAULT_SERVER_URL,
  cliConfigPath,
  MissingCredentialsError,
  normalizeServerUrl,
  resolveCredentials,
  type ConfigEnv,
} from "../src/config.js";

const MUL_TOKEN = `mul_${"a1b2c3d4e5".repeat(4)}`;

function envWith(over: Partial<ConfigEnv>): ConfigEnv {
  return { HOME: "/home/tester", ...over };
}

function readerFor(files: Record<string, string>) {
  return (path: string): string => {
    const hit = files[path];
    if (hit === undefined) {
      const err = new Error(`ENOENT: ${path}`) as NodeJS.ErrnoException;
      err.code = "ENOENT";
      throw err;
    }
    return hit;
  };
}

describe("cliConfigPath", () => {
  it("uses ~/.multica/config.json for the default profile", () => {
    expect(cliConfigPath(envWith({}))).toBe("/home/tester/.multica/config.json");
  });

  it("uses the profile directory for a named profile", () => {
    expect(cliConfigPath(envWith({ MULTICA_PROFILE: "work" }))).toBe(
      "/home/tester/.multica/profiles/work/config.json",
    );
  });

  it("roots directly under MULTICA_TASK_CONFIG_ROOT when set", () => {
    expect(
      cliConfigPath(envWith({ MULTICA_TASK_CONFIG_ROOT: "/task/cfg" })),
    ).toBe("/task/cfg/config.json");
    expect(
      cliConfigPath(
        envWith({ MULTICA_TASK_CONFIG_ROOT: "/task/cfg", MULTICA_PROFILE: "p1" }),
      ),
    ).toBe("/task/cfg/profiles/p1/config.json");
  });

  it("flag profile overrides the env profile", () => {
    expect(
      cliConfigPath(envWith({ MULTICA_PROFILE: "a" }), "b"),
    ).toBe("/home/tester/.multica/profiles/b/config.json");
  });
});

describe("resolveCredentials", () => {
  it("reads token and server URL from the CLI config file", () => {
    const creds = resolveCredentials(
      envWith({}),
      readerFor({
        "/home/tester/.multica/config.json": JSON.stringify({
          token: MUL_TOKEN,
          server_url: "https://multica.example.com",
        }),
      }),
    );
    expect(creds.token).toBe(MUL_TOKEN);
    expect(creds.serverUrl).toBe("https://multica.example.com");
    expect(creds.tokenSource).toBe("cli-config");
  });

  it("MULTICA_TOKEN env wins over the config file", () => {
    const creds = resolveCredentials(
      envWith({ MULTICA_TOKEN: MUL_TOKEN }),
      readerFor({
        "/home/tester/.multica/config.json": JSON.stringify({
          token: `mul_${"b".repeat(40)}`,
          server_url: "https://from-file.example.com",
        }),
      }),
    );
    expect(creds.token).toBe(MUL_TOKEN);
    expect(creds.tokenSource).toBe("env");
    // Server URL still falls through to the config file.
    expect(creds.serverUrl).toBe("https://from-file.example.com");
  });

  it("flag overrides beat env for token and server URL", () => {
    const creds = resolveCredentials(
      envWith({
        MULTICA_TOKEN: `mul_${"c".repeat(40)}`,
        MULTICA_SERVER_URL: "https://env.example.com/",
      }),
      () => "",
      { token: MUL_TOKEN, serverUrl: "https://flag.example.com" },
    );
    expect(creds.token).toBe(MUL_TOKEN);
    expect(creds.tokenSource).toBe("flag");
    expect(creds.serverUrl).toBe("https://flag.example.com");
  });

  it("falls back to the cloud default server URL", () => {
    const creds = resolveCredentials(
      envWith({ MULTICA_TOKEN: MUL_TOKEN }),
      () => {
        throw new Error("no file");
      },
    );
    expect(creds.serverUrl).toBe(DEFAULT_SERVER_URL);
  });

  it("reads a named profile config", () => {
    const creds = resolveCredentials(
      envWith({ MULTICA_PROFILE: "staging" }),
      readerFor({
        "/home/tester/.multica/profiles/staging/config.json": JSON.stringify({
          token: MUL_TOKEN,
        }),
      }),
    );
    expect(creds.token).toBe(MUL_TOKEN);
  });

  it("throws MissingCredentialsError naming the path when nothing resolves", () => {
    let caught: unknown;
    try {
      resolveCredentials(envWith({}), readerFor({}));
    } catch (err) {
      caught = err;
    }
    expect(caught).toBeInstanceOf(MissingCredentialsError);
    const message = (caught as Error).message;
    expect(message).toContain("/home/tester/.multica/config.json");
    expect(message).not.toContain(MUL_TOKEN);
  });

  it("ignores an unparsable config file and reports missing credentials", () => {
    expect(() =>
      resolveCredentials(
        envWith({}),
        readerFor({ "/home/tester/.multica/config.json": "{not json" }),
      ),
    ).toThrow(MissingCredentialsError);
  });
});

describe("normalizeServerUrl", () => {
  it("strips trailing slashes", () => {
    expect(normalizeServerUrl("https://api.example.com/")).toBe(
      "https://api.example.com",
    );
  });

  it("keeps an http self-hosted URL", () => {
    expect(normalizeServerUrl("http://localhost:8080")).toBe(
      "http://localhost:8080",
    );
  });

  it("rejects non-http schemes and garbage", () => {
    expect(() => normalizeServerUrl("ftp://x")).toThrow(/http\(s\)/);
    expect(() => normalizeServerUrl("not a url")).toThrow(/Invalid server URL/);
  });
});
