/**
 * Workspace switcher — presented as a formSheet by the parent Stack.
 *
 * Reached from the More popover's WorkspaceCard (collapsed single-row entry).
 * Lists every workspace the user belongs to, current one disabled with a
 * checkmark. Rows of workspaces holding unread inbox items show a small
 * blue dot before the name (RUYI-44) — same account-level summary and
 * predicate as web's sidebar switcher (`unreadWorkspaceIds`,
 * @multica/core/inbox/unread), so both platforms point at the same
 * workspaces. Tapping a non-current row triggers an iOS-native `Alert.alert`
 * confirm — only after the user confirms do we dismiss the sheet and
 * `router.replace` to the target slug.
 *
 * Why a confirm step:
 *   The previous flow ("popover → tap row → instant switch") had no friction
 *   against fat-finger taps in the cramped popover, and the user lost their
 *   entire navigation context (tabs, scroll position) with one accidental
 *   tap. iOS Alert is the platform-correct gate (mobile/CLAUDE.md Principle
 *   3 — iOS native > RNR > discuss).
 *
 * Switching itself stays minimal: `router.dismiss()` to close this sheet,
 * then `router.replace(/${slug}/inbox)`. The downstream WorkspaceRouteLayout
 * handles `setCurrentWorkspace(slug, uuid)` on mount.
 */
import {
  ActivityIndicator,
  Alert,
  Platform,
  Pressable,
  ScrollView,
  View,
} from "react-native";
import { Image as ExpoImage } from "expo-image";
import { Ionicons } from "@expo/vector-icons";
import { router } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import type { Workspace } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { WorkspaceAvatar } from "@/components/workspace/workspace-avatar";
import { workspaceListOptions } from "@/data/queries/workspaces";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useWorkspaceUnreadIds } from "@/lib/unread-counts";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";
import { cn } from "@/lib/utils";

export default function SwitchWorkspaceRoute() {
  const activeSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const { data, isLoading } = useQuery(workspaceListOptions());
  const unreadWsIds = useWorkspaceUnreadIds(activeSlug);
  const { t } = useT("workspace");

  const onSelect = (ws: Workspace) => {
    if (ws.slug === activeSlug) return;
    Alert.alert(
      t("mobile.switch.title", "Switch workspace"),
      t("mobile.switch.confirm_message", 'Switch to "{{name}}"?', {
        name: ws.name,
      }),
      [
        { text: t("common:cancel", "Cancel"), style: "cancel" },
        {
          text: t("mobile.switch.confirm_action", "Switch"),
          onPress: () => {
            router.dismiss();
            router.replace(`/${ws.slug}/inbox`);
          },
        },
      ],
    );
  };

  return (
    <View className="flex-1">
      <View className="px-4 pt-4 pb-3">
        <Text className="text-base font-semibold text-foreground">
          {t("mobile.switch.title", "Switch workspace")}
        </Text>
      </View>
      {isLoading ? (
        <View className="py-6 items-center">
          <ActivityIndicator />
        </View>
      ) : (
        <ScrollView className="flex-1" showsVerticalScrollIndicator={false}>
          {(data ?? []).map((ws) => {
            const active = ws.slug === activeSlug;
            return (
              <WorkspaceRow
                key={ws.id}
                workspace={ws}
                active={active}
                // Active workspace excluded — its own unread already shows
                // on the Inbox tab badge, same rule as web's switcher
                // dropdown (so dot and check never share a row).
                hasUnread={!active && unreadWsIds.has(ws.id)}
                onPress={() => onSelect(ws)}
                iconTint={theme.foreground}
              />
            );
          })}
        </ScrollView>
      )}
    </View>
  );
}

function WorkspaceRow({
  workspace,
  active,
  hasUnread,
  onPress,
  iconTint,
}: {
  workspace: Workspace;
  active: boolean;
  hasUnread: boolean;
  onPress: () => void;
  iconTint: string;
}) {
  return (
    <Pressable
      onPress={onPress}
      disabled={active}
      accessibilityLabel={
        active
          ? `${workspace.name}, current workspace`
          : `Switch to ${workspace.name}`
      }
      className={cn(
        "flex-row items-center gap-3 px-4 py-3 active:bg-secondary",
        active && "opacity-100",
      )}
    >
      <WorkspaceAvatar
        name={workspace.name}
        avatarUrl={workspace.avatar_url}
        size={24}
      />
      {/* Unread dot BEFORE the name, per RUYI-44 spec. Data and predicate
          are web-identical (account-level unread summary +
          unreadWorkspaceIds in @multica/core/inbox/unread); only placement
          differs — web puts the dot on the row's right edge, the mobile
          sheet puts it ahead of the name. Rendered only when there IS
          unread so rows without unread keep their rhythm. */}
      {hasUnread ? <View className="h-2 w-2 rounded-full bg-brand" /> : null}
      <Text
        className={cn(
          "flex-1 text-sm text-foreground",
          active && "font-semibold",
        )}
        numberOfLines={1}
      >
        {workspace.name}
      </Text>
      {active ? (
        Platform.OS === "ios" ? (
          <ExpoImage
            source="sf:checkmark"
            tintColor={iconTint}
            style={{ width: 16, height: 16 }}
          />
        ) : (
          <Ionicons name="checkmark" size={16} color={iconTint} />
        )
      ) : null}
    </Pressable>
  );
}
