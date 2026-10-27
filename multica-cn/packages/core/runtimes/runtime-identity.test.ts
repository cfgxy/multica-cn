// @vitest-environment node
import { describe, expect, it } from "vitest";
import { providerSupportsMcpConfig } from "../agents/mcp-support";
import { RUNTIME_PROFILE_PROTOCOL_FAMILIES } from "../types/agent";
import { providerDisplayName } from "./display";

// DeerFlow and ZCode were previously reachable only as runtime profiles over
// the `kimi` protocol family, which made every one of their runtimes present
// itself as Kimi in the UI, the daemon log and the runtime metrics. These are
// the client-side halves of the split.
describe("independent DeerFlow and ZCode runtime identity", () => {
  it("labels each family with its own product name", () => {
    expect(providerDisplayName("deerflow")).toBe("DeerFlow");
    expect(providerDisplayName("zcode")).toBe("ZCode");
    // The family they used to be shelled onto keeps its own label.
    expect(providerDisplayName("kimi")).toBe("Kimi");
  });

  it("offers both as selectable protocol families", () => {
    expect(RUNTIME_PROFILE_PROTOCOL_FAMILIES).toContain("deerflow");
    expect(RUNTIME_PROFILE_PROTOCOL_FAMILIES).toContain("zcode");
    // Existing profiles are declared as `kimi`; dropping it would break them.
    expect(RUNTIME_PROFILE_PROTOCOL_FAMILIES).toContain("kimi");
  });

  it("does not accept the CLI command names as families", () => {
    // `deerflow-acp` / `zcode-acp` are the executables a profile launches, not
    // protocol identities. Offering them here would produce profiles the
    // server rejects with 400 and the daemon refuses to register.
    const families: readonly string[] = RUNTIME_PROFILE_PROTOCOL_FAMILIES;
    expect(families).not.toContain("deerflow-acp");
    expect(families).not.toContain("zcode-acp");
  });

  it("advertises MCP for zcode but not for deerflow", () => {
    // zcode-acp forwards mcpServers to its backend on session/new and
    // session/resume. The DeerFlow bridge rejects a non-empty mcpServers array
    // with -32602, so offering the MCP surface there would produce tasks that
    // fail at session creation.
    expect(providerSupportsMcpConfig("zcode")).toBe(true);
    expect(providerSupportsMcpConfig("deerflow")).toBe(false);
  });
});
