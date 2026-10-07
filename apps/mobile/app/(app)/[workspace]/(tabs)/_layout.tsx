/**
 * Bottom tab bar — JS `<Tabs>` from expo-router (react-navigation under the
 * hood). We tried NativeTabs first but its `canPreventDefault: false`
 * constraint makes "tap More → open something" impossible. JS Tabs
 * supports `listeners.tabPress + e.preventDefault()`, the canonical RN
 * pattern for tab-as-action.
 *
 * The "More" tab is **not a navigation target** — its press opens a
 * DropdownMenu popover anchored above the tab. The popover is rendered
 * by `<MoreTabDropdownAnchor />` as a sibling of `<Tabs>`, NOT as a
 * `tabBarButton` replacement: keeping the real tab button intact means
 * the icon + "More" label render identically to the other three tabs.
 * We just open the dropdown imperatively from `listeners.tabPress` via
 * the exposed `TriggerRef.open()`.
 *
 * The stub (tabs)/more.tsx file still exists only because expo-router
 * requires every Tabs.Screen to have a backing route file — the press
 * is preventDefault'd so we never actually navigate to it.
 *
 * Active / inactive tint colors are derived from the current colour
 * scheme via THEME so dark mode picks contrasting values automatically.
 */
import { useRef } from "react";
import { Platform } from "react-native";
import { Tabs } from "expo-router";
import { Image } from "expo-image";
import { Ionicons } from "@expo/vector-icons";
import { View } from "react-native";
import i18n from "i18next";
import type { TriggerRef } from "@rn-primitives/dropdown-menu";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import {
  useInboxUnreadCount,
  useChatUnreadMessageCount,
  useOpenDecisionCount,
} from "@/lib/unread-counts";
import { MoreTabDropdownAnchor } from "@/components/nav/more-tab-dropdown";

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
  const inboxUnread = useInboxUnreadCount(wsId);
  const chatUnread = useChatUnreadMessageCount(wsId);
  // RUYI-494: open decision cards — same server `counts.open` web's sidebar
  // badge shows (see useOpenDecisionCount for the parity contract).
  const openDecisions = useOpenDecisionCount(wsId);

  // Truncation aligned with web's sidebar badges: 99+ for both. `undefined`
  // makes React Navigation hide the badge, so zero-count is a free no-op.
  const inboxBadge =
    inboxUnread > 0 ? (inboxUnread > 99 ? "99+" : String(inboxUnread)) : undefined;
  const chatBadge =
    chatUnread > 0 ? (chatUnread > 99 ? "99+" : String(chatUnread)) : undefined;
  const decisionBadge =
    openDecisions > 0
      ? openDecisions > 99
        ? "99+"
        : String(openDecisions)
      : undefined;

  // Imperative handle into the More tab's dropdown — listeners.tabPress
  // calls .open(); the @rn-primitives Trigger measures itself inside
  // open() so the popover anchors to MoreTabDropdownAnchor's rect.
  const moreTriggerRef = useRef<TriggerRef>(null);

  return (
    <View style={{ flex: 1 }}>
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
            title: i18n.t("layout:nav.inbox", "Inbox"),
            tabBarBadge: inboxBadge,
            tabBarBadgeStyle: BADGE_STYLE,
            tabBarIcon: Platform.OS === "ios"
              ? ({ color, size, focused }) => (
                  <Image
                    source={focused ? "sf:tray.fill" : "sf:tray"}
                    tintColor={color}
                    style={{ width: size, height: size }}
                  />
                )
              : ({ color, size, focused }) => (
                  <Ionicons
                    name={focused ? "mail" : "mail-outline"}
                    size={size}
                    color={color}
                  />
                ),
          }}
        />
        <Tabs.Screen
          name="tasks"
          options={{
            // RUYI-344: the tab graduates from personal "My Issues" to
            // full-space task management; the label uses the mobile-scoped
            // key so the shared layout:nav.issues copy stays Web-owned.
            title: i18n.t("issues:mobile.tasks.page.title", "Tasks"),
            tabBarIcon: Platform.OS === "ios"
              ? ({ color, size, focused }) => (
                  <Image
                    source={focused ? "sf:checklist" : "sf:checklist.unchecked"}
                    tintColor={color}
                    style={{ width: size, height: size }}
                  />
                )
              : ({ color, size, focused }) => (
                  <Ionicons
                    name={focused ? "checkbox" : "square-outline"}
                    size={size}
                    color={color}
                  />
                ),
          }}
        />
        <Tabs.Screen
          name="decisions"
          options={{
            // RUYI-494: 决策中心 — workspace-level decision card inbox,
            // mobile's 5th tab. Badge counts the server's `counts.open`,
            // mirroring web's sidebar badge.
            title: i18n.t("decisions:mobile.tab.title", "Decisions"),
            tabBarBadge: decisionBadge,
            tabBarBadgeStyle: BADGE_STYLE,
            tabBarIcon: Platform.OS === "ios"
              ? ({ color, size, focused }) => (
                  <Image
                    source={focused ? "sf:checkmark.seal.fill" : "sf:checkmark.seal"}
                    tintColor={color}
                    style={{ width: size, height: size }}
                  />
                )
              : ({ color, size, focused }) => (
                  <Ionicons
                    name={focused
                      ? "checkmark-done-circle"
                      : "checkmark-done-circle-outline"}
                    size={size}
                    color={color}
                  />
                ),
          }}
        />
        <Tabs.Screen
          name="chat"
          options={{
            title: i18n.t("layout:nav.chat", "Chat"),
            tabBarBadge: chatBadge,
            tabBarBadgeStyle: BADGE_STYLE,
            tabBarIcon: Platform.OS === "ios"
              ? ({ color, size, focused }) => (
                  <Image
                    source={focused ? "sf:bubble.left.fill" : "sf:bubble.left"}
                    tintColor={color}
                    style={{ width: size, height: size }}
                  />
                )
              : ({ color, size, focused }) => (
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
            title: i18n.t("agents:transcript.more_actions", "More"),
            tabBarIcon: Platform.OS === "ios"
              ? ({ color, size }) => (
                  <Image
                    source="sf:ellipsis"
                    tintColor={color}
                    style={{ width: size, height: size }}
                  />
                )
              : ({ color, size }) => (
                  <Ionicons
                    name="ellipsis-horizontal"
                    size={size}
                    color={color}
                  />
                ),
          }}
          listeners={() => ({
            tabPress: (e) => {
              // Don't navigate to the (stub) /more screen — open the
              // dropdown popover instead. The trigger is invisible and
              // mounted in MoreTabDropdownAnchor below; ref.open() also
              // measures its rect so the popover anchors correctly.
              e.preventDefault();
              moreTriggerRef.current?.open();
            },
          })}
        />
      </Tabs>

      <MoreTabDropdownAnchor triggerRef={moreTriggerRef} />
    </View>
  );
}
