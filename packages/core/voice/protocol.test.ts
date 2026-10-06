import { parseVoiceServerFrame, voiceAudioInputFrame, VOICE_INPUT_MIME_TYPE } from "./protocol";

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
});
