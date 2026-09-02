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
    name: a.fileName ?? `image-${Date.now()}.jpg`,
    type: a.mimeType ?? "image/jpeg",
    size: a.fileSize ?? undefined,
  };
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
