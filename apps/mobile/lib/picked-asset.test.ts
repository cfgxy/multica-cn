import { describe, expect, it } from "vitest";
import {
  assetFromDocumentPicker,
  assetFromImagePicker,
  assetFromSharedFile,
  partitionOversize,
} from "./picked-asset";
import type { SharedFile } from "./share-payload";

const MB = 1024 * 1024;

describe("assetFromImagePicker", () => {
  it("maps an expo-image-picker asset to the upload payload shape", () => {
    const asset = assetFromImagePicker({
      uri: "file:///tmp/a.png",
      fileName: "a.png",
      mimeType: "image/png",
      fileSize: 12,
    });
    expect(asset).toEqual({
      uri: "file:///tmp/a.png",
      name: "a.png",
      type: "image/png",
      size: 12,
    });
  });

  it("falls back to a placeholder name when fileName is missing (Android)", () => {
    const asset = assetFromImagePicker({
      uri: "file:///tmp/b",
      fileName: null,
      mimeType: null,
      fileSize: null,
    });
    expect(asset.name).toMatch(/^image-\d+\.jpg$/);
    expect(asset.type).toBe("image/jpeg");
    expect(asset.size).toBeUndefined();
  });
});

describe("assetFromDocumentPicker", () => {
  it("maps an expo-document-picker asset to the upload payload shape", () => {
    const asset = assetFromDocumentPicker({
      uri: "file:///tmp/c.pdf",
      name: "c.pdf",
      mimeType: "application/pdf",
      size: 5,
    });
    expect(asset).toEqual({
      uri: "file:///tmp/c.pdf",
      name: "c.pdf",
      type: "application/pdf",
      size: 5,
    });
  });

  it("falls back to application/octet-stream when mimeType is missing", () => {
    const asset = assetFromDocumentPicker({
      uri: "file:///tmp/d",
      name: "d",
      mimeType: null,
      size: null,
    });
    expect(asset.type).toBe("application/octet-stream");
    expect(asset.size).toBeUndefined();
  });
});

describe("assetFromSharedFile", () => {
  // RUYI-463: 系统分享进来的文件已经过 normalizeSharePayload 清洗，
  // 这里只验证与既有上传通道入参（PickedAsset）的对齐。
  it("maps a shared file to the upload payload shape", () => {
    const shared: SharedFile = {
      uri: "file:///cache/share-intent/report.pdf",
      name: "report.pdf",
      mimeType: "application/pdf",
      size: 2048,
    };
    expect(assetFromSharedFile(shared)).toEqual({
      uri: shared.uri,
      name: "report.pdf",
      type: "application/pdf",
      size: 2048,
    });
  });

  it("keeps an unknown size as undefined so partitionOversize defers to the server", () => {
    const shared: SharedFile = {
      uri: "file:///cache/x.bin",
      name: "x.bin",
      mimeType: "application/octet-stream",
    };
    expect(assetFromSharedFile(shared).size).toBeUndefined();
  });
});

describe("partitionOversize", () => {
  const small: { uri: string; name: string; type: string; size?: number } = {
    uri: "u1",
    name: "small.png",
    type: "image/png",
    size: 1,
  };
  const big = { uri: "u2", name: "big.png", type: "image/png", size: 2 * MB };

  it("keeps assets at or under the limit and splits oversized ones", () => {
    const atLimit = { uri: "u3", name: "edge.png", type: "image/png", size: MB };
    const { ok, oversized } = partitionOversize([small, atLimit, big], MB);
    expect(ok).toEqual([small, atLimit]);
    expect(oversized).toEqual([big]);
  });

  it("never blocks assets without a known size", () => {
    const unknown = {
      uri: "u4",
      name: "mystery.bin",
      type: "application/octet-stream",
    };
    const { ok, oversized } = partitionOversize([unknown, big], MB);
    expect(ok).toEqual([unknown]);
    expect(oversized).toEqual([big]);
  });
});
