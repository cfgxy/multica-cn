import { describe, expect, it } from "vitest";
import {
  MAX_SHARE_FILES,
  MAX_SHARED_NAME_LENGTH,
  normalizeSharePayload,
  type RawShareFile,
} from "./share-payload";

/**
 * Native module (modules/share-intent) hands over already-copied cache
 * files. The parser is the trust boundary between the OS share pipeline
 * and the app: everything downstream (landing page, attachment zone)
 * assumes well-formed file:// URIs and display-safe names.
 */

function rawFile(overrides: Partial<RawShareFile> = {}): RawShareFile {
  return {
    uri: "file:///data/cache/share-intent/report.pdf",
    name: "report.pdf",
    mimeType: "application/pdf",
    size: 1234,
    ...overrides,
  };
}

describe("normalizeSharePayload", () => {
  it("透传规范的单文件负载", () => {
    const out = normalizeSharePayload({ files: [rawFile()] });
    expect(out).not.toBeNull();
    expect(out?.files).toHaveLength(1);
    expect(out?.files[0]).toEqual({
      uri: "file:///data/cache/share-intent/report.pdf",
      name: "report.pdf",
      mimeType: "application/pdf",
      size: 1234,
    });
  });

  it("保留多文件分享的顺序（ACTION_SEND_MULTIPLE）", () => {
    const out = normalizeSharePayload({
      files: [rawFile({ name: "a.png", mimeType: "image/png" }), rawFile({ name: "b.pdf" })],
    });
    expect(out?.files.map((f) => f.name)).toEqual(["a.png", "b.pdf"]);
  });

  it("丢弃非 file:// 的条目而不是整体失败", () => {
    const out = normalizeSharePayload({
      files: [
        rawFile({ uri: "content://media/external/images/1" }),
        rawFile({ name: "keep.png" }),
      ],
    });
    expect(out?.files).toHaveLength(1);
    expect(out?.files[0].name).toBe("keep.png");
  });

  it("丢弃缺 name 或缺 uri 的条目", () => {
    const out = normalizeSharePayload({
      files: [
        rawFile({ name: undefined as unknown as string }),
        rawFile({ uri: undefined as unknown as string }),
        rawFile({ name: "ok.txt" }),
      ],
    });
    expect(out?.files.map((f) => f.name)).toEqual(["ok.txt"]);
  });

  it("缺 mimeType 时回退 application/octet-stream，缺 size 时字段可省", () => {
    const out = normalizeSharePayload({
      files: [{ uri: "file:///cache/x.bin", name: "x.bin" }],
    });
    expect(out?.files[0].mimeType).toBe("application/octet-stream");
    expect(out?.files[0].size).toBeUndefined();
  });

  it("负数或非数值 size 视为未知并置空", () => {
    const out = normalizeSharePayload({
      files: [rawFile({ size: -5 }), rawFile({ size: "big" as unknown as number })],
    });
    expect(out?.files[0].size).toBeUndefined();
    expect(out?.files[1].size).toBeUndefined();
  });

  it("清洗文件名中的路径分隔符与控制字符（防路径穿越）", () => {
    const out = normalizeSharePayload({
      files: [rawFile({ name: "../../etc/passwd" })],
    });
    expect(out?.files[0].name).not.toContain("/");
    expect(out?.files[0].name).toBe(".._.._etc_passwd");
  });

  it("文件名超长时截断但保留扩展名", () => {
    const longName = `${"a".repeat(MAX_SHARED_NAME_LENGTH + 50)}.pdf`;
    const out = normalizeSharePayload({ files: [rawFile({ name: longName })] });
    expect(out?.files[0].name.length).toBeLessThanOrEqual(MAX_SHARED_NAME_LENGTH);
    expect(out?.files[0].name.endsWith(".pdf")).toBe(true);
  });

  it("超过上限时只保留前 MAX_SHARE_FILES 个", () => {
    const many = Array.from({ length: MAX_SHARE_FILES + 5 }, (_, i) =>
      rawFile({ name: `f${i}.txt` }),
    );
    const out = normalizeSharePayload({ files: many });
    expect(out?.files).toHaveLength(MAX_SHARE_FILES);
    expect(out?.files[MAX_SHARE_FILES - 1].name).toBe(`f${MAX_SHARE_FILES - 1}.txt`);
  });

  it("空 files 数组返回 null（无负载则不进落地页）", () => {
    expect(normalizeSharePayload({ files: [] })).toBeNull();
  });

  it("非对象、缺 files、files 非数组一律返回 null", () => {
    expect(normalizeSharePayload(null)).toBeNull();
    expect(normalizeSharePayload("share")).toBeNull();
    expect(normalizeSharePayload({})).toBeNull();
    expect(normalizeSharePayload({ files: "nope" })).toBeNull();
  });

  it("条目为非对象时跳过", () => {
    const out = normalizeSharePayload({ files: ["file:///a", null, rawFile()] });
    expect(out?.files).toHaveLength(1);
  });
});
