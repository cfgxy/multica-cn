/**
 * BidiGenerateContent wire frames, client side (RUYI-449).
 *
 * Shapes mirror the demo-verified wire format (live-llm LiveProtocol.kt,
 * cross-checked against @google/genai 2.24.0): upstream
 * `{"realtimeInput":{"audio":{"data","mimeType"}}}`, downstream
 * `setupComplete` / `serverContent{modelTurn,interrupted,turnComplete,
 * inputTranscription,outputTranscription}` / `goAway` / `error`.
 *
 * Gateway mode composes the setup frame server-side and swallows any client
 * setup, so that path never builds one. Direct mode (RUYI-626) dials the
 * provider from the client, so it builds the setup with
 * `composeVoiceSetupFrame` — a line-for-line port of the gateway's Go
 * composition (reserved keys, VAD block placement, transcription taps), kept
 * in lockstep by mirrored unit tests on both sides. Pure string in / object
 * out — no DOM, no WebSocket — so every surface shares it.
 */

/** 16 kHz mono PCM16, the capture format both transports feed in. */
export const VOICE_INPUT_MIME_TYPE = "audio/pcm;rate=16000";

/** One downstream audio blob (24 kHz PCM16 chunks from the model). */
export interface VoiceServerAudioPart {
  mimeType: string;
  data: string;
}

export interface VoiceServerFrame {
  setupComplete: boolean;
  interrupted: boolean;
  turnComplete: boolean;
  goAway: boolean;
  audioParts: VoiceServerAudioPart[];
  /** Delta segments of the user's speech; "" when the frame has none. */
  inputTranscription: string;
  /** Delta segments of the model's speech; "" when the frame has none. */
  outputTranscription: string;
  /** Provider/gateway error text; "" when the frame is not an error. */
  errorMessage: string;
  /** The gateway's stable rejection code; "" on provider-relayed errors. */
  errorCode: string;
  /** Fresh session-resumption handle; "" when the frame carries none. */
  resumptionHandle: string;
}

export function voiceAudioInputFrame(pcmBase64: string): string {
  return JSON.stringify({
    realtimeInput: {
      audio: { data: pcmBase64, mimeType: VOICE_INPUT_MIME_TYPE },
    },
  });
}

/**
 * Setup fields the composing side owns (gateway in relay mode, client in
 * direct mode): an instance's advanced JSON may add provider options around
 * them but never override them. Mirrors voice_gateway.go's reservedSetupKeys.
 */
const reservedSetupKeys: ReadonlySet<string> = new Set([
  "model",
  "systemInstruction",
  "generationConfig",
  "inputAudioTranscription",
  "outputAudioTranscription",
  "realtimeInputConfig",
  "sessionResumption",
  "contextWindowCompression",
]);

/**
 * Builds the BidiGenerateContent setup the direct client sends the provider:
 * the gateway's demo-aligned shape (VAD under realtimeInputConfig —
 * setup-root placement is rejected by the service) plus sessionResumption,
 * contextWindowCompression, and both transcription taps that feed the
 * transcript write-back.
 */
export function composeVoiceSetupFrame(
  instructions: string,
  model: string,
  advanced: Record<string, unknown>,
): { setup: Record<string, unknown> } {
  const setup: Record<string, unknown> = {
    model: `models/${model}`,
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
  };
  if (instructions.trim() !== "") {
    setup.systemInstruction = { parts: [{ text: instructions }] };
  }
  for (const [key, value] of Object.entries(advanced)) {
    if (reservedSetupKeys.has(key)) continue;
    setup[key] = value;
  }
  return { setup };
}

const emptyFrame: VoiceServerFrame = {
  setupComplete: false,
  interrupted: false,
  turnComplete: false,
  goAway: false,
  audioParts: [],
  inputTranscription: "",
  outputTranscription: "",
  errorMessage: "",
  errorCode: "",
  resumptionHandle: "",
};

/**
 * Parses one downstream gateway frame. Returns null for anything that is not
 * a JSON object — out-of-protocol bytes degrade to a no-op instead of
 * throwing out of a websocket callback (MUL-3418's lesson).
 */
export function parseVoiceServerFrame(raw: string): VoiceServerFrame | null {
  let msg: unknown;
  try {
    msg = JSON.parse(raw);
  } catch {
    return null;
  }
  if (!msg || typeof msg !== "object" || Array.isArray(msg)) return null;
  const root = msg as Record<string, unknown>;
  const frame: VoiceServerFrame = { ...emptyFrame, audioParts: [] };

  if (root.setupComplete !== undefined) frame.setupComplete = true;

  const sc = root.serverContent as Record<string, unknown> | undefined;
  if (sc && typeof sc === "object") {
    if (sc.interrupted === true) frame.interrupted = true;
    if (sc.turnComplete === true) frame.turnComplete = true;
    const input = sc.inputTranscription as { text?: unknown } | undefined;
    if (input && typeof input.text === "string") frame.inputTranscription = input.text;
    const output = sc.outputTranscription as { text?: unknown } | undefined;
    if (output && typeof output.text === "string") frame.outputTranscription = output.text;
    const modelTurn = sc.modelTurn as { parts?: unknown } | undefined;
    const parts = modelTurn?.parts;
    if (Array.isArray(parts)) {
      for (const part of parts) {
        if (!part || typeof part !== "object") continue;
        const blob = (part as Record<string, unknown>).inlineData as
          | { data?: unknown; mimeType?: unknown }
          | undefined;
        if (blob && typeof blob.data === "string" && blob.data) {
          frame.audioParts.push({
            mimeType: typeof blob.mimeType === "string" ? blob.mimeType : "",
            data: blob.data,
          });
        }
      }
    }
  }

  if (root.goAway !== undefined) frame.goAway = true;

  const resumptionUpdate = root.sessionResumptionUpdate as
    | { newHandle?: unknown }
    | undefined;
  if (
    resumptionUpdate &&
    typeof resumptionUpdate === "object" &&
    typeof resumptionUpdate.newHandle === "string"
  ) {
    frame.resumptionHandle = resumptionUpdate.newHandle;
  }

  // The gateway's rejection frames use the HTTP shape {"error": <text>,
  // "code": <stable code>}; provider-relayed errors use {"error": {message}}.
  // Accept both so degrade mapping works on either surface.
  if (typeof root.error === "string") frame.errorMessage = root.error;
  else if (root.error && typeof root.error === "object") {
    const message = (root.error as { message?: unknown }).message;
    if (typeof message === "string") frame.errorMessage = message;
  }
  if (typeof root.code === "string") frame.errorCode = root.code;

  return frame;
}
