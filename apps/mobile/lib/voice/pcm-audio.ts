/**
 * 降级听写音频纯函数（RUYI-710 阶段 2）。voice-audio 原生模块上行的是
 * 16 kHz PCM16 单声道 base64 分片（每片 100ms），服务端转写端点只收
 * wav/webm/mp4 容器 —— 本模块在端侧把分片封回 WAV（44 字节头 + 原始
 * PCM，零拷贝级别的小对象），并把分片折算成 0..1 的 RMS 供波形条复用。
 *
 * 纯函数、零平台依赖（base64 解码手写，不依赖 Hermes/Node 的 atob 差
 * 异），vitest node 通道直接钉住字节面。
 */

const base64Alphabet =
  "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

const base64Lookup = (() => {
  const table = new Int16Array(128).fill(-1);
  for (let i = 0; i < base64Alphabet.length; i += 1) {
    table[base64Alphabet.charCodeAt(i)] = i;
  }
  return table;
})();

/** 标准 base64 → 字节。非法字符按 0 处理（上游是自家原生模块，不做容错升级）。 */
export function base64ToBytes(encoded: string): Uint8Array<ArrayBuffer> {
  const value = encoded.replace(/=+$/, "");
  const out = new Uint8Array(Math.floor((value.length * 3) / 4));
  let outAt = 0;
  let buffer = 0;
  let bits = 0;
  for (let i = 0; i < value.length; i += 1) {
    const digit = base64Lookup[value.charCodeAt(i) & 0x7f];
    if (digit < 0) continue;
    buffer = (buffer << 6) | digit;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      out[outAt] = (buffer >> bits) & 0xff;
      outAt += 1;
    }
  }
  return out.subarray(0, outAt);
}

function writeAscii(bytes: Uint8Array, at: number, text: string): void {
  for (let i = 0; i < text.length; i += 1) bytes[at + i] = text.charCodeAt(i);
}

function writeUint32(bytes: Uint8Array, at: number, value: number): void {
  bytes[at] = value & 0xff;
  bytes[at + 1] = (value >>> 8) & 0xff;
  bytes[at + 2] = (value >>> 16) & 0xff;
  bytes[at + 3] = (value >>> 24) & 0xff;
}

function writeUint16(bytes: Uint8Array, at: number, value: number): void {
  bytes[at] = value & 0xff;
  bytes[at + 1] = (value >>> 8) & 0xff;
}

/**
 * PCM16 单声道 base64 分片 → 完整 WAV 字节。分片全空时返回 null：
 * 没有任何音频就不该发起一次注定失败（或空转写）的上传。
 */
export function wavFromPcmBase64Chunks(
  chunks: readonly string[],
  sampleRate = 16000,
): Uint8Array<ArrayBuffer> | null {
  let pcmLength = 0;
  const parts = chunks.map((chunk) => {
    const bytes = base64ToBytes(chunk);
    pcmLength += bytes.byteLength;
    return bytes;
  });
  if (pcmLength === 0) return null;

  // 44 字节标准 PCM WAV 头：RIFF/WAVE + fmt(16, PCM, mono) + data。
  const wav = new Uint8Array(44 + pcmLength);
  writeAscii(wav, 0, "RIFF");
  writeUint32(wav, 4, 36 + pcmLength);
  writeAscii(wav, 8, "WAVE");
  writeAscii(wav, 12, "fmt ");
  writeUint32(wav, 16, 16);
  writeUint16(wav, 20, 1); // PCM
  writeUint16(wav, 22, 1); // 单声道
  writeUint32(wav, 24, sampleRate);
  writeUint32(wav, 28, sampleRate * 2); // 字节率 = 采样率 × 2 字节
  writeUint16(wav, 32, 2); // 块对齐
  writeUint16(wav, 34, 16); // 位深
  writeAscii(wav, 36, "data");
  writeUint32(wav, 40, pcmLength);

  let at = 44;
  for (const part of parts) {
    wav.set(part, at);
    at += part.byteLength;
  }
  return wav;
}

/** PCM16 base64 分片 → RMS 归一化 0..1（32768 满幅）。奇数字节截断半样本。 */
export function rmsFromPcmBase64(encoded: string): number {
  const bytes = base64ToBytes(encoded);
  const sampleCount = Math.floor(bytes.byteLength / 2);
  if (sampleCount === 0) return 0;
  const view = new DataView(bytes.buffer, bytes.byteOffset, sampleCount * 2);
  let sumSquares = 0;
  for (let i = 0; i < sampleCount; i += 1) {
    sumSquares += (view.getInt16(i * 2, true) / 32768) ** 2;
  }
  return Math.sqrt(sumSquares / sampleCount);
}
