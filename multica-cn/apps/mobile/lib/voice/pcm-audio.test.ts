// @vitest-environment node
/**
 * RUYI-710 阶段 2 — 降级听写音频纯函数测试。
 * 钉住：PCM16 单声道 16kHz base64 分片 → WAV 容器封装（RIFF/fmt/data
 * 字段逐字节可核对）、空分片不上传（返回 null）、RMS 归一化。
 */
import { describe, expect, it } from "vitest";
import {
  base64ToBytes,
  rmsFromPcmBase64,
  wavFromPcmBase64Chunks,
} from "./pcm-audio";

describe("base64ToBytes", () => {
  it("解码标准向量（含 -_ 变体不做要求，仅标准字母表）", () => {
    // "AQID" = [1,2,3]；"AA==" = [0]；"//8=" = [255,255]
    expect(Array.from(base64ToBytes("AQID"))).toEqual([1, 2, 3]);
    expect(Array.from(base64ToBytes("AA=="))).toEqual([0]);
    expect(Array.from(base64ToBytes("//8="))).toEqual([255, 255]);
    expect(base64ToBytes("").byteLength).toBe(0);
  });
});

describe("wavFromPcmBase64Chunks", () => {
  it("空分片返回 null（不产生无音频的上传）", () => {
    expect(wavFromPcmBase64Chunks([])).toBeNull();
    expect(wavFromPcmBase64Chunks(["", ""])).toBeNull();
  });

  it("单分片：44 字节头 + 原始 PCM，字段按小端核对", () => {
    const wav = wavFromPcmBase64Chunks(["AQIDBA=="]);
    expect(wav).not.toBeNull();
    expect(wav!.byteLength).toBe(44 + 4);

    const view = new DataView(wav!.buffer, wav!.byteOffset, wav!.byteLength);
    const magic = (off: number, len: number) =>
      String.fromCharCode(...Array.from(wav!.subarray(off, off + len)));
    expect(magic(0, 4)).toBe("RIFF");
    expect(view.getUint32(4, true)).toBe(36 + 4); // 文件大小 - 8
    expect(magic(8, 4)).toBe("WAVE");
    expect(magic(12, 4)).toBe("fmt ");
    expect(view.getUint32(16, true)).toBe(16); // fmt 块长
    expect(view.getUint16(20, true)).toBe(1); // PCM
    expect(view.getUint16(22, true)).toBe(1); // 单声道
    expect(view.getUint32(24, true)).toBe(16000); // 采样率
    expect(view.getUint32(28, true)).toBe(32000); // 字节率 = 16000*2
    expect(view.getUint16(32, true)).toBe(2); // 块对齐
    expect(view.getUint16(34, true)).toBe(16); // 位深
    expect(magic(36, 4)).toBe("data");
    expect(view.getUint32(40, true)).toBe(4); // data 块长
    expect(Array.from(wav!.subarray(44))).toEqual([1, 2, 3, 4]);
  });

  it("多分片按序拼接，data 长度为分片字节和", () => {
    const wav = wavFromPcmBase64Chunks(["AQIDBA==", "//8=", "AA=="]);
    expect(wav).not.toBeNull();
    expect(wav!.byteLength).toBe(44 + 4 + 2 + 1);
    expect(Array.from(wav!.subarray(44))).toEqual([1, 2, 3, 4, 255, 255, 0]);
    const view = new DataView(wav!.buffer, wav!.byteOffset, wav!.byteLength);
    expect(view.getUint32(4, true)).toBe(36 + 7);
    expect(view.getUint32(40, true)).toBe(7);
  });

  it("采样率可指定且影响字节率字段", () => {
    const wav = wavFromPcmBase64Chunks(["AA=="], 8000);
    const view = new DataView(wav!.buffer, wav!.byteOffset, wav!.byteLength);
    expect(view.getUint32(24, true)).toBe(8000);
    expect(view.getUint32(28, true)).toBe(16000);
  });
});

describe("rmsFromPcmBase64", () => {
  it("静音为 0，空分片为 0", () => {
    // 3200 个 0 字节 = 1600 个 0 采样
    expect(rmsFromPcmBase64("A".repeat(4264) + "AAA=")).toBe(0);
    expect(rmsFromPcmBase64("")).toBe(0);
  });

  it("恒定幅度按 32768 归一化（16384 → 0.5）", () => {
    // int16 小端 16384 = 0x4000 → 字节 [0x00, 0x40] 重复
    const samples = Array.from({ length: 100 }, () => [0x00, 0x40]).flat();
    const b64 = Buffer.from(new Uint8Array(samples)).toString("base64");
    expect(rmsFromPcmBase64(b64)).toBeCloseTo(0.5, 5);
  });

  it("奇数字节按完整样本截断，不读半样本", () => {
    // [0x01, 0x00, 0xFF] → 只有 1 个完整样本 = 1 → 1/32768
    const b64 = Buffer.from(new Uint8Array([0x01, 0x00, 0xff])).toString("base64");
    expect(rmsFromPcmBase64(b64)).toBeCloseTo(1 / 32768, 8);
  });
});
