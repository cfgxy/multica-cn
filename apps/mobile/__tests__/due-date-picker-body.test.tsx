/**
 * DueDatePickerBody 的平台分叉行为（RUYI-480）。
 *
 * 缺陷背景：`@react-native-community/datetimepicker` 在 Android 上的声明式
 * 组件 `return null`（不渲染任何内联内容，只负责挂载时弹出原生对话框），
 * `display="inline"` 是 iOS 专有值——此前 body 不分平台直接渲染该组件，
 * Android 端 formSheet 主体因此恒为空白。
 *
 * 修复契约：iOS 保持内联 UIDatePicker 不变；Android 渲染内联的所选日期
 * 内容 + 打开按钮，对话框经命令式 `DateTimePickerAndroid.open` 打开
 * （display 用 Android 合法值 "default"），挂载时自动弹一次，对话框
 * 确定后不重弹。
 *
 * 结构约束（RNTL v14 + React 19 + test-renderer 的 jsdom 调度限制）：
 * fireEvent/act 触发的状态更新需「用例内 await act(async () => {}) 冲刷」
 * 才提交，且经历交互的测试会使其后所有 render 不再提交——因此纯渲染/
 * rerender 用例在前，唯一一个交互用例置于文件末位。
 */
import { act, fireEvent, render, screen } from "@testing-library/react-native";
import { Platform } from "react-native";
import { createRef } from "react";
import {
  DueDatePickerBody,
  type DueDatePickerBodyHandle,
} from "@/components/issue/pickers/due-date-picker-body";
import { formatDateOnly, toDateOnly } from "@multica/core/issues/date";

const mockOpen = jest.fn((params: Record<string, unknown>) => params);
const mockInlinePicker = jest.fn((props: Record<string, unknown>) => null);

jest.mock("@react-native-community/datetimepicker", () => ({
  __esModule: true,
  default: (props: Record<string, unknown>) => {
    mockInlinePicker(props);
    return null;
  },
  DateTimePickerAndroid: {
    open: (params: Record<string, unknown>) => {
      mockOpen(params);
    },
    dismiss: jest.fn(),
  },
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (_key: string, fallback?: string) => fallback ?? _key,
  }),
}));

jest.mock("@/lib/display-locale", () => ({
  displayLocale: () => "en-US",
}));

// ui 原语经 @rn-primitives/slot，其 dist 含裸 JSX，jest 不转译——
// 本目录既有测试统一 mock 为 RN 原生组件（见 decision-card.test.tsx）。
jest.mock("@/components/ui/text", () => ({
  Text: jest.requireActual("react-native").Text,
}));

const originalOS = Platform.OS;

function setPlatformOS(os: "ios" | "android") {
  Object.defineProperty(Platform, "OS", { value: os, configurable: true });
}

afterEach(() => {
  Object.defineProperty(Platform, "OS", {
    value: originalOS,
    configurable: true,
  });
  jest.clearAllMocks();
});

function lastDialogParams() {
  expect(mockOpen).toHaveBeenCalled();
  return mockOpen.mock.calls[mockOpen.mock.calls.length - 1][0] as {
    onChange?: (event: unknown, selected?: Date) => void;
    value: Date;
  } & Record<string, unknown>;
}

describe("DueDatePickerBody（iOS，行为不回归）", () => {
  beforeEach(() => setPlatformOS("ios"));

  it("保持内联 UIDatePicker：display=inline，不弹对话框", async () => {
    await render(<DueDatePickerBody value="2026-10-01" />);

    expect(mockInlinePicker).toHaveBeenCalledWith(
      expect.objectContaining({ display: "inline", mode: "date" }),
    );
    expect(mockOpen).not.toHaveBeenCalled();
  });

  it("value 变化时内联选择器跟随重置", async () => {
    await render(<DueDatePickerBody value="2026-10-01" />);
    await screen.rerender(<DueDatePickerBody value="2026-11-20" />);

    const last = mockInlinePicker.mock.calls[
      mockInlinePicker.mock.calls.length - 1
    ][0] as { value: Date };
    expect(last.value.getFullYear()).toBe(2026);
    expect(last.value.getMonth()).toBe(10);
    expect(last.value.getDate()).toBe(20);
  });
});

describe("DueDatePickerBody（Android，渲染输出）", () => {
  beforeEach(() => setPlatformOS("android"));

  it("日期已设：主体内联呈现所选日期，不再是空白 body", async () => {
    await render(<DueDatePickerBody value="2026-10-01" />);

    expect(screen.getByText("Oct 1, 2026")).toBeTruthy();
  });

  it("未设日期：主体回退呈现今天（非空 body）", async () => {
    await render(<DueDatePickerBody value={null} />);

    const today = formatDateOnly(
      toDateOnly(new Date()),
      { year: "numeric", month: "short", day: "numeric" },
      "en-US",
    );
    expect(screen.getByText(today)).toBeTruthy();
  });
});

describe("DueDatePickerBody（Android，对话框交互）", () => {
  beforeEach(() => setPlatformOS("android"));

  it("挂载自动弹出→确定更新草稿不重弹→按钮携草稿重开→getIso 读回", async () => {
    const ref = createRef<DueDatePickerBodyHandle>();
    await render(<DueDatePickerBody ref={ref} value="2026-10-01" />);

    // 挂载即自动弹出一次，且用 Android 合法的 display 值
    expect(mockOpen).toHaveBeenCalledTimes(1);
    const opened = lastDialogParams();
    expect(opened.mode).toBe("date");
    // Android 只接受 calendar/spinner/clock/default；"inline" 会走进
    // RNDatePickerDisplay.valueOf 抛 IllegalArgumentException。
    expect(opened.display).toBe("default");
    expect((opened.value as Date).getFullYear()).toBe(2026);

    // 对话框确定：草稿更新为主体可见，且不自动重弹
    await act(async () => {
      opened.onChange?.(null, new Date(2026, 0, 5));
    });
    expect(mockOpen).toHaveBeenCalledTimes(1);
    expect(screen.getByText("Jan 5, 2026")).toBeTruthy();

    // 点击主体按钮：携带当前草稿值再次打开
    fireEvent.press(screen.getByTestId("due-date-dialog-trigger"));
    expect(mockOpen).toHaveBeenCalledTimes(2);
    const reopened = lastDialogParams();
    expect((reopened.value as Date).getFullYear()).toBe(2026);
    expect((reopened.value as Date).getMonth()).toBe(0);
    expect((reopened.value as Date).getDate()).toBe(5);

    // 再次确定后 ref.getIso() 以 date-only 读回
    await act(async () => {
      reopened.onChange?.(null, new Date(2026, 2, 15));
    });
    expect(ref.current?.getIso()).toBe("2026-03-15");
  });
});
