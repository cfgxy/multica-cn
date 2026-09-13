/**
 * Pure-function tests for the mobile attachment URL resolver. We exercise
 * the with-base form because `resolveAttachmentUrl` reads the active API
 * base from `server-store` at call time (RUYI-4) — that store owns
 * AsyncStorage + build-time env and is out of scope for this node-env
 * suite. The with-base helper is the same code path with the API base
 * passed in explicitly; the store is mocked below so the bound form's
 * pass-through contract stays covered.
 *
 * Coverage target: every branch the call sites in the app rely on —
 *   - `comment-attachment-list.tsx`         → file chip Linking.openURL
 *   - `markdown-image.tsx`                  → mc:// + RN image loader
 *   - `attachment-zone.tsx`                 → completed non-image chip
 *                                             tap → Linking.openURL
 */
import { describe, expect, it, vi } from "vitest";

// server-store 在模块加载期从构建期 env 合成内置默认项,并 import
// AsyncStorage 原生模块 —— 两者在 node 环境里都不可用。这里只需要
// getApiUrl 的返回值。
vi.mock("@/data/server-store", () => ({
  getApiUrl: () => "https://api.example.test",
}));

import {
  pickAttachmentImageUrl,
  resolveAttachmentUrl,
  resolveAttachmentUrlWithBase,
} from "./attachment-url";

describe("resolveAttachmentUrlWithBase", () => {
  const BASE = "https://api.example.test";

  it("prepends the API base for a server-relative path", () => {
    expect(
      resolveAttachmentUrlWithBase("/api/attachments/3fa85f64-5717-4562-b3fc-2c963f66afa6/download", BASE),
    ).toBe("https://api.example.test/api/attachments/3fa85f64-5717-4562-b3fc-2c963f66afa6/download");
  });

  it("trims a trailing slash on the API base before joining", () => {
    expect(
      resolveAttachmentUrlWithBase(
        "/api/attachments/3fa85f64-5717-4562-b3fc-2c963f66afa6/download",
        "https://api.example.test/",
      ),
    ).toBe("https://api.example.test/api/attachments/3fa85f64-5717-4562-b3fc-2c963f66afa6/download");
  });

  it("passes an absolute https URL through unchanged (CloudFront / presigned)", () => {
    const signed =
      "https://cdn.example.test/3fa85f64-5717-4562-b3fc-2c963f66afa6.bin?Policy=p&Signature=s&Key-Pair-Id=k";
    expect(resolveAttachmentUrlWithBase(signed, BASE)).toBe(signed);
  });

  it("passes an absolute http URL through unchanged (self-hosted dev)", () => {
    expect(
      resolveAttachmentUrlWithBase("http://localhost:8080/file.bin", BASE),
    ).toBe("http://localhost:8080/file.bin");
  });

  it("returns null for nullish or empty input", () => {
    expect(resolveAttachmentUrlWithBase(null, BASE)).toBeNull();
    expect(resolveAttachmentUrlWithBase(undefined, BASE)).toBeNull();
    expect(resolveAttachmentUrlWithBase("", BASE)).toBeNull();
  });

  it("keeps a relative path unchanged when the base is empty (web same-origin convention)", () => {
    // Mirrors `packages/core/workspace/avatar-url.ts` semantics for the
    // empty-base case — the host platform resolves the path against its
    // own document/page origin. RN doesn't have one, but exercising this
    // branch keeps the contract explicit.
    expect(
      resolveAttachmentUrlWithBase("/api/attachments/3fa85f64-5717-4562-b3fc-2c963f66afa6/download", ""),
    ).toBe("/api/attachments/3fa85f64-5717-4562-b3fc-2c963f66afa6/download");
  });
});

describe("composer file chip — completed non-image attachment", () => {
  // MUL-2976 (PR #3747 follow-up): when `api.uploadFile(...)` finishes on
  // a non-CloudFront deployment the returned `attachment.download_url` is
  // a server-relative path. `attachment-zone.tsx` taps that value
  // straight into `Linking.openURL` — and iOS rejects relative URLs with
  // "Cannot open URL". The fix wraps the value with `resolveAttachmentUrl`
  // before handing it to Linking; this test pins the behaviour we rely on.
  const BASE = "https://api.example.test";
  // Mirrors `AttachmentZoneItem` after a successful non-image upload.
  const completedFileChip = {
    localId: "local-1",
    localUri: "file:///private/var/.../IMG_0001.pdf",
    filename: "report.pdf",
    mimeType: "application/pdf",
    status: "completed" as const,
    id: "att-42",
    url: "mc://file/att-42",
    downloadUrl: "/api/attachments/att-42/download",
  };

  it("resolves a server-relative downloadUrl against the API base", () => {
    expect(
      resolveAttachmentUrlWithBase(completedFileChip.downloadUrl, BASE),
    ).toBe("https://api.example.test/api/attachments/att-42/download");
  });

  it("preserves an absolute downloadUrl returned by CloudFront / presign", () => {
    const cloudFront = {
      ...completedFileChip,
      downloadUrl:
        "https://cdn.example.test/att-42.pdf?Signature=s&Key-Pair-Id=k",
    };
    expect(
      resolveAttachmentUrlWithBase(cloudFront.downloadUrl, BASE),
    ).toBe(cloudFront.downloadUrl);
  });

  it("returns null when the upload hasn't populated downloadUrl yet (no Linking call)", () => {
    // Mirrors a `completed` chip that arrived before the server response
    // (defensive; in practice `completed` implies downloadUrl is set).
    const partial = { ...completedFileChip, downloadUrl: undefined };
    expect(resolveAttachmentUrlWithBase(partial.downloadUrl, BASE)).toBeNull();
  });
});

