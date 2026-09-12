/**
 * Shared picker and upload state for issue-create and message-composer
 * attachment zones. Every picked asset is rendered immediately, then moves
 * through uploading -> completed/failed without changing its join order.
 *
 * `inFlight` remains a count rather than a boolean so concurrent uploads cannot
 * unblock submit when only the first request settles. Removing an uploading
 * item abandons that result and removes it from the effective count; a late
 * response is ignored instead of re-inserting the attachment.
 */
import { useCallback, useRef, useState } from "react";
import { Alert } from "react-native";
import * as ImagePicker from "expo-image-picker";
import * as DocumentPicker from "expo-document-picker";
import { api, MAX_FILE_SIZE, type FileAsset } from "@/data/api";
import {
  assetFromDocumentPicker,
  assetFromImagePicker,
  partitionOversize,
  type PickedAsset,
} from "@/lib/picked-asset";
import {
  removeAttachmentZoneItem,
  updateAttachmentZoneItem,
  type AttachmentZoneItem,
} from "@/lib/attachment-zone";
import { useT } from "@/lib/use-t";

export interface UploadContext {
  issueId?: string;
  commentId?: string;
}

interface UseFileAttachOptions {
  uploadContext?: UploadContext;
  alertOnError?: boolean;
  onAttachmentsEnqueued?: () => void;
}

function makeLocalId(): string {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}

/** One alert for the oversize part of a multi-pick. Filenames are user
 * content and follow the translated size-limit sentence. */
export function useOversizeAlert() {
  const { t } = useT("common");
  return useCallback(
    (oversized: PickedAsset[]) => {
      if (oversized.length === 0) return;
      const names = oversized.map((asset) => asset.name).join("\n");
      Alert.alert(
        t("composer.file_too_large_title", "File too large"),
        t(
          "composer.file_too_large_message",
          "Files must be smaller than {{size}} MB.",
          { size: Math.floor(MAX_FILE_SIZE / (1024 * 1024)) },
        ) + (names ? `\n${names}` : ""),
      );
    },
    [t],
  );
}

export function useFileAttach({
  uploadContext,
  alertOnError = true,
  onAttachmentsEnqueued,
}: UseFileAttachOptions = {}) {
  const { t } = useT("common");
  const onOversize = useOversizeAlert();
  const [attachments, setAttachments] = useState<AttachmentZoneItem[]>([]);
  const attachmentsRef = useRef(attachments);
  const activeUploadsRef = useRef(new Set<string>());
  const [inFlight, setInFlight] = useState(0);

  const updateAttachments = useCallback(
    (update: (current: AttachmentZoneItem[]) => AttachmentZoneItem[]) => {
      setAttachments((current) => {
        const next = update(current);
        attachmentsRef.current = next;
        return next;
      });
    },
    [],
  );

  const finishUpload = useCallback((localId: string) => {
    if (!activeUploadsRef.current.delete(localId)) return;
    setInFlight((count) => Math.max(0, count - 1));
  }, []);

  const startUpload = useCallback(
    async (localId: string, asset: FileAsset) => {
      if (activeUploadsRef.current.has(localId)) return;
      activeUploadsRef.current.add(localId);
      setInFlight((count) => count + 1);
      try {
        const result = await api.uploadFile(asset, uploadContext);
        if (!activeUploadsRef.current.has(localId)) return;
        updateAttachments((current) =>
          updateAttachmentZoneItem(current, localId, (item) => ({
            ...item,
            filename: result.filename,
            mimeType: result.content_type || item.mimeType,
            status: "completed",
            id: result.id,
            url: result.url,
            downloadUrl: result.download_url,
            error: undefined,
          })),
        );
      } catch (err) {
        if (!activeUploadsRef.current.has(localId)) return;
        const message =
          err instanceof Error ? err.message : t("unknown_error", "Unknown error");
        updateAttachments((current) =>
          updateAttachmentZoneItem(current, localId, (item) => ({
            ...item,
            status: "failed",
            error: message,
          })),
        );
        if (alertOnError) {
          Alert.alert(
            t("composer.upload_failed_title", "Upload failed"),
            message,
          );
        }
      } finally {
        finishUpload(localId);
      }
    },
    [alertOnError, finishUpload, t, updateAttachments, uploadContext],
  );

  const enqueueAssets = useCallback(
    (assets: PickedAsset[]) => {
      const entries = assets.map((asset) => ({
        localId: makeLocalId(),
        asset,
      }));
      updateAttachments((current) => [
        ...current,
        ...entries.map(({ localId, asset }) => ({
          localId,
          localUri: asset.uri,
          filename: asset.name,
          mimeType: asset.type,
          status: "uploading" as const,
        })),
      ]);
      onAttachmentsEnqueued?.();
      for (const { localId, asset } of entries) {
        void startUpload(localId, asset);
      }
    },
    [onAttachmentsEnqueued, startUpload, updateAttachments],
  );

  const pickAndUploadImages = useCallback(async () => {
    const result = await ImagePicker.launchImageLibraryAsync({
      mediaTypes: ImagePicker.MediaTypeOptions.Images,
      quality: 1,
      allowsMultipleSelection: true,
    });
    if (result.canceled) return;
    const assets = (result.assets ?? []).map(assetFromImagePicker);
    const { ok, oversized } = partitionOversize(assets, MAX_FILE_SIZE);
    onOversize(oversized);
    if (ok.length > 0) enqueueAssets(ok);
  }, [enqueueAssets, onOversize]);

  const pickAndUploadFiles = useCallback(async () => {
    const result = await DocumentPicker.getDocumentAsync({
      type: "*/*",
      copyToCacheDirectory: true,
      multiple: true,
    });
    if (result.canceled) return;
    const assets = (result.assets ?? []).map(assetFromDocumentPicker);
    const { ok, oversized } = partitionOversize(assets, MAX_FILE_SIZE);
    onOversize(oversized);
    if (ok.length > 0) enqueueAssets(ok);
  }, [enqueueAssets, onOversize]);

  const removeAttachment = useCallback(
    (localId: string) => {
      if (activeUploadsRef.current.delete(localId)) {
        setInFlight((count) => Math.max(0, count - 1));
      }
      updateAttachments((current) => removeAttachmentZoneItem(current, localId));
    },
    [updateAttachments],
  );

  const retryAttachment = useCallback(
    (localId: string) => {
      const item = attachmentsRef.current.find(
        (candidate) => candidate.localId === localId,
      );
      if (!item || item.status !== "failed") return;
      updateAttachments((current) =>
        updateAttachmentZoneItem(current, localId, (candidate) => ({
          ...candidate,
          status: "uploading",
          error: undefined,
        })),
      );
      void startUpload(localId, {
        uri: item.localUri,
        name: item.filename,
        type: item.mimeType,
      });
    },
    [startUpload, updateAttachments],
  );

  const clearAttachments = useCallback(() => {
    const abandonedCount = activeUploadsRef.current.size;
    activeUploadsRef.current.clear();
    if (abandonedCount > 0) {
      setInFlight((count) => Math.max(0, count - abandonedCount));
    }
    updateAttachments(() => []);
  }, [updateAttachments]);

  const restoreAttachments = useCallback(
    (snapshot: AttachmentZoneItem[]) => {
      activeUploadsRef.current.clear();
      setInFlight(0);
      updateAttachments(() => snapshot);
    },
    [updateAttachments],
  );

  return {
    attachments,
    pickAndUploadImages,
    pickAndUploadFiles,
    removeAttachment,
    retryAttachment,
    clearAttachments,
    restoreAttachments,
    uploading: inFlight > 0,
  };
}
