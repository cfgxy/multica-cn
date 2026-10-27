/**
 * Pure helpers shared by every attachment entry point (comment composer
 * toolbar, new-issue description toolbar, chat composer). Normalising the
 * expo pickers' output here keeps the per-picker shape knowledge in one
 * tested place — the pickers return subtly different field names
 * (`fileName`/`fileSize` vs `name`/`size`) and both historically took
 * `assets[0]` only (RUYI-42 multi-select).
 *
 * No RN / Expo imports on purpose: this module runs under vitest's Node
 * environment (see vitest.config.ts — mobile tests are pure-logic only).
 */

import type { SharedFile } from "./share-payload";

export interface PickedAsset {
  uri: string;
  name: string;
  type: string;
  size?: number;
}

/** Fields mobile reads off one `expo-image-picker` result asset. The
 *  optional-field unions mirror the SDK 55 types, which declare these
 *  nullable AND optional depending on platform. */
export interface ImagePickerAssetShape {
  uri: string;
  fileName?: string | null;
  mimeType?: string | null;
  fileSize?: number | null;
}

/** Fields mobile reads off one `expo-document-picker` result asset. */
export interface DocumentPickerAssetShape {
  uri: string;
  name: string;
  mimeType?: string | null;
  size?: number | null;
}

export function assetFromImagePicker(a: ImagePickerAssetShape): PickedAsset {
  return {
    uri: a.uri,
    // expo-image-picker exposes `fileName` (camelCase) on iOS; fall back to
    // a placeholder so the multipart Content-Disposition is never empty.
    // The placeholder extension follows the mimeType (camera captures),
    // and a missing mimeType is inferred from the extension (RUYI-477:
    // iOS HEIC / Live-Photo stills arrive with `.HEIC` and a null
    // mimeType on some SDK versions — labelling those bytes `image/jpeg`
    // poisons the stored content-type and every downstream renderer).
    name: a.fileName ?? placeholderImageName(a.mimeType),
    type: a.mimeType ?? imageMimeFromFilename(a.fileName) ?? "image/jpeg",
    size: a.fileSize ?? undefined,
  };
}

/** Extension → image mime for types photo picking can actually produce.
 *  Kept explicit rather than a full table: the fallback below stays
 *  `image/jpeg` for anything unlisted, matching the historical default. */
const IMAGE_MIME_BY_EXT: Record<string, string> = {
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".png": "image/png",
  ".gif": "image/gif",
  ".webp": "image/webp",
  ".bmp": "image/bmp",
  ".heic": "image/heic",
  ".heif": "image/heif",
};

const EXT_BY_IMAGE_MIME: Record<string, string> = Object.fromEntries(
  Object.entries(IMAGE_MIME_BY_EXT).map(([ext, mime]) => [mime, ext]),
);

function imageMimeFromFilename(fileName?: string | null): string | undefined {
  if (!fileName) return undefined;
  const dot = fileName.lastIndexOf(".");
  if (dot < 0) return undefined;
  return IMAGE_MIME_BY_EXT[fileName.slice(dot).toLowerCase()];
}

function placeholderImageName(mimeType?: string | null): string {
  const ext =
    (mimeType && EXT_BY_IMAGE_MIME[mimeType.toLowerCase()]) || ".jpg";
  return `image-${Date.now()}${ext}`;
}

export function assetFromDocumentPicker(
  a: DocumentPickerAssetShape,
): PickedAsset {
  return {
    uri: a.uri,
    name: a.name,
    type: a.mimeType ?? "application/octet-stream",
    size: a.size ?? undefined,
  };
}

/** Map one share-intent file (already normalized + copied to our cache by
 *  the native side, RUYI-463) onto the same upload-payload shape the pickers
 *  produce, so the shared upload channel needs no per-entry-point branch. */
export function assetFromSharedFile(f: SharedFile): PickedAsset {
  return {
    uri: f.uri,
    name: f.name,
    type: f.mimeType,
    size: f.size,
  };
}

/** Split a multi-pick into uploadable vs over-limit assets. Assets with an
 *  unknown size are never blocked — the server enforces the real limit.
 *  Callers show ONE alert naming the oversized files and upload the rest
 *  (web parity: a failed file never blocks its siblings in a multi-pick). */
export function partitionOversize(
  assets: PickedAsset[],
  maxSize: number,
): { ok: PickedAsset[]; oversized: PickedAsset[] } {
  const ok: PickedAsset[] = [];
  const oversized: PickedAsset[] = [];
  for (const asset of assets) {
    if (asset.size != null && asset.size > maxSize) oversized.push(asset);
    else ok.push(asset);
  }
  return { ok, oversized };
}
