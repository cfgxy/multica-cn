// RUYI-568：verify 屏 resend 在途时离开屏幕 → 请求被中止。
// 独立成文件的原因：本测试需要半 fake 定时器驱动 60s 冷却倒计时，React 19
// 调度器与 fake timers 的交互会在同文件内跨测试泄漏 act 域（详见测试内
// 注释）；jest 每个测试文件是独立模块注册表与定时器域，隔离到此为止。
// verify 在途 unmount 中止由 auth-login-chain.test.tsx 覆盖。
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react-native";

const mockSendCode = jest.fn();
const mockVerifyCode = jest.fn();
const mockPush = jest.fn();
const mockReplace = jest.fn();
const mockBack = jest.fn();
const mockAuthState = { sendCode: mockSendCode, verifyCode: mockVerifyCode };
const mockParams = { email: "user@test.local" };

jest.mock("expo-router", () => ({
  router: { push: mockPush, replace: mockReplace, back: mockBack },
  useLocalSearchParams: () => mockParams,
}));
jest.mock("expo-haptics", () => ({
  selectionAsync: jest.fn(),
  notificationAsync: jest.fn(),
  NotificationFeedbackType: { Error: "error", Success: "success" },
}));
jest.mock("i18next", () => ({
  __esModule: true,
  default: { t: (_key: string, defaultValue?: string) => defaultValue ?? _key },
}));
jest.mock("@/components/ui/text", () => ({
  Text: (props: Record<string, unknown>) => {
    const { createElement } = jest.requireActual("react");
    const { Text } = jest.requireActual("react-native");
    return createElement(Text, props);
  },
}));
jest.mock("@/components/ui/button", () => ({
  Button: ({ children, onPress, disabled }: {
    children: React.ReactNode; onPress?: () => void; disabled?: boolean;
  }) => {
    const { createElement } = jest.requireActual("react");
    const { Pressable } = jest.requireActual("react-native");
    return createElement(Pressable, { onPress, disabled, testID: "button" }, children);
  },
}));
jest.mock("@/components/ui/otp-input", () => ({
  OtpInput: ({ onChange }: { onChange?: (value: string) => void }) => {
    const { createElement } = jest.requireActual("react");
    const { TextInput } = jest.requireActual("react-native");
    return createElement(TextInput, { onChangeText: onChange, testID: "otp-input" });
  },
}));
jest.mock("@/components/brand/multica-logo", () => ({ MulticaLogo: () => null }));
jest.mock("@/data/auth-store", () => ({
  useAuthStore: (select: (state: typeof mockAuthState) => unknown) => select(mockAuthState),
}));
jest.mock("@/lib/use-t", () => ({
  useT: () => ({
    t: (key: string, arg?: unknown) => (typeof arg === "string" ? arg : key),
  }),
}));
jest.mock("react-native-keyboard-controller", () => ({
  KeyboardAvoidingView: (props: Record<string, unknown>) => {
    const { createElement } = jest.requireActual("react");
    const { View } = jest.requireActual("react-native");
    return createElement(View, props);
  },
}));
jest.mock("react-native-safe-area-context", () => ({
  SafeAreaView: (props: Record<string, unknown>) => {
    const { createElement } = jest.requireActual("react");
    const { View } = jest.requireActual("react-native");
    return createElement(View, props);
  },
}));

const VerifyScreen = require("@/app/(auth)/verify").default;

afterEach(() => {
  cleanup();
  jest.clearAllMocks();
  jest.useRealTimers();
});

// 与真实 fetchRaw 同语义的 pending promise：signal abort 即以 AbortError
// 拒绝——屏幕的复位逻辑（finally）依赖该拒绝路径。
function abortablePending(opts?: { signal?: AbortSignal }): Promise<never> {
  return new Promise<never>((_resolve, reject) => {
    opts?.signal?.addEventListener("abort", () => {
      const err = new Error("Aborted");
      err.name = "AbortError";
      reject(err);
    });
  });
}

it("unmounting while a resend is in flight aborts the request", async () => {
  // React 19 的调度器回调挂在 queueMicrotask/nextTick/setImmediate 上，
  // 默认 fake 配置把它们一并冻结，冷却倒计时的重渲染永不落地——只 fake
  // 真正的 1s interval 时钟，放行微任务类调度。
  jest.useFakeTimers({
    doNotFake: ["queueMicrotask", "nextTick", "setImmediate", "clearImmediate"],
  });
  let capturedSignal: AbortSignal | undefined;
  mockSendCode.mockImplementation(
    (_email: string, opts?: { signal?: AbortSignal }) => {
      capturedSignal = opts?.signal;
      return abortablePending(opts);
    },
  );
  const view = await render(<VerifyScreen />);
  // 首次 resend 有 60s 冷却——推进到 0 使入口可点。
  await act(async () => {
    jest.advanceTimersByTime(60_000);
    await Promise.resolve();
  });
  await waitFor(() => expect(screen.getByText("Resend code")).toBeTruthy());
  await fireEvent.press(screen.getByText("Resend code"));
  await waitFor(() => expect(capturedSignal).toBeDefined());
  expect(capturedSignal?.aborted).toBe(false);

  // 返回修改邮箱 = 离开本屏：在途 resend 被中止。unmount 的 passive
  // cleanup 是异步调度的，act 冲洗后断言。
  view.unmount();
  await act(async () => {});
  expect(capturedSignal?.aborted).toBe(true);
});
