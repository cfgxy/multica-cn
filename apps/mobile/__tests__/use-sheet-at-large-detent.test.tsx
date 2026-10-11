import { act, renderHook } from "@testing-library/react-native";
import { useNavigation } from "@react-navigation/native";
import { useSheetAtLargeDetent } from "@/lib/use-sheet-at-large-detent";

/**
 * RUYI-698 — detent 事件驱动的标题头避让状态机。
 *
 * useSheetAtLargeDetent 订阅 native-stack 的 `sheetDetentChange`
 * navigation 事件（screens 4.23 双端原生派发 `{ index, stable }`）：
 * 仅稳定事件切换「是否最大档」，非稳定事件（Android 拖拽/settling，
 * index 仍是上一个稳定档）必须被忽略，否则拖拽途中标题头 padding
 * 会提前跳变。
 */

jest.mock("@react-navigation/native", () => ({
  useNavigation: jest.fn(),
}));

const addListener = jest.fn();
const unsubscribe = jest.fn();
let listener: null | ((e: { data: { index: number; stable: boolean } }) => void) =
  null;

beforeEach(() => {
  listener = null;
  addListener.mockReset();
  unsubscribe.mockReset();
  addListener.mockImplementation(
    (
      _type: string,
      cb: (e: { data: { index: number; stable: boolean } }) => void,
    ) => {
      listener = cb;
      return unsubscribe;
    },
  );
  (useNavigation as jest.Mock).mockReturnValue({ addListener });
});

// React 19 批处理下渲染通知异步落地（同 inbox-mutations 的批处理注释），
// 事件触发走 async act flush，断言在 flush 后进行。
async function fire(index: number, stable: boolean) {
  await act(async () => {
    listener?.({ data: { index, stable } });
  });
}

describe("useSheetAtLargeDetent（RUYI-698）", () => {
  it("默认 0.6 档路由：初始 false，首帧不含避让", async () => {
    const { result } = await renderHook(() => useSheetAtLargeDetent(false));
    expect(result.current).toBe(false);
  });

  it("拉到最大档（stable index=1）切换为 true，收回 0.6 档回 false", async () => {
    const { result } = await renderHook(() => useSheetAtLargeDetent(false));
    await fire(1, true);
    expect(result.current).toBe(true);
    await fire(0, true);
    expect(result.current).toBe(false);
  });

  it("LONG_CONTENT / Android modal 路由：初始 true，事件前保持避让", async () => {
    const { result } = await renderHook(() => useSheetAtLargeDetent(true));
    expect(result.current).toBe(true);
  });

  it("非稳定事件（Android 拖拽/settling 途中）不切换状态", async () => {
    const { result } = await renderHook(() => useSheetAtLargeDetent(false));
    await fire(1, false);
    expect(result.current).toBe(false);
    await fire(0, false);
    expect(result.current).toBe(false);
    await fire(1, true);
    expect(result.current).toBe(true);
  });

  it("卸载时退订 sheetDetentChange", async () => {
    const { unmount } = await renderHook(() => useSheetAtLargeDetent(false));
    expect(unsubscribe).not.toHaveBeenCalled();
    await unmount();
    expect(unsubscribe).toHaveBeenCalledTimes(1);
  });
});