describe("pickAttachmentImageUrl (RUYI-141)", () => {
  // 未配置签名器的部署：download_url 与 markdown_url 都渲染为鉴权相对路径
  // `/api/attachments/{id}/download`（见 server/internal/handler/file.go
  // attachmentToResponse / buildMarkdownURL），RN 的 Image.getSize 无法带
  // Authorization 头访问，会 401 落回灰框占位。`url` 是同一部署下匿名可达
  // 的静态存储路径（RUYI-133 QA 实测证据）。
  const unsigned = {
    url: "/uploads/3fa85f64-5717-4562-b3fc-2c963f66afa6/photo.png",
    download_url: "/api/attachments/3fa85f64-5717-4562-b3fc-2c963f66afa6/download",
    markdown_url: "/api/attachments/3fa85f64-5717-4562-b3fc-2c963f66afa6/download",
  };

  it("falls back to the anonymous static `url` when download_url/markdown_url are the auth-gated stable path", () => {
    expect(pickAttachmentImageUrl(unsigned)).toBe(unsigned.url);
  });

  it("falls back to `url` even when markdown_url is absolute but still points at the auth-gated path (PublicURL configured, no CDN)", () => {
    const withPublicUrl = {
      ...unsigned,
      markdown_url: "https://app.example.test/api/attachments/3fa85f64-5717-4562-b3fc-2c963f66afa6/download",
    };
    expect(pickAttachmentImageUrl(withPublicUrl)).toBe(withPublicUrl.url);
  });

  it("prefers a CloudFront/S3-signed absolute download_url — signer deployments must not regress", () => {
    const signed = {
      url: "s3://private-bucket/3fa85f64-5717-4562-b3fc-2c963f66afa6/photo.png",
      download_url:
        "https://cdn.example.test/3fa85f64-5717-4562-b3fc-2c963f66afa6/photo.png?Policy=p&Signature=s&Key-Pair-Id=k",
      markdown_url: "https://app.example.test/api/attachments/3fa85f64-5717-4562-b3fc-2c963f66afa6/download",
    };
    expect(pickAttachmentImageUrl(signed)).toBe(signed.download_url);
  });

  it("falls back to the auth-gated download_url when no candidate is credential-free", () => {
    // Degenerate case (should not occur from a real server response, but
    // pinning it keeps the function total rather than throwing): every
    // candidate matches the stable-path shape.
    const allGated = {
      url: "/api/attachments/3fa85f64-5717-4562-b3fc-2c963f66afa6/download",
      download_url: "/api/attachments/3fa85f64-5717-4562-b3fc-2c963f66afa6/download",
      markdown_url: "/api/attachments/3fa85f64-5717-4562-b3fc-2c963f66afa6/download",
    };
    expect(pickAttachmentImageUrl(allGated)).toBe(allGated.download_url);
  });
});

describe("resolveAttachmentUrl (store-bound)", () => {
  it("matches the with-base form for an absolute URL regardless of the active server", () => {
    // For absolute URLs the base is irrelevant — guarantees pass-through
    // stays stable.
    const absolute = "https://cdn.example.test/file.pdf?Signature=s";
    expect(resolveAttachmentUrl(absolute)).toBe(absolute);
  });

  it("resolves a server-relative path against the ACTIVE server's API base", () => {
    // RUYI-4: 这是应用内切换服务器后附件必须跟着走的那条路径 —— 地址在
    // 调用时从 store 现取,不是模块加载期绑死的。
    expect(resolveAttachmentUrl("/api/attachments/3fa85f64-5717-4562-b3fc-2c963f66afa6/download")).toBe(
      "https://api.example.test/api/attachments/3fa85f64-5717-4562-b3fc-2c963f66afa6/download",
    );
  });

  it("returns null for empty input", () => {
    expect(resolveAttachmentUrl(undefined)).toBeNull();
    expect(resolveAttachmentUrl(null)).toBeNull();
    expect(resolveAttachmentUrl("")).toBeNull();
  });
});
