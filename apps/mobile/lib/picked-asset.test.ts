import { describe, expect, it } from "vitest";
import {
  assetFromDocumentPicker,
  assetFromImagePicker,
  partitionOversize,
} from "./picked-asset";

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
