/**
 * Bottom tab bar — JS `<Tabs>` from expo-router (react-navigation under the
 * hood). We tried NativeTabs first but its `canPreventDefault: false`
 * constraint makes "tap More → open sheet" impossible. JS Tabs supports
 * `listeners.tabPress + e.preventDefault()`, which is the canonical RN
 * pattern for tab-as-action.
 *
 * "More" intercepts tabPress and pushes /[workspace]/menu — a stack route
 * registered as `presentation: "formSheet"` in [workspace]/_layout.tsx —
 * giving us a native iOS bottom sheet (drag handle, swipe-down dismiss,
 * UIKit-managed blur backdrop) without the popover's hand-painted backdrop.
 *
 * The route is named `menu` (not `more`) because (tabs)/more.tsx already
 * occupies the path /[workspace]/more for tab registration; collapsing
 * (tabs) as a group means a sibling `more.tsx` at the workspace level
 * would collide. The stub (tabs)/more.tsx file exists only because
 * expo-router requires a route entry to register a Tabs.Screen.
 *
 * Active / inactive tint colors are derived from the current colour scheme
 * via THEME so dark mode picks contrasting values automatically — no
 * hardcoded hex (the old layout's bug that made selected tabs look dim
 * on dark).
 */
import { Tabs, router } from "expo-router";
import { Image } from "expo-image";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import {
  useInboxUnreadCount,
  useChatUnreadSessionCount,
} from "@/lib/unread-counts";

// Only override backgroundColor — @react-navigation/elements Badge internally
// sets borderRadius = size/2, height = size, minWidth = size, so a single
// character renders as a perfect circle. Overriding minWidth/fontSize here
// breaks that geometry. Text color is auto-derived from backgroundColor
// luminance by Badge itself (white on brand blue).
const BADGE_STYLE = {
  backgroundColor: THEME.light.brand,
};

export default function TabsLayout() {
  const { colorScheme } = useColorScheme();
  const t = THEME[colorScheme];

  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const inboxUnread = useInboxUnreadCount(wsId);
  const chatUnread = useChatUnreadSessionCount(wsId);

  // Truncation aligned with web: inbox 99+, chat 9+ (matches sidebar +
  // ChatFab respectively). `undefined` makes React Navigation hide the
  // badge, so zero-count is a free no-op.
  const inboxBadge =
    inboxUnread > 0 ? (inboxUnread > 99 ? "99+" : String(inboxUnread)) : undefined;
  const chatBadge =
    chatUnread > 0 ? (chatUnread > 9 ? "9+" : String(chatUnread)) : undefined;

  return (
    <Tabs
      screenOptions={{
        headerShown: false,
        tabBarActiveTintColor: t.foreground,
        tabBarInactiveTintColor: t.mutedForeground,
        tabBarStyle: { backgroundColor: t.background },
        tabBarLabelStyle: { fontSize: 11 },
      }}
    >
      <Tabs.Screen
        name="inbox"
        options={{
          title: "Inbox",
          tabBarBadge: inboxBadge,
          tabBarBadgeStyle: BADGE_STYLE,
          tabBarIcon: ({ color, size, focused }) => (
            <Image
              source={focused ? "sf:tray.fill" : "sf:tray"}
              tintColor={color}
              style={{ width: size, height: size }}
            />
          ),
        }}
      />
      <Tabs.Screen
        name="my-issues"
        options={{
          title: "My Issues",
          tabBarIcon: ({ color, size, focused }) => (
            <Image
              source={focused ? "sf:checklist" : "sf:checklist.unchecked"}
              tintColor={color}
              style={{ width: size, height: size }}
            />
          ),
        }}
      />
      <Tabs.Screen
        name="chat"
        options={{
          title: "Chat",
          tabBarBadge: chatBadge,
          tabBarBadgeStyle: BADGE_STYLE,
          tabBarIcon: ({ color, size, focused }) => (
            <Image
              source={focused ? "sf:bubble.left.fill" : "sf:bubble.left"}
              tintColor={color}
              style={{ width: size, height: size }}
            />
          ),
        }}
      />
      <Tabs.Screen
        name="more"
        options={{
          title: "More",
          tabBarIcon: ({ color, size }) => (
            <Image
              source="sf:ellipsis"
              tintColor={color}
              style={{ width: size, height: size }}
            />
          ),
        }}
        listeners={() => ({
          tabPress: (e) => {
            // Intercept: don't switch to the More tab screen — instead
            // push the workspace menu route, which the workspace stack
            // presents as an iOS formSheet (see [workspace]/_layout.tsx).
            // The stub more.tsx exists only to satisfy expo-router's
            // requirement that every Tabs.Screen have a backing file.
            e.preventDefault();
            if (wsSlug) router.push(`/${wsSlug}/menu`);
          },
        })}
      />
    </Tabs>
  );
}
