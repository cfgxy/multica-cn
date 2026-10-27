/**
 * Shared avatar pick + upload flow (RUYI-418 Q5) — extracted from the
 * settings profile screen so squads (create/detail) and the agent settings
 * surface reuse one ActionSheet → expo-image-picker → api.uploadFile path.
 *
 * The hook only produces an attachment URL; the caller decides where it goes
 * (CreateSquadRequest.avatar_url, UpdateSquadRequest.avatar_url,
 * UpdateAgentRequest.avatar_url …). Size guard and permission prompts match
 * the profile screen (5 MB, camera + library).
 */
import { useState } from "react";
import { Alert } from "react-native";
import * as ImagePicker from "expo-image-picker";
import i18n from "i18next";
import { useActionSheet } from "@/components/ui/action-sheet";
import { api } from "@/data/api";
import { assetFromImagePicker } from "@/lib/picked-asset";
import { useT } from "@/lib/use-t";

const MAX_AVATAR_BYTES = 5 * 1024 * 1024; // 5 MB — matches the profile screen.

// RUYI-477: expo-image-picker ~55 在 iOS 上默认
// preferredAssetRepresentationMode=.current，选 HEIC（实况照片静态帧、
// 高效格式拍摄）时原样透出 HEIC 容器，渲染端无法解码。compatible 让
// PHPicker 直接给出 JPEG 兼容表示，从选择段根修。
const AVATAR_PICKER_OPTIONS = {
  mediaTypes: ["images"] as ["images"],
  aspect: [1, 1] as [number, number],
  quality: 0.8,
  preferredAssetRepresentationMode:
    ImagePicker.UIImagePickerPreferredAssetRepresentationMode.Compatible,
};

export function useAvatarUploader() {
  const sheet = useActionSheet();
  const { t } = useT("settings");
  const [uploading, setUploading] = useState(false);

  const uploadAsset = async (
    asset: ImagePicker.ImagePickerAsset,
  ): Promise<string | null> => {
    if (asset.fileSize && asset.fileSize > MAX_AVATAR_BYTES) {
      Alert.alert(
        t("mobile.avatar.too_large_title", "Image too large"),
        t("mobile.avatar.too_large_body", "Pick an image under 5 MB."),
      );
      return null;
    }
    const fileAsset = assetFromImagePicker(asset);
    setUploading(true);
    try {
      const attachment = await api.uploadFile(fileAsset);
      return attachment.url;
    } catch (err) {
      Alert.alert(
        i18n.t("common:avatar_upload.failed", "Failed to upload avatar"),
        err instanceof Error
          ? err.message
          : t("mobile.avatar.upload_failed_body", "Could not upload avatar."),
      );
      return null;
    } finally {
      setUploading(false);
    }
  };

  const pickFromCamera = async (): Promise<string | null> => {
    const perm = await ImagePicker.requestCameraPermissionsAsync();
    if (!perm.granted) {
      Alert.alert(
        t("mobile.avatar.permission_title", "Permission needed"),
        t(
          "mobile.avatar.permission_camera",
          "Camera access is required to take a photo.",
        ),
      );
      return null;
    }
    const result = await ImagePicker.launchCameraAsync(AVATAR_PICKER_OPTIONS);
    if (result.canceled) return null;
    return uploadAsset(result.assets[0]);
  };

  const pickFromLibrary = async (): Promise<string | null> => {
    const perm = await ImagePicker.requestMediaLibraryPermissionsAsync();
    if (!perm.granted) {
      Alert.alert(
        t("mobile.avatar.permission_title", "Permission needed"),
        t(
          "mobile.avatar.permission_library",
          "Photo library access is required to choose an image.",
        ),
      );
      return null;
    }
    const result = await ImagePicker.launchImageLibraryAsync(
      AVATAR_PICKER_OPTIONS,
    );
    if (result.canceled) return null;
    return uploadAsset(result.assets[0]);
  };

  /**
   * Opens the shared ActionSheet and resolves with the chosen avatar URL
   * ("" when the user picked Remove, null when cancelled/failed).
   */
  const showAvatarSheet = (currentUrl: string | null | undefined) =>
    new Promise<string | null>((resolve) => {
      const options = [
        t("mobile.avatar.take_photo", "Take Photo"),
        t("mobile.avatar.choose_from_library", "Choose from Library"),
        t("mobile.avatar.remove_photo", "Remove Photo"),
        t("mobile.avatar.cancel", "Cancel"),
      ];
      const hasCurrent = !!currentUrl;
      const removeIndex = hasCurrent ? 2 : -1;
      const cancelIndex = hasCurrent ? 3 : 2;
      const visibleOptions = hasCurrent
        ? options
        : options.filter((_, i) => i !== 2);
      sheet.show({
        options: visibleOptions,
        cancelButtonIndex: cancelIndex,
        destructiveButtonIndex: removeIndex >= 0 ? removeIndex : undefined,
        onSelect: (index) => {
          if (index === cancelIndex) {
            resolve(null);
          } else if (index === 0) {
            void pickFromCamera().then(resolve);
          } else if (index === 1) {
            void pickFromLibrary().then(resolve);
          } else if (index === removeIndex) {
            resolve("");
          }
        },
      });
    });

  // Android 的弹层是 RN Modal，消费方必须挂载 <ActionSheetModal {...modalProps} />
  // 才可见（iOS 走 ActionSheetIOS 命令式路径，不依赖该渲染）。
  return { uploading, showAvatarSheet, modalProps: sheet.modalProps };
}
