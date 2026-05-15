import { useState } from "react";
import { Tabs } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { GlobalNavMenu } from "@/components/nav/global-nav-menu";
import { useWorkspaceStore } from "@/data/workspace-store";
import {
  useInboxUnreadCount,
  useChatUnreadSessionCount,
} from "@/lib/unread-counts";

const ACTIVE = "#2e2e33"; // matches tailwind.config.js primary
const INACTIVE = "#71717a"; // matches muted-foreground
const BRAND = "#4571e0"; // matches tailwind.config.js brand

// Only override backgroundColor — @react-navigation/elements Badge internally
// sets borderRadius = size/2, height = size, minWidth = size, so a single
// character renders as a perfect circle. Overriding minWidth/fontSize here
// breaks that geometry. Text color is auto-derived from backgroundColor
// luminance by Badge itself (white on brand blue).
const BADGE_STYLE = {
  backgroundColor: BRAND,
};

export default function TabsLayout() {
  // The "More" tab doesn't navigate to a screen — its tabPress is
  // intercepted to open the global nav popover. State is lifted here so
  // the listener and the Modal share the same boolean. The stub
  // more.tsx file exists only because expo-router requires a route
  // entry to register a Tabs.Screen.
  const [menuOpen, setMenuOpen] = useState(false);

  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
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
    <>
      <Tabs
        screenOptions={{
          headerShown: false,
          tabBarActiveTintColor: ACTIVE,
          tabBarInactiveTintColor: INACTIVE,
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
              <Ionicons
                name={focused ? "mail" : "mail-outline"}
                size={size}
                color={color}
              />
            ),
          }}
        />
        <Tabs.Screen
          name="my-issues"
          options={{
            title: "My Issues",
            tabBarIcon: ({ color, size, focused }) => (
              <Ionicons
                name={focused ? "list" : "list-outline"}
                size={size}
                color={color}
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
              <Ionicons
                name={focused ? "chatbubble" : "chatbubble-outline"}
                size={size}
                color={color}
              />
            ),
          }}
        />
        <Tabs.Screen
          name="more"
          options={{
            title: "More",
            tabBarIcon: ({ color, size }) => (
              <Ionicons
                name={menuOpen ? "menu" : "menu-outline"}
                size={size}
                color={color}
              />
            ),
          }}
          listeners={() => ({
            tabPress: (e) => {
              // Open the popover instead of navigating into the stub
              // more.tsx route. Without preventDefault expo-router
              // would push that route and briefly mount the stub.
              e.preventDefault();
              setMenuOpen(true);
            },
          })}
        />
      </Tabs>
      <GlobalNavMenu visible={menuOpen} onClose={() => setMenuOpen(false)} />
    </>
  );
}
