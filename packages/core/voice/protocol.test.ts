import { composeVoiceSetupFrame, parseVoiceServerFrame, voiceAudioInputFrame, VOICE_INPUT_MIME_TYPE } from "./protocol";

describe("voiceAudioInputFrame", () => {
  it("uses the demo-verified realtimeInput.audio shape", () => {
    expect(voiceAudioInputFrame("QUJD")).toBe(
      `{"realtimeInput":{"audio":{"data":"QUJD","mimeType":"${VOICE_INPUT_MIME_TYPE}"}}}`,
    );
  });
});

describe("parseVoiceServerFrame", () => {
  it("parses setupComplete", () => {
    expect(parseVoiceServerFrame(`{"setupComplete":{}}`)).toMatchObject({
      setupComplete: true,
    });
  });

  it("parses model audio parts", () => {
    const frame = parseVoiceServerFrame(
      `{"serverContent":{"modelTurn":{"parts":[{"inlineData":{"mimeType":"audio/pcm;rate=24000","data":"QUJD"}}]}}}`,
    );
    expect(frame?.audioParts).toEqual([
      { mimeType: "audio/pcm;rate=24000", data: "QUJD" },
    ]);
  });

  it("parses transcription deltas and turn completion", () => {
    const frame = parseVoiceServerFrame(
      `{"serverContent":{"inputTranscription":{"text":"你好"},"outputTranscription":{"text":"hi"},"turnComplete":true}}`,
    );
    expect(frame).toMatchObject({
      inputTranscription: "你好",
      outputTranscription: "hi",
      turnComplete: true,
    });
  });

  it("parses interrupted and goAway", () => {
    const frame = parseVoiceServerFrame(`{"serverContent":{"interrupted":true}}`);
    expect(frame?.interrupted).toBe(true);
    expect(parseVoiceServerFrame(`{"goAway":{}}`)?.goAway).toBe(true);
  });

  it("parses the gateway rejection frame shape (string error + code)", () => {
    const frame = parseVoiceServerFrame(
      `{"error":"no voice runtime is bound","code":"VOICE_UNAVAILABLE:no_voice_runtime"}`,
    );
    expect(frame).toMatchObject({
      errorMessage: "no voice runtime is bound",
      errorCode: "VOICE_UNAVAILABLE:no_voice_runtime",
    });
  });

  it("parses the provider error object shape", () => {
    const frame = parseVoiceServerFrame(`{"error":{"code":500,"message":"boom"}}`);
    expect(frame?.errorMessage).toBe("boom");
    expect(frame?.errorCode).toBe("");
  });

  it("returns null for non-JSON and non-object frames", () => {
    expect(parseVoiceServerFrame("not json")).toBeNull();
    expect(parseVoiceServerFrame(`[1,2]`)).toBeNull();
    expect(parseVoiceServerFrame(`"str"`)).toBeNull();
  });

  it("parses the resumption handle update (RUYI-626 direct mode)", () => {
    const frame = parseVoiceServerFrame(
      `{"sessionResumptionUpdate":{"newHandle":"handle-abc","resumable":true}}`,
    );
    expect(frame?.resumptionHandle).toBe("handle-abc");
  });

  it("leaves resumptionHandle empty when the frame carries none", () => {
    expect(
      parseVoiceServerFrame(`{"sessionResumptionUpdate":{}}`)?.resumptionHandle,
    ).toBe("");
    expect(parseVoiceServerFrame(`{"setupComplete":{}}`)?.resumptionHandle).toBe("");
  });
});

describe("composeVoiceSetupFrame", () => {
  it("mirrors the gateway's demo-aligned setup shape", () => {
    const frame = composeVoiceSetupFrame("be terse", "gemini-3.8-live", {});
    expect(frame).toEqual({
      setup: {
        model: "models/gemini-3.8-live",
        generationConfig: { responseModalities: ["AUDIO"] },
        inputAudioTranscription: {},
        outputAudioTranscription: {},
        sessionResumption: {},
        contextWindowCompression: { slidingWindow: {} },
        realtimeInputConfig: {
          automaticActivityDetection: {
            disabled: false,
            startOfSpeechSensitivity: "START_SENSITIVITY_LOW",
          },
        },
        systemInstruction: { parts: [{ text: "be terse" }] },
      },
    });
  });

  it("omits systemInstruction when instructions are blank", () => {
    const frame = composeVoiceSetupFrame("   ", "gemini-3.8-live", {});
    expect(frame.setup.systemInstruction).toBeUndefined();
  });

  it("merges advanced params but never lets them shadow reserved keys", () => {
    const frame = composeVoiceSetupFrame("", "gemini-3.8-live", {
      temperature: 0.5,
      model: "models/rogue",
      generationConfig: { responseModalities: ["TEXT"] },
      inputAudioTranscription: { hacked: true },
    });
    expect(frame.setup.temperature).toEqual(0.5);
    expect(frame.setup.model).toBe("models/gemini-3.8-live");
    expect(frame.setup.generationConfig).toEqual({ responseModalities: ["AUDIO"] });
    expect(frame.setup.inputAudioTranscription).toEqual({});
  });

  it("stringifies to one JSON line with deterministic reserved-key shape", () => {
    const frame = composeVoiceSetupFrame("", "m1", { speechConfig: { voice: "aoede" } });
    const parsed = JSON.parse(JSON.stringify(frame));
    expect(parsed.setup.speechConfig).toEqual({ voice: "aoede" });
    expect(parsed.setup.model).toBe("models/m1");
  });
});
