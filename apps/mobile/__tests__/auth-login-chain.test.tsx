// RUYI-568：登录链路失败反馈与取消语义的屏幕级特征测试。
// - 登录屏：成功路径回归、超时失败 → 明确错误文案 + 重试入口可用、
//   发送中修改邮箱 → 在途请求立即取消（不留悬挂请求）。
// - 验证屏：verify 在途时离开屏幕 → 请求被中止，不再悬挂。
// resend 在途 unmount 中止因需要半 fake 定时器驱动冷却倒计时，独立在
// auth-verify-resend-abort.test.tsx（避免 fake timers 的 act 域跨测试
// 泄漏）。store 层 signal 透传由 vitest lane（data/auth-store.test.ts）
// 覆盖，api 层 10s deadline 由 data/api.test.ts 覆盖。
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
// mapAuthError 直连 i18next——未初始化环境下取 defaultValue。
// __esModule 标记让 interop 后 .default 指向 { t } 本体。
jest.mock("i18next", () => ({
  __esModule: true,
  default: { t: (_key: string, defaultValue?: string) => defaultValue ?? _key },
}));
// mock 工厂内不允许引用外部导入（jest hoist 守卫），RN 控件一律在工厂内
// require 惰性获取。
jest.mock("@/components/ui/text", () => ({
  Text: (props: Record<string, unknown>) => {
    const { createElement } = jest.requireActual("react");
    const { Text } = jest.requireActual("react-native");
    return createElement(Text, props);
  },
}));
jest.mock("@/components/ui/text-field", () => ({
  TextField: ({ invalid: _invalid, className: _className, ...rest }: Record<string, unknown>) => {
    const { createElement } = jest.requireActual("react");
    const { TextInput } = jest.requireActual("react-native");
    return createElement(TextInput, rest);
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

const LoginScreen = require("@/app/(auth)/login").default;
const VerifyScreen = require("@/app/(auth)/verify").default;

// 与真实 fetchRaw 同语义的 pending promise：signal abort 即以 AbortError
// 拒绝——屏幕的复位逻辑（finally）依赖该拒绝路径，永不结算的假 promise
// 会让 fireEvent.press 的 await 挂死。
function abortablePending(opts?: { signal?: AbortSignal }): Promise<never> {
  return new Promise<never>((_resolve, reject) => {
    opts?.signal?.addEventListener("abort", () => {
      const err = new Error("Aborted");
      err.name = "AbortError";
      reject(err);
    });
  });
}

afterEach(() => {
  cleanup();
  jest.clearAllMocks();
  jest.useRealTimers();
});

describe("login screen (RUYI-568)", () => {
  it("successful send navigates to /verify with the trimmed email", async () => {
    mockSendCode.mockResolvedValue(undefined);
    const view = await render(<LoginScreen />);
    await fireEvent.changeText(
      view.getByPlaceholderText("you@example.com"),
      "  user@test.local  ",
    );
    await fireEvent.press(screen.getByText("Send code"));
    await waitFor(() =>
      expect(mockPush).toHaveBeenCalledWith({
        pathname: "/verify",
        params: { email: "user@test.local" },
      }),
    );
  });

  it("timeout failure shows the unreachable error and keeps the retry entry usable", async () => {
    mockSendCode.mockRejectedValueOnce(
      new Error("Request timed out after 10000ms"),
    );
    const view = await render(<LoginScreen />);
    await fireEvent.changeText(
      view.getByPlaceholderText("you@example.com"),
      "user@test.local",
    );
    await fireEvent.press(screen.getByText("Send code"));
    await waitFor(() =>
      expect(
        screen.getByText("Can't reach Multica. Check your connection and retry."),
      ).toBeTruthy(),
    );
    // 重试入口可用：按钮回到可点态，再按一次重新发起并成功导航。
    mockSendCode.mockResolvedValueOnce(undefined);
    await fireEvent.press(screen.getByText("Send code"));
    await waitFor(() => expect(mockSendCode).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(mockPush).toHaveBeenCalled());
  });

  it("editing the email while send is in flight cancels the in-flight request", async () => {
    let capturedSignal: AbortSignal | undefined;
    mockSendCode.mockImplementation(
      (_email: string, opts?: { signal?: AbortSignal }) => {
        capturedSignal = opts?.signal;
        return abortablePending(opts);
      },
    );
    const view = await render(<LoginScreen />);
    await fireEvent.changeText(
      view.getByPlaceholderText("you@example.com"),
      "user@test.local",
    );
    // 屏上 handler 已显式 void 化（不回传 promise），press 即发即返，
    // 在途请求由 signal 管理——无需悬挂 act 技巧。
    await fireEvent.press(screen.getByText("Send code"));
    await waitFor(() => expect(capturedSignal).toBeDefined());
    await waitFor(() =>
      expect(screen.getByText("Sending code...")).toBeTruthy(),
    );

    // 发送中修改邮箱：在途请求立即取消，不留悬挂请求。
    await fireEvent.changeText(
      view.getByPlaceholderText("you@example.com"),
      "other@test.local",
    );
    expect(capturedSignal?.aborted).toBe(true);
    // abort 经 mock 的 AbortError 拒绝路径驱动 finally 复位：按钮回到可点态，
    // 修改后的邮箱可直接重新发送。
    await waitFor(() => expect(screen.getByText("Send code")).toBeTruthy());
    mockSendCode.mockResolvedValueOnce(undefined);
    await fireEvent.press(screen.getByText("Send code"));
    await waitFor(() =>
      expect(mockSendCode).toHaveBeenLastCalledWith(
        "other@test.local",
        expect.anything(),
      ),
    );
  });
});

describe("verify screen (RUYI-568)", () => {
  afterEach(() => {
    jest.useRealTimers();
  });

  it("unmounting while a verify is in flight aborts the request", async () => {
    let capturedSignal: AbortSignal | undefined;
    mockVerifyCode.mockImplementation(
      (_email: string, _code: string, opts?: { signal?: AbortSignal }) => {
        capturedSignal = opts?.signal;
        return abortablePending(opts);
      },
    );
    const view = await render(<VerifyScreen />);
    await fireEvent.changeText(view.getByTestId("otp-input"), "123456");
    await fireEvent.press(screen.getByText("Verify"));
    await waitFor(() => expect(capturedSignal).toBeDefined());

    // 离开本屏：在途 verify 被中止，不再导航。unmount 的 passive cleanup
    // 是异步调度的，act 冲洗后断言。
    view.unmount();
    await act(async () => {});
    expect(capturedSignal?.aborted).toBe(true);
    expect(mockReplace).not.toHaveBeenCalled();
  });

});
