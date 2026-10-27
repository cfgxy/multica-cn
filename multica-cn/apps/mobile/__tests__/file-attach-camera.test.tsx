/**
 * RUYI-477: 贴图按钮「相册 / 拍照」源选择与相机权限契约。
 * RUYI-553: 弹层顺序固定为相册在前、拍照在后（Owner 指定）；iOS
 * ActionSheetIOS 与 Android Modal 共用同一 options 数组，选项顺序断言
 * 同时锁两个平台的顺序。
 *
 * 锁定四条行为：
 *  1. hook 暴露 imageSourceModalProps，消费方必须挂载 <ActionSheetModal>
 *     才能在 Android 可见（同 useAvatarUploader 契约）；
 *  2. 选择「拍照」走 requestCameraPermissionsAsync → launchCameraAsync，
 *     拍摄结果进入与相册选择同一条归一化 + 上传通道；
 *  3. 相机权限被拒时弹出可理解提示，绝不静默启动相机；
 *  4. 相册/拍照两条 picker 选项都带 preferredAssetRepresentationMode:
 *     "compatible"（expo-image-picker ~55 iOS 默认 .current 会原样透出
 *     HEIC，实况照片静态帧因此无法被 Chromium/Android 渲染——RUYI-477
 *     失败段定位：选择段根因）。
 */
import React from "react";
import { Alert, Platform } from "react-native";
import { act, render, renderHook, waitFor } from "@testing-library/react-native";
import * as ImagePicker from "expo-image-picker";
import { ActionSheetModal } from "@/components/ui/action-sheet";
import { useFileAttach } from "@/components/editor/use-file-attach";
import { api } from "@/data/api";

jest.mock("@/data/api", () => ({
  MAX_FILE_SIZE: 25 * 1024 * 1024,
  api: {
    uploadFile: jest.fn().mockResolvedValue({
      id: "att-1",
      filename: "IMG_0001.jpg",
      url: "https://cdn.example/IMG_0001.jpg",
      download_url: "https://cdn.example/dl/IMG_0001.jpg",
      markdown_url: "https://cdn.example/md/IMG_0001.jpg",
      content_type: "image/jpeg",
      size_bytes: 12,
      created_at: "2026-10-06T00:00:00Z",
    }),
  },
}));

jest.mock("expo-image-picker", () => ({
  requestCameraPermissionsAsync: jest.fn().mockResolvedValue({ granted: true }),
  requestMediaLibraryPermissionsAsync: jest
    .fn()
    .mockResolvedValue({ granted: true }),
  launchCameraAsync: jest.fn(),
  launchImageLibraryAsync: jest.fn(),
  UIImagePickerPreferredAssetRepresentationMode: {
    Automatic: "automatic",
    Compatible: "compatible",
    Current: "current",
  },
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (key: string, fallback?: string): string => fallback ?? key,
  }),
}));

jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light" }),
}));

