import { describe, expect, it } from "vitest";
import { parseWorkspaceVoiceSettings } from "./providers";

describe("parseWorkspaceVoiceSettings", () => {
  it("returns null levels when settings is empty or the voice key is absent", () => {
    expect(parseWorkspaceVoiceSettings(undefined)).toEqual({ default: null, asr: null });
    expect(parseWorkspaceVoiceSettings(null)).toEqual({ default: null, asr: null });
    expect(parseWorkspaceVoiceSettings({ issue_prefix: "RUYI" })).toEqual({
      default: null,
      asr: null,
    });
  });

  it("tolerates a malformed voice bag without throwing", () => {
    expect(parseWorkspaceVoiceSettings({ voice: "garbage" })).toEqual({
      default: null,
      asr: null,
    });
    expect(parseWorkspaceVoiceSettings({ voice: 42 })).toEqual({ default: null, asr: null });
    expect(parseWorkspaceVoiceSettings({ voice: [] })).toEqual({ default: null, asr: null });
  });

  it("decodes well-formed refs, keeping model and advanced", () => {
    expect(
      parseWorkspaceVoiceSettings({
        voice: {
          default: { provider_id: "p1", model: "gemini-3.8-live", advanced: { temperature: 0.5 } },
          asr: { provider_id: "p2" },
        },
      }),
    ).toEqual({
      default: { provider_id: "p1", model: "gemini-3.8-live", advanced: { temperature: 0.5 } },
      asr: { provider_id: "p2", model: undefined, advanced: undefined },
    });
  });

  it("nulls a level whose ref is malformed but keeps the sibling level", () => {
    expect(
      parseWorkspaceVoiceSettings({
        voice: {
          default: { model: "no-provider-id" },
          asr: { provider_id: "p2" },
        },
      }),
    ).toEqual({
      default: null,
      asr: { provider_id: "p2", model: undefined, advanced: undefined },
    });
  });

  it("drops non-string model and array advanced instead of rejecting the ref", () => {
    expect(
      parseWorkspaceVoiceSettings({
        voice: {
          default: { provider_id: "p1", model: 7, advanced: ["nope"] },
        },
      }),
    ).toEqual({
      default: { provider_id: "p1", model: undefined, advanced: undefined },
      asr: null,
    });
  });
});
