/**
 * MoreTabDropdownAnchor — the popover that opens when the More tab is
 * tapped. Mounted as a sibling to the Tabs view, NOT as the tab button
 * itself: that way the real More tab button stays a standard React
 * Navigation `PlatformPressable` (icon + "More" label, full visual
 * parity with Inbox / My Issues / Chat).
 *
 * The wrapper View is absolute-positioned over the More tab's screen
 * rect (right 25%, bottom = safe-area, height = tab bar). It uses
 * `pointerEvents="box-none"` so taps pass through to the real tab
 * button underneath; we open the dropdown imperatively from the tab's
 * `listeners.tabPress` via the exposed `TriggerRef.open()`. The
 * @rn-primitives Trigger measures its own layout inside `open()`, so
 * the popover anchors to this invisible Pressable's rect — i.e.
 * directly above the More tab.
 *
 * Why ref-controlled instead of `asChild` on the tab button: a previous
 * attempt wrapped a custom tabBarButton in `<DropdownMenu.Root>` +
 * Trigger asChild. RN's BottomTabItem wraps the returned button in
 * `<View style={{flex:1}}>` and expects a single Pressable child. Our
 * Root introduced an extra wrapping `View` with no flex:1, collapsing
 * the More cell and stripping the label. The Option B pattern here
 * leaves the real tab button entirely alone.
 *
 * Visual conventions inside the popover (apps/mobile/CLAUDE.md):
 *   - All glyphs are SF Symbols rendered via expo-image (`sf:` source),
 *     so they share the visual language of the bottom tab bar icons.
 *   - All colours route through THEME tokens (foreground /
 *     mutedForeground / secondary), so dark mode is automatic.
 *   - Workspace section is a flat list of every workspace the user
 *     belongs to. The current one is rendered bold + with a trailing
 *     `sf:checkmark`; the others are tappable and switch immediately.
 *     This is the iOS list-picker idiom (Apple Reminders' list picker,
 *     Calendar's calendar picker) — selection lives in the row itself,
 *     no secondary picker sheet, no extra chevron required.
 */
import { useMemo } from "react";
import { ActivityIndicator, Image, Pressable, View } from "react-native";
import { Image as ExpoImage } from "expo-image";
import { router, usePathname } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import type { TriggerRef } from "@rn-primitives/dropdown-menu";
import type { User, Workspace } from "@multica/core/types";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
import { Text } from "@/components/ui/text";
import { workspaceListOptions } from "@/data/queries/workspaces";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { cn } from "@/lib/utils";

// iOS bottom tab bar default height (above safe-area). React Navigation
// doesn't expose this as a layout constant, but the value is stable
// across Expo Router 55 / RN Screens 4 — see BottomTabBar.tsx in
// @react-navigation/bottom-tabs (`styles.tab` has no explicit height;
// the container settles at 49 from the inner padding + icon size).
const TAB_BAR_HEIGHT = 49;

interface NavItem {
  label: string;
  /** SF Symbol name, rendered via expo-image `source: "sf:<name>"`. */
  icon: string;
  /** Path under /:slug/ — final href is `/${slug}${path}`. */
  path: string;
}

const NAV_ITEMS: NavItem[] = [
  { label: "Issues", icon: "list.bullet", path: "/more/issues" },
  { label: "Projects", icon: "square.stack", path: "/more/projects" },
];

