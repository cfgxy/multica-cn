import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react-native";

const mockConnect = jest.fn(async () => undefined);
const mockProbe = jest.fn(async () => false);
const mockReplace = jest.fn();
const mockPush = jest.fn();
const mockStartupState = { phase: "select", previousId: null as string | null, connect: mockConnect };
const mockServerState = { servers: [
  { id: "default", name: "Built-in", apiUrl: "https://default.example.test", builtIn: true },
  { id: "srv_b", name: "Second", apiUrl: "https://second.example.test", builtIn: false },
] };

jest.mock("expo-router", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  return {
    router: { replace: mockReplace, push: mockPush },
    Redirect: () => null,
    useFocusEffect: (effect: () => () => void) => React.useEffect(effect, [effect]),
  };
});
jest.mock("@expo/vector-icons", () => ({ Ionicons: () => null }));
jest.mock("@/components/ui/text", () => ({ Text: jest.requireActual("react-native").Text }));
jest.mock("@/components/ui/button", () => {
  const React = jest.requireActual<typeof import("react")>("react");
  const { Pressable } = jest.requireActual<typeof import("react-native")>("react-native");
  return { Button: ({ children, onPress, disabled }: {
    children: React.ReactNode; onPress?: () => void; disabled?: boolean;
  }) => React.createElement(Pressable, { onPress, disabled }, children) };
});
jest.mock("@/components/ui/header", () => ({ Header: () => null }));
jest.mock("@/components/ui/icon-button", () => ({ IconButton: () => null }));
jest.mock("@/data/server-store", () => ({
  useServerStore: (select: (state: typeof mockServerState) => unknown) => select(mockServerState),
}));
jest.mock("@/data/startup-server-store", () => ({
  useStartupServerStore: (select: (state: typeof mockStartupState) => unknown) => select(mockStartupState),
}));
jest.mock("@/data/probe-server", () => ({ probeServer: mockProbe }));
jest.mock("@/lib/use-color-scheme", () => ({ useColorScheme: () => ({ colorScheme: "light" }) }));
jest.mock("@/lib/theme", () => ({ THEME: { light: {
  foreground: "black", mutedForeground: "gray", success: "green", destructive: "red",
} } }));
jest.mock("@/lib/use-t", () => ({ useT: () => ({ t: (key: string) => key.split(".").at(-1) }) }));

const ServerSelectScreen = jest.requireActual<typeof import("@/app/servers/select")>("@/app/servers/select").default;

afterEach(() => {
  cleanup();
  jest.clearAllMocks();
});

it("allows a red server to be chosen and connects before routing into authentication", async () => {
  await render(<ServerSelectScreen />);
  await waitFor(() => expect(screen.getByLabelText("Second, unreachable")).toBeTruthy());
  fireEvent.press(screen.getByLabelText("Second, unreachable"));
  await waitFor(() => expect(mockConnect).toHaveBeenCalledWith("srv_b"));
  expect(mockReplace).toHaveBeenCalledWith("/");
  expect(mockProbe).toHaveBeenCalledTimes(2);
});

it("does not auto-connect after the countdown is cancelled", async () => {
  mockStartupState.previousId = "default";
  const view = await render(<ServerSelectScreen />);
  fireEvent.press(view.getByText("cancel_auto"));
  await new Promise((resolve) => setTimeout(resolve, 5_200));
  expect(mockConnect).not.toHaveBeenCalled();
  mockStartupState.previousId = null;
}, 10_000);
