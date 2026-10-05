/**
 * RUYI-418 P1 回归：useAvatarUploader 的 Android 弹层渲染契约。
 *
 * Android 上 useActionSheet 的弹层是 RN Modal，必须由消费方显式挂载
 * <ActionSheetModal {...modalProps} />（iOS 走 ActionSheetIOS 命令式路径，
 * 不依赖该渲染）。hook 一旦不再返回 modalProps，三处头像入口在 Android
 * 全部死 UI——这里锁定「hook 返回 modalProps + modalProps 可渲染出选项」
 * 两条契约。
 */
import React from "react";
import { act, render, renderHook } from "@testing-library/react-native";
import { Platform } from "react-native";
import { ActionSheetModal } from "@/components/ui/action-sheet";
import { useAvatarUploader } from "@/lib/avatar";

jest.mock("@/data/api", () => ({
  api: {
    uploadFile: jest.fn().mockResolvedValue({ url: "https://cdn.example/a.png" }),
  },
}));

jest.mock("expo-image-picker", () => ({
  requestCameraPermissionsAsync: jest.fn().mockResolvedValue({ granted: true }),
  requestMediaLibraryPermissionsAsync: jest
    .fn()
    .mockResolvedValue({ granted: true }),
  launchCameraAsync: jest.fn(),
  launchImageLibraryAsync: jest.fn(),
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

// jest-expo 默认跑 iOS 平台，而 ActionSheetIOS 路径在 jest 下不存在。
// useActionSheet 在 show() 时读 Platform.OS，模块级固定为 Android 以驱动
// Modal 路径（同 comment-long-press.test.tsx 惯例）。
Object.defineProperty(Platform, "OS", { value: "android", configurable: true });

describe("useAvatarUploader Android sheet rendering contract", () => {
  it("returns modalProps so consumers can mount <ActionSheetModal>", async () => {
    const { result } = await renderHook(() => useAvatarUploader());

    expect(result.current.modalProps).toEqual(
      expect.objectContaining({
        visible: false,
        sheet: null,
        onSelect: expect.any(Function),
      }),
    );
  });

  it("showAvatarSheet makes the sheet visible and ActionSheetModal renders the options; cancel resolves null", async () => {
    const { result } = await renderHook(() => useAvatarUploader());

    let picked: Promise<string | null> = Promise.resolve(null);
    await act(async () => {
      picked = result.current.showAvatarSheet(null);
    });

    // 无当前头像：不含 Remove Photo，Cancel 兜底为最后一项
    expect(result.current.modalProps.visible).toBe(true);
    expect(result.current.modalProps.sheet?.options).toEqual([
      "Take Photo",
      "Choose from Library",
      "Cancel",
    ]);

    const sheet = await render(
      <ActionSheetModal {...result.current.modalProps} />,
    );
    expect(sheet.getByText("Take Photo")).toBeTruthy();
    expect(sheet.getByText("Choose from Library")).toBeTruthy();
    expect(sheet.getByText("Cancel")).toBeTruthy();
    expect(sheet.queryByText("Remove Photo")).toBeNull();

    await act(async () => {
      result.current.modalProps.onSelect(2);
      await expect(picked).resolves.toBeNull();
    });
    expect(result.current.modalProps.visible).toBe(false);
  });

  it("with a current avatar the sheet gains a destructive Remove Photo entry that resolves to empty string", async () => {
    const { result } = await renderHook(() => useAvatarUploader());

    let picked: Promise<string | null> = Promise.resolve(null);
    await act(async () => {
      picked = result.current.showAvatarSheet("https://cdn.example/a.png");
    });

    expect(result.current.modalProps.sheet?.options).toEqual([
      "Take Photo",
      "Choose from Library",
      "Remove Photo",
      "Cancel",
    ]);
    expect(result.current.modalProps.sheet?.destructiveButtonIndex).toBe(2);

    const sheet = await render(
      <ActionSheetModal {...result.current.modalProps} />,
    );
    expect(sheet.getByText("Remove Photo")).toBeTruthy();

    await act(async () => {
      result.current.modalProps.onSelect(2);
      await expect(picked).resolves.toBe("");
    });
  });
});
