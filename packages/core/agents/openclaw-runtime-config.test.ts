import { describe, expect, it } from "vitest";
import {
  OPENCLAW_GATEWAY_TOKEN_MASK,
  parseOpenclawRuntimeConfig,
  serializeOpenclawRuntimeConfig,
} from "./openclaw-runtime-config";

describe("subagent override compatibility", () => {
  it("preserves legacy true and explicit false through parse and serialize", () => {
    expect(
      serializeOpenclawRuntimeConfig(
        parseOpenclawRuntimeConfig({ mode: "local", allow_subagents: true }),
      ),
    ).toEqual({ mode: "local", allow_subagents: true });
    expect(
      serializeOpenclawRuntimeConfig(
        parseOpenclawRuntimeConfig({ mode: "local", allow_subagents: false }),
      ),
    ).toEqual({ mode: "local", allow_subagents: false });
  });
});

describe("serializeOpenclawRuntimeConfig", () => {
  it("keeps the masked gateway token sentinel so the API can preserve the persisted token", () => {
    expect(
      serializeOpenclawRuntimeConfig({
        mode: "gateway",
        gateway: {
          host: "gw.internal",
          port: 18789,
          token: OPENCLAW_GATEWAY_TOKEN_MASK,
          tls: true,
        },
      }),
    ).toEqual({
      mode: "gateway",
      gateway: {
        host: "gw.internal",
        port: 18789,
        token: OPENCLAW_GATEWAY_TOKEN_MASK,
        tls: true,
      },
    });
  });

  it("omits an empty gateway token so users can clear a persisted token", () => {
    expect(
      serializeOpenclawRuntimeConfig({
        mode: "gateway",
        gateway: {
          host: "gw.internal",
          port: 18789,
        },
      }),
    ).toEqual({
      mode: "gateway",
      gateway: {
        host: "gw.internal",
        port: 18789,
      },
    });
  });

  it("passes through a real gateway token value", () => {
    expect(
      serializeOpenclawRuntimeConfig({
        mode: "gateway",
        gateway: {
          token: "rotated-secret",
        },
      }),
    ).toEqual({
      mode: "gateway",
      gateway: {
        token: "rotated-secret",
      },
    });
  });
});