jest.mock("react-native-safe-area-context", () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

// jest-expo 默认 iOS 平台；ActionSheetIOS 在 jest 下不存在，固定 Android
// 驱动 Modal 路径（同 avatar-uploader.test.tsx 惯例）。
Object.defineProperty(Platform, "OS", { value: "android", configurable: true });

const mockedLaunchCamera = ImagePicker.launchCameraAsync as jest.Mock;
const mockedLaunchLibrary = ImagePicker.launchImageLibraryAsync as jest.Mock;
const mockedCameraPerm = ImagePicker.requestCameraPermissionsAsync as jest.Mock;

describe("useFileAttach image-source sheet (RUYI-477)", () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockedCameraPerm.mockResolvedValue({ granted: true });
  });

  it("exposes imageSourceModalProps whose ActionSheetModal renders 相册/拍照/取消（相册在前，RUYI-553）", async () => {
    const { result } = await renderHook(() => useFileAttach());

    expect(result.current.imageSourceModalProps).toEqual(
      expect.objectContaining({
        visible: false,
        sheet: null,
        onSelect: expect.any(Function),
      }),
    );

    await act(async () => {
      result.current.chooseImageSource();
    });
    expect(result.current.imageSourceModalProps.visible).toBe(true);
    expect(result.current.imageSourceModalProps.sheet?.options).toEqual([
      "Choose from Library",
      "Take Photo",
      "Cancel",
    ]);
    expect(result.current.imageSourceModalProps.sheet?.cancelButtonIndex).toBe(
      2,
    );

    const sheet = await render(
      <ActionSheetModal {...result.current.imageSourceModalProps} />,
    );
    expect(sheet.getByText("Take Photo")).toBeTruthy();
    expect(sheet.getByText("Choose from Library")).toBeTruthy();
    expect(sheet.getByText("Cancel")).toBeTruthy();

    await act(async () => {
      result.current.imageSourceModalProps.onSelect(2);
    });
    expect(result.current.imageSourceModalProps.visible).toBe(false);
    expect(mockedLaunchCamera).not.toHaveBeenCalled();
    expect(mockedLaunchLibrary).not.toHaveBeenCalled();
  });

  it("maps 相册 (index 0) to the library picker and never to the camera (RUYI-553)", async () => {
    mockedLaunchLibrary.mockResolvedValue({ canceled: true, assets: [] });
    const { result } = await renderHook(() => useFileAttach());

    await act(async () => {
      result.current.chooseImageSource();
    });
    await act(async () => {
      result.current.imageSourceModalProps.onSelect(0);
    });
    await waitFor(() => expect(mockedLaunchLibrary).toHaveBeenCalledTimes(1));
    expect(mockedLaunchCamera).not.toHaveBeenCalled();
  });

  it("Take Photo requests camera permission, launches the camera with the compatible representation, and uploads through the shared pipeline", async () => {
    mockedLaunchCamera.mockResolvedValue({
      canceled: false,
      assets: [
        { uri: "file:///cache/IMG_0001.jpg", fileName: null, mimeType: null, fileSize: null },
      ],
    });
    const { result } = await renderHook(() => useFileAttach());

    await act(async () => {
      result.current.chooseImageSource();
    });
    // 重排后「拍照」在 index 1（相册在前，RUYI-553）。
    await act(async () => {
      result.current.imageSourceModalProps.onSelect(1);
    });
    // useActionSheet.handleSelect 有 50ms 延迟派发（关闭动画后再回调）。
    await waitFor(() =>
      expect(mockedCameraPerm).toHaveBeenCalledTimes(1),
    );
    expect(mockedLaunchCamera).toHaveBeenCalledWith(
      expect.objectContaining({
        mediaTypes: ["images"],
        preferredAssetRepresentationMode: "compatible",
      }),
    );
    await waitFor(() =>
      expect(result.current.attachments[0]?.status).toBe("completed"),
    );
    expect(api.uploadFile).toHaveBeenCalledTimes(1);
    // hook 显式传 (asset, uploadContext=undefined)；这里只断言资产本体。
    expect((api.uploadFile as jest.Mock).mock.calls[0][0]).toEqual(
      expect.objectContaining({
        uri: "file:///cache/IMG_0001.jpg",
        name: expect.stringMatching(/^image-\d+\.jpg$/),
        type: "image/jpeg",
      }),
    );
  });

  it("shows an understandable alert and never launches the camera when the permission is denied", async () => {
    mockedCameraPerm.mockResolvedValue({ granted: false });
    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    const { result } = await renderHook(() => useFileAttach());

    await act(async () => {
      result.current.chooseImageSource();
    });
    // 重排后「拍照」在 index 1（相册在前，RUYI-553）。
    await act(async () => {
      result.current.imageSourceModalProps.onSelect(1);
    });

    expect(mockedLaunchCamera).not.toHaveBeenCalled();
    await waitFor(() => expect(alertSpy).toHaveBeenCalled());
    expect(alertSpy).toHaveBeenCalledWith(
      "Camera permission needed",
      expect.stringContaining("camera"),
    );
    alertSpy.mockRestore();
  });

  it("library pick passes the compatible representation so iOS HEIC stills arrive as JPEG", async () => {
    mockedLaunchLibrary.mockResolvedValue({ canceled: true, assets: [] });
    const { result } = await renderHook(() => useFileAttach());

    await act(async () => {
      await result.current.pickAndUploadImages();
    });

    expect(mockedLaunchLibrary).toHaveBeenCalledWith(
      expect.objectContaining({
        mediaTypes: ["images"],
        preferredAssetRepresentationMode: "compatible",
      }),
    );
  });
});
