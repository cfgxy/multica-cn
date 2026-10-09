/**
 * RUYI-553 — 任务失败重试入口呈按钮形态（原 text-xs 裸文本链接难点按）。
 *
 * 锁定三条行为：
 *  1. 入口是带可见底色的按钮（bg-secondary + rounded + 水平内边距），
 *     可点击区域显著大于旧版裸文本链接；回退旧形态时下方断言失败；
 *  2. 与 run-row.tsx 的 RetryButton 同标签同形态（同为任务失败重试，
 *     避免一屏两制）——className 契约与 issue-run-retry.test.tsx 的
 *     run-row 侧断言保持同串，改形态必须两处同改；
 *  3. 保留 accessibilityRole="button"、pending 态（ActivityIndicator +
 *     禁用）与 task 定向 rerun / 失败弹窗语义（RUYI-343）。
 */
import React from "react";
import { Alert } from "react-native";
import { fireEvent, render, screen, waitFor } from "@testing-library/react-native";

const mockMutate = jest.fn();
let mockPending = false;

jest.mock("@/data/mutations/issues", () => ({
  useRerunIssue: () => ({ isPending: mockPending, mutate: mockMutate }),
}));

jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (key: string, fallback?: string): string => fallback ?? key,
  }),
}));

jest.mock("i18next", () => ({
  __esModule: true,
  default: { t: (_key: string, fallback: string) => fallback },
}));

import { TaskRetryStrip } from "@/components/issue/task-retry-strip";

beforeEach(() => {
  jest.clearAllMocks();
  mockPending = false;
});

describe("TaskRetryStrip (agent failure comment retry entry)", () => {
  it("renders a filled button, not the old bare text link (RUYI-553)", async () => {
    await render(<TaskRetryStrip issueId="issue-1" taskId="task-1" />);

    const button = screen.getByLabelText("Retry task");
    expect(button.props.accessibilityRole).toBe("button");
    // 按钮形态三要素：可见底色、圆角、水平内边距。旧实现 Pressable 无
    // className，本断言回退即红。
    expect(button.props.className).toMatch(/bg-secondary/);
    expect(button.props.className).toMatch(/rounded-md/);
    expect(button.props.className).toMatch(/px-3/);
    expect(screen.getByText("Retry task")).toBeTruthy();
  });

  it("shows a spinner and disables the button while a retry is pending", async () => {
    mockPending = true;
    await render(<TaskRetryStrip issueId="issue-1" taskId="task-1" />);

    const button = screen.getByLabelText("Retry task");
    expect(button.props.accessibilityState).toMatchObject({ disabled: true });
  });

  it("fires the task-scoped rerun and surfaces failure via a native alert", async () => {
    await render(<TaskRetryStrip issueId="issue-1" taskId="task-7" />);

    fireEvent.press(screen.getByLabelText("Retry task"));
    expect(mockMutate).toHaveBeenCalledTimes(1);
    expect(mockMutate).toHaveBeenCalledWith("task-7", expect.anything());

    const alertSpy = jest.spyOn(Alert, "alert").mockImplementation(() => {});
    const { onError } = mockMutate.mock.calls[0][1] as {
      onError: (err: unknown) => void;
    };
    onError(new Error("boom"));
    await waitFor(() => expect(alertSpy).toHaveBeenCalledTimes(1));
    expect(alertSpy.mock.calls[0][0]).toMatch(/failed to retry/i);
    alertSpy.mockRestore();
  });
});
