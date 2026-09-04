/**
 * Picker + upload glue for the description editor's image / file buttons
 * (new-issue, RUYI-42). Mirrors web's create-issue upload flow: pick →
 * stream to `/api/upload-file` → the caller inserts the durable markdown
 * link into the body and binds `attachment_ids` at submit time
 * (`referencedAttachmentIds`).
 *
 * Each call:
 *   1. Opens the appropriate picker — MULTI-SELECT on both (image library
 *      `allowsMultipleSelection`, document picker `multiple`).
 *   2. On user-cancel, resolves `[]` (caller should treat as no-op — do
 *      not insert anything into the text).
 *   3. Oversize files are split off via `partitionOversize`, surfaced as
 *      ONE alert naming them, and never uploaded — their siblings proceed
 *      (web parity: a failed file never blocks the rest of a multi-pick).
 *   4. Remaining assets upload CONCURRENTLY and resolve to the successful
 *      `Attachment`s in PICK order; each failure alerts individually and
 *      just drops out of the result. Callers treat "fewer results than
 *      picked" as "some failed", never as cancel.
 *
 * `uploading` is an in-flight COUNT, not a boolean: a boolean flips false
 * as soon as the FIRST upload's finally runs while N-1 are still mid-
 * request, which would unblock submit while uploads are still in flight
 * and strand their attachment ids unbound (MUL-3339, mobile mirror).
 */
import { useCallback, useState } from "react";
import { Alert } from "react-native";
import * as ImagePicker from "expo-image-picker";
import * as DocumentPicker from "expo-document-picker";
import { api, MAX_FILE_SIZE } from "@/data/api";
import type { Attachment } from "@multica/core/types";
import {
  assetFromDocumentPicker,
  assetFromImagePicker,
  partitionOversize,
  type PickedAsset,
} from "@/lib/picked-asset";
import { useT } from "@/lib/use-t";

export interface UploadContext {
  issueId?: string;
  commentId?: string;
}

/** One alert for the oversize part of a multi-pick. Filenames are user
 *  content — they ride below the translated size-limit sentence without
 *  needing their own keys. Shared by the composer's chip flow, which has
 *  the same "skip, don't block the siblings" rule. */
export function useOversizeAlert() {
  const { t } = useT("common");
  return useCallback(
    (oversized: PickedAsset[]) => {
      if (oversized.length === 0) return;
      const names = oversized.map((a) => a.name).join("\n");
      Alert.alert(
        t("composer.file_too_large_title", "File too large"),
        t(
          "composer.file_too_large_message",
          "Files must be smaller than {{size}} MB.",
          // 100 是 MAX_FILE_SIZE 的字节数换算，不再写死在文案里——改上限
          // 时只需改常量，四语文案自动跟随。
          { size: Math.floor(MAX_FILE_SIZE / (1024 * 1024)) },
        ) + (names ? `\n${names}` : ""),
      );
    },
    [t],
  );
}

export function useFileAttach() {
  const { t } = useT("common");
  const onOversize = useOversizeAlert();
  // In-flight COUNT (see module docstring for the MUL-3339 rationale).
  const [inFlight, setInFlight] = useState(0);

  const uploadAll = useCallback(
    async (assets: PickedAsset[], ctx?: UploadContext): Promise<Attachment[]> => {
      const results = await Promise.all(
        assets.map(async (asset) => {
          setInFlight((n) => n + 1);
          try {
            return await api.uploadFile(asset, ctx);
          } catch (err) {
            Alert.alert(
              t("composer.upload_failed_title", "Upload failed"),
              err instanceof Error
                ? err.message
                : t("unknown_error", "Unknown error"),
            );
            return null;
          } finally {
            setInFlight((n) => n - 1);
          }
        }),
      );
      // Promise.all preserves pick order; failed entries drop out as null.
      return results.filter((u): u is Attachment => u != null);
    },
    [t],
  );

  const pickAndUploadImages = useCallback(
    async (ctx?: UploadContext): Promise<Attachment[]> => {
      const result = await ImagePicker.launchImageLibraryAsync({
        // SDK 55: `MediaTypeOptions.Images` is supported (deprecation only
        // hits SDK 56+). Stick with it until we upgrade.
        mediaTypes: ImagePicker.MediaTypeOptions.Images,
        quality: 1,
        // RUYI-42: one picker pass, N attachments (Android 13+ photo picker).
        allowsMultipleSelection: true,
      });
      if (result.canceled) return [];
      const assets = (result.assets ?? []).map(assetFromImagePicker);
      const { ok, oversized } = partitionOversize(assets, MAX_FILE_SIZE);
      onOversize(oversized);
      if (ok.length === 0) return [];
      return uploadAll(ok, ctx);
    },
    [onOversize, uploadAll],
  );

  const pickAndUploadFiles = useCallback(
    async (ctx?: UploadContext): Promise<Attachment[]> => {
      const result = await DocumentPicker.getDocumentAsync({
        type: "*/*",
        copyToCacheDirectory: true,
        // RUYI-42: one picker pass, N attachments.
        multiple: true,
      });
      if (result.canceled) return [];
      const assets = (result.assets ?? []).map(assetFromDocumentPicker);
      const { ok, oversized } = partitionOversize(assets, MAX_FILE_SIZE);
      onOversize(oversized);
      if (ok.length === 0) return [];
      return uploadAll(ok, ctx);
    },
    [onOversize, uploadAll],
  );

  return {
    pickAndUploadImages,
    pickAndUploadFiles,
    uploading: inFlight > 0,
  };
}