export function MoreTabDropdownAnchor({
  triggerRef,
}: {
  triggerRef: React.RefObject<TriggerRef | null>;
}) {
  const insets = useSafeAreaInsets();
  const slug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const user = useAuthStore((s) => s.user);
  const pathname = usePathname();
  const { colorScheme } = useColorScheme();
  const t = THEME[colorScheme];
  const currentWorkspace = useCurrentWorkspace(slug);

  const isActive = (path: string) => {
    if (!slug) return false;
    const target = `/${slug}${path}`;
    return pathname === target || pathname.startsWith(target + "/");
  };

  return (
    <View
      pointerEvents="box-none"
      style={{
        position: "absolute",
        right: 0,
        bottom: insets.bottom,
        width: "25%",
        height: TAB_BAR_HEIGHT,
      }}
    >
      <DropdownMenu>
        <DropdownMenuTrigger ref={triggerRef} asChild>
          {/* Invisible, non-tappable: the real tab button below catches
              all touches; we open this trigger imperatively via ref.
              The Pressable just provides a measurable rect for the
              popover to anchor against. */}
          <Pressable
            pointerEvents="none"
            accessibilityElementsHidden
            importantForAccessibility="no-hide-descendants"
            style={{ width: "100%", height: "100%" }}
          />
        </DropdownMenuTrigger>

        <DropdownMenuContent
          side="top"
          align="end"
          sideOffset={6}
          className="w-72 p-2"
        >
          <UserCard
            user={user}
            onPress={() => slug && router.push(`/${slug}/more/settings`)}
            chevronTint={t.mutedForeground}
          />

          <DropdownMenuSeparator />

          <WorkspaceSection
            activeSlug={slug}
            currentWorkspaceName={currentWorkspace?.name}
            iconTint={t.foreground}
            mutedIconTint={t.mutedForeground}
          />

          <DropdownMenuSeparator />

          {NAV_ITEMS.map((item) => (
            <DropdownMenuItem
              key={item.path}
              onPress={() => slug && router.push(`/${slug}${item.path}`)}
              accessibilityLabel={item.label}
              className={cn(
                "h-9 gap-3",
                isActive(item.path) && "bg-secondary",
              )}
            >
              <ExpoImage
                source={`sf:${item.icon}`}
                tintColor={t.foreground}
                style={{ width: 18, height: 18 }}
              />
              <Text className="text-sm text-foreground">{item.label}</Text>
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
    </View>
  );
}

/**
 * iOS-list-row identity card. Right-side `chevron.right` is the standard
 * disclosure indicator (UITableViewCellAccessoryDisclosureIndicator);
 * this row navigates into settings, so the chevron is idiomatic even
 * though menu items elsewhere in the popover don't use it.
 */
function UserCard({
  user,
  onPress,
  chevronTint,
}: {
  user: User | null;
  onPress: () => void;
  chevronTint: string;
}) {
  const initial = (user?.name ?? user?.email ?? "U").charAt(0).toUpperCase();
  return (
    <DropdownMenuItem
      onPress={onPress}
      className="h-12 gap-3"
      accessibilityLabel="Account settings"
    >
      {user?.avatar_url ? (
        <Image
          source={{ uri: user.avatar_url }}
          className="size-8 rounded-full bg-muted"
        />
      ) : (
        <View className="size-8 rounded-full bg-muted items-center justify-center">
          <Text className="text-xs font-medium text-muted-foreground">
            {initial}
          </Text>
        </View>
      )}
      <View className="flex-1 min-w-0">
        <Text
          className="text-sm font-medium text-foreground"
          numberOfLines={1}
        >
          {user?.name ?? "—"}
        </Text>
        {user?.email ? (
          <Text
            className="text-xs text-muted-foreground"
            numberOfLines={1}
          >
            {user.email}
          </Text>
        ) : null}
      </View>
      <ExpoImage
        source="sf:chevron.right"
        tintColor={chevronTint}
        style={{ width: 12, height: 12 }}
      />
    </DropdownMenuItem>
  );
}

/**
 * Flat list of every workspace the user belongs to. The current row is
 * bold + ends with `sf:checkmark`; the rest are tappable and switch
 * immediately. iOS list-picker idiom — no nested sheet, no separate
 * picker chevron. When there is only one workspace, that single row is
 * still rendered with the checkmark so the user can confirm where they
 * are; the "switchable" affordance is the existence of other rows.
 */
function WorkspaceSection({
  activeSlug,
  currentWorkspaceName,
  iconTint,
  mutedIconTint,
}: {
  activeSlug: string | null;
  currentWorkspaceName: string | undefined;
  iconTint: string;
  mutedIconTint: string;
}) {
  const { data, isLoading } = useQuery(workspaceListOptions());

  if (isLoading) {
    return (
      <View className="py-3 items-center">
        <ActivityIndicator />
      </View>
    );
  }

  // Fallback: query somehow returned nothing but we still know the
  // current workspace name from the store. Render it as a static row so
  // the user can confirm context.
  if (!data || data.length === 0) {
    return (
      <View className="px-2 py-2 flex-row items-center gap-3">
        <ExpoImage
          source="sf:building.2"
          tintColor={iconTint}
          style={{ width: 16, height: 16 }}
        />
        <Text
          className="flex-1 text-sm font-medium text-foreground"
          numberOfLines={1}
        >
          {currentWorkspaceName ?? "Workspace"}
        </Text>
      </View>
    );
  }

  return (
    <View>
      <View className="px-2 pt-1 pb-1">
        <Text className="text-[11px] uppercase tracking-wider text-muted-foreground">
          Workspace
        </Text>
      </View>
      {data.map((ws) => {
        const active = ws.slug === activeSlug;
        return (
          <DropdownMenuItem
            key={ws.id}
            onPress={() => {
              if (active) return;
              router.replace(`/${ws.slug}/inbox`);
            }}
            className="h-9 gap-3"
            accessibilityLabel={
              active ? `${ws.name}, current` : `Switch to ${ws.name}`
            }
          >
            <ExpoImage
              source="sf:building.2"
              tintColor={active ? iconTint : mutedIconTint}
              style={{ width: 16, height: 16 }}
            />
            <Text
              className={cn(
                "flex-1 text-sm text-foreground",
                active && "font-semibold",
              )}
              numberOfLines={1}
            >
              {ws.name}
            </Text>
            {active ? (
              <ExpoImage
                source="sf:checkmark"
                tintColor={iconTint}
                style={{ width: 14, height: 14 }}
              />
            ) : null}
          </DropdownMenuItem>
        );
      })}
    </View>
  );
}

function useCurrentWorkspace(slug: string | null): Workspace | undefined {
  const { data } = useQuery(workspaceListOptions());
  return useMemo(
    () => (slug ? data?.find((w) => w.slug === slug) : undefined),
    [data, slug],
  );
}
