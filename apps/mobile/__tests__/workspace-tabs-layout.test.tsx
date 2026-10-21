import { fireEvent, render } from "@testing-library/react-native";
import { Text } from "react-native";
import type { ReactElement, ReactNode, RefObject } from "react";
import TabsLayout from "@/app/(app)/[workspace]/(tabs)/_layout";

const mockOpenMore = jest.fn();
const mockScreenOptions: Record<string, Record<string, unknown>> = {};

jest.mock("expo-router", () => {
  return {
    Tabs: Object.assign(
      ({ children }: { children: ReactNode }) => <>{children}</>,
      {
        Screen: ({ name, options }: {
          name: string;
          options: Record<string, unknown>;
        }) => {
          mockScreenOptions[name] = options;
          return null;
        },
      },
    ),
  };
});
jest.mock("expo-image", () => ({ Image: () => null }));
jest.mock("@expo/vector-icons", () => ({ Ionicons: () => null }));
jest.mock("@react-navigation/elements", () => {
  const { Pressable } = jest.requireActual("react-native");
  return { PlatformPressable: Pressable };
});
jest.mock("i18next", () => ({
  __esModule: true,
  default: { t: (_key: string, fallback?: string) => fallback ?? _key },
}));
jest.mock("@/data/workspace-store", () => ({
  useWorkspaceStore: (select: (state: { currentWorkspaceId: string }) => unknown) =>
    select({ currentWorkspaceId: "ws-1" }),
}));
jest.mock("@/lib/unread-counts", () => ({
  useInboxUnreadCount: () => 0,
  useChatUnreadMessageCount: () => 0,
  useOpenDecisionCount: () => 0,
}));
jest.mock("@/lib/use-color-scheme", () => ({
  useColorScheme: () => ({ colorScheme: "light" }),
}));
jest.mock("@/lib/theme", () => ({
  THEME: {
    light: {
      foreground: "black",
      mutedForeground: "gray",
      background: "white",
      brand: "blue",
    },
  },
}));
jest.mock("@/components/nav/more-tab-dropdown", () => ({
  MoreTabDropdownAnchor: ({ triggerRef }: {
    triggerRef: RefObject<{ open: () => void } | null>;
  }) => {
    triggerRef.current = { open: mockOpenMore };
    return null;
  },
}));

beforeEach(() => {
  jest.clearAllMocks();
  Object.keys(mockScreenOptions).forEach((name) => delete mockScreenOptions[name]);
});

it("opens More directly without navigating away from the selected Inbox tab", async () => {
  await render(<TabsLayout />);
  const tabBarButton = mockScreenOptions.more.tabBarButton as (
    props: Record<string, unknown>,
  ) => ReactElement;
  const navigateToMore = jest.fn();
  const button = await render(
    tabBarButton({
      accessibilityRole: "button",
      accessibilityState: { selected: false },
      onPress: navigateToMore,
      children: <Text>More</Text>,
    }),
  );

  const moreButton = button.getByRole("button");
  fireEvent.press(moreButton);

  expect(mockOpenMore).toHaveBeenCalledTimes(1);
  expect(navigateToMore).not.toHaveBeenCalled();
  expect(moreButton.props.accessibilityState).toEqual({ selected: false });
});
