/**
 * RUYI-542: the More popover gains a server row between the user card and
 * the workspace card. The row is a two-line disclosure (active server name
 * + URL subtitle + chevron) that pushes the in-app server management
 * screen (`/server-settings`), the same destination the settings page's
 * Server group already uses — selection/switch semantics stay in one
 * place (switchServer + confirm alert live there).
 *
 * Menu primitives are mocked as transparent pass-throughs (the real
 * @rn-primitives layer measures layout and portals content, none of which
 * exists under jest); assertions run against the accessibility tree these
 * mocks preserve in render order.
 */
import { cleanup, fireEvent, render, screen } from "@testing-library/react-native";

const mockPush = jest.fn();

const mockServerState = {
  servers: [
    { id: "default", name: "HomeLab (local)", apiUrl: "http://192.168.2.226:3000", builtIn: true },
    { id: "srv_b", name: "Office", apiUrl: "https://office.example.test", builtIn: false },
  ],
  activeServerId: "default",
};
const mockUser = { name: "cfgxy", email: "cfgxy@qq.com", avatar_url: null };
const mockWorkspaces = [
  { id: "w1", slug: "ruyi", name: "Ruyi", avatar_url: null },
  { id: "w2", slug: "other", name: "Other", avatar_url: null },
];

jest.mock("expo-router", () => ({
  router: { push: mockPush, replace: jest.fn() },
  usePathname: () => "/ruyi/inbox",
}));
jest.mock("@expo/vector-icons", () => ({ Ionicons: () => null }));
jest.mock("expo-image", () => ({ Image: () => null }));
jest.mock("@/components/ui/text", () => ({ Text: jest.requireActual("react-native").Text }));
jest.mock("@/components/ui/dropdown-menu", () => {
  const React = jest.requireActual("react");
  const { Pressable, View } = jest.requireActual("react-native");
  return {
    DropdownMenu: ({ children }: { children?: React.ReactNode }) => React.createElement(View, null, children),
    DropdownMenuTrigger: ({ children }: { children?: React.ReactNode }) => React.createElement(View, null, children),
    DropdownMenuContent: ({ children }: { children?: React.ReactNode }) => React.createElement(View, null, children),
    DropdownMenuItem: ({ onPress, disabled, accessibilityLabel, children }: {
      onPress?: () => void; disabled?: boolean; accessibilityLabel?: string; children?: React.ReactNode;
    }) => React.createElement(Pressable, { onPress, disabled, accessibilityLabel }, children),
    DropdownMenuSeparator: () => React.createElement(View, { testID: "menu-separator" }),
  };
});
jest.mock("@/components/workspace/workspace-avatar", () => ({ WorkspaceAvatar: () => null }));
jest.mock("react-native-safe-area-context", () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));
jest.mock("@/data/auth-store", () => ({
  useAuthStore: (select: (state: { user: typeof mockUser }) => unknown) => select({ user: mockUser }),
}));
jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (select: (state: { currentWorkspaceSlug: string }) => unknown) =>
    select({ currentWorkspaceSlug: "ruyi" }),
}));
jest.mock("@/data/server-store", () => ({
  useServerStore: (select: (state: typeof mockServerState) => unknown) => select(mockServerState),
}));
jest.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: mockWorkspaces }),
}));
jest.mock("@/data/queries/workspaces", () => ({
  workspaceListOptions: () => ({ queryKey: ["workspaces"] }),
}));
jest.mock("@/lib/use-check-app-update", () => ({
  useAppUpdate: () => ({ currentVersion: "0.2.0", checkForUpdates: jest.fn() }),
}));
jest.mock("@/lib/use-color-scheme", () => ({ useColorScheme: () => ({ colorScheme: "light" }) }));
jest.mock("@/lib/theme", () => ({
  THEME: { light: { foreground: "black", mutedForeground: "gray", secondary: "#eee" } },
}));
// The component reads absolute keys via the shared i18next instance
// (see the navItems comment in more-tab-dropdown.tsx); under jest the
// instance is uninitialized, so resolve to the inline default.
jest.mock("i18next", () => ({
  __esModule: true,
  default: { t: (_key: string, defaultValue?: unknown) => (typeof defaultValue === "string" ? defaultValue : _key) },
}));

const MoreTabDropdownAnchor = jest.requireActual<
  typeof import("@/components/nav/more-tab-dropdown")
>("@/components/nav/more-tab-dropdown").MoreTabDropdownAnchor;

/** Pre-order traversal of the render tree collecting labelled nodes. */
function collectLabels(node: unknown, acc: string[] = []): string[] {
  if (node === null || typeof node !== "object") return acc;
  const { props, children } = node as {
    props?: { accessibilityLabel?: unknown };
    children?: unknown;
  };
  if (typeof props?.accessibilityLabel === "string") acc.push(props.accessibilityLabel);
  if (Array.isArray(children)) children.forEach((child) => collectLabels(child, acc));
  return acc;
}

async function renderDropdown() {
  return render(<MoreTabDropdownAnchor triggerRef={{ current: null }} />);
}

afterEach(() => {
  cleanup();
  jest.clearAllMocks();
});

it("renders the server row between the user card and the workspace card", async () => {
  const labels = collectLabels((await renderDropdown()).toJSON());
  const userIndex = labels.indexOf("My Account");
  const serverIndex = labels.indexOf("Server");
  const workspaceIndex = labels.indexOf("Switch workspace");
  expect(userIndex).toBeGreaterThanOrEqual(0);
  expect(serverIndex).toBeGreaterThan(userIndex);
  expect(workspaceIndex).toBeGreaterThan(serverIndex);
});

it("shows the active server name with the URL as subtitle", async () => {
  await renderDropdown();
  expect(screen.getByText("HomeLab (local)")).toBeTruthy();
  expect(screen.getByText("http://192.168.2.226:3000")).toBeTruthy();
});

it("pushes the server management screen when the server row is tapped", async () => {
  await renderDropdown();
  fireEvent.press(screen.getByLabelText("Server"));
  expect(mockPush).toHaveBeenCalledWith("/server-settings");
});

// RUYI-638 阶段3: the usage stats entry rides the same navItems convention
// as the other dropdown rows — labelled "Analytics" (layout:nav.usage, the
// key web's sidebar uses) and pushing /<slug>/more/stats.
it("renders the analytics row after the voice runtimes row", async () => {
  const labels = collectLabels((await renderDropdown()).toJSON());
  const voiceIndex = labels.indexOf("Voice Runtimes");
  const analyticsIndex = labels.indexOf("Analytics");
  expect(voiceIndex).toBeGreaterThanOrEqual(0);
  expect(analyticsIndex).toBeGreaterThan(voiceIndex);
});

it("pushes the stats screen when the analytics row is tapped", async () => {
  await renderDropdown();
  fireEvent.press(screen.getByLabelText("Analytics"));
  expect(mockPush).toHaveBeenCalledWith("/ruyi/more/stats");
});
