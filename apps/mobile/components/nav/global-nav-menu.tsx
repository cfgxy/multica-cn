/**
 * GlobalNavMenu — content of the workspace "Menu" sheet (a stack route
 * presented as iOS formSheet). Three sections: user identity card →
 * workspace switcher → real feature entries.
 *
 * No self-managed Modal — the parent stack screen (formSheet route)
 * handles presentation, drag-to-dismiss, and safe area. This component
 * is a pure content view; it calls `router.dismiss()` to close.
 *
 * Composition mirrors web's sidebar dropdown (packages/views/layout/
 * app-sidebar.tsx:496-511): user info row (avatar + name + email) sits
 * above the workspace list. On mobile the row is a tappable card that
 * pushes into the existing settings page.
 */
import { useMemo, useState } from "react";
import {
  ActivityIndicator,
  Image,
  Pressable,
  ScrollView,
  View,
} from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { router, usePathname } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import type { User, Workspace } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { workspaceListOptions } from "@/data/queries/workspaces";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { cn } from "@/lib/utils";

interface NavItem {
  label: string;
  icon: keyof typeof Ionicons.glyphMap;
  /** Path under /:slug/ — final href is `/${slug}${path}`. */
  path: string;
}

// Inbox / My Issues / Chat live on the bottom tab bar; Settings is reached
// via the user card at the top of this menu. Only entries that are NOT
// covered by either of those surfaces belong here.
const NAV_ITEMS: NavItem[] = [
  { label: "Issues", icon: "list-outline", path: "/more/issues" },
  { label: "Projects", icon: "cube-outline", path: "/more/projects" },
];

const ICON_COLOR = "#3f3f46";
const ICON_MUTED = "#71717a";

export function GlobalNavMenu() {
  const slug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const user = useAuthStore((s) => s.user);
  const pathname = usePathname();
  const [showWorkspaces, setShowWorkspaces] = useState(false);

  const currentWorkspace = useCurrentWorkspace(slug);

  const isActive = (path: string) => {
    if (!slug) return false;
    const target = `/${slug}${path}`;
    if (pathname === target) return true;
    return pathname.startsWith(target + "/");
  };

  const dismissAndPush = (href: string) => {
    router.dismiss();
    router.push(href);
  };

  const onNav = (path: string) => {
    if (!slug) return;
    dismissAndPush(`/${slug}${path}`);
  };

  const onOpenSettings = () => {
    if (!slug) return;
    dismissAndPush(`/${slug}/more/settings`);
  };

  const onPickWorkspace = (ws: Workspace) => {
    router.dismiss();
    router.replace(`/${ws.slug}/inbox`);
  };

  return (
    // No `flex-1` — when this view is rendered inside a formSheet with
    // `sheetAllowedDetents: "fitToContents"`, the sheet measures the
    // content's intrinsic size. `flex-1` would expand to fill the parent
    // and defeat fitToContents.
    <ScrollView className="bg-background">
      <UserCard user={user} onPress={onOpenSettings} />

      <Pressable
        onPress={() => setShowWorkspaces((v) => !v)}
        className="flex-row items-center px-4 py-3 active:bg-secondary border-b border-border"
      >
        <View className="size-7 rounded-md bg-secondary items-center justify-center mr-3">
          <Ionicons name="business" size={14} color={ICON_COLOR} />
        </View>
        <Text
          className="flex-1 text-sm font-medium text-foreground"
          numberOfLines={1}
        >
          {currentWorkspace?.name ?? "Workspace"}
        </Text>
        <Ionicons
          name={showWorkspaces ? "chevron-up" : "chevron-down"}
          size={14}
          color={ICON_MUTED}
        />
      </Pressable>

      {showWorkspaces ? (
        <WorkspaceList activeSlug={slug} onPick={onPickWorkspace} />
      ) : (
        <View className="py-1">
          {NAV_ITEMS.map((item) => {
            const active = isActive(item.path);
            return (
              <Pressable
                key={item.path}
                onPress={() => onNav(item.path)}
                className={cn(
                  "flex-row items-center px-3 py-2.5 mx-2 rounded-lg active:bg-secondary",
                  active && "bg-secondary",
                )}
              >
                <Ionicons
                  name={item.icon}
                  size={18}
                  color={ICON_COLOR}
                />
                <Text className="ml-3 flex-1 text-sm text-foreground">
                  {item.label}
                </Text>
              </Pressable>
            );
          })}
        </View>
      )}
    </ScrollView>
  );
}

function WorkspaceList({
  activeSlug,
  onPick,
}: {
  activeSlug: string | null;
  onPick: (ws: Workspace) => void;
}) {
  const { data, isLoading, error } = useQuery(workspaceListOptions());

  if (isLoading) {
    return (
      <View className="py-6 items-center">
        <ActivityIndicator />
      </View>
    );
  }

  if (error) {
    return (
      <View className="px-4 py-4">
        <Text className="text-sm text-destructive">
          Failed to load workspaces
        </Text>
      </View>
    );
  }

  return (
    <View>
      {data?.map((ws) => {
        const active = ws.slug === activeSlug;
        return (
          <Pressable
            key={ws.id}
            onPress={() => {
              if (active) return;
              onPick(ws);
            }}
            disabled={active}
            className="flex-row items-center px-4 py-3 active:bg-secondary"
          >
            <View className="flex-1">
              <Text
                className="text-sm font-medium text-foreground"
                numberOfLines={1}
              >
                {ws.name}
              </Text>
              <Text className="text-xs text-muted-foreground mt-0.5">
                /{ws.slug}
              </Text>
            </View>
            {active ? (
              <Ionicons name="checkmark" size={16} color={ICON_MUTED} />
            ) : null}
          </Pressable>
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

function UserCard({
  user,
  onPress,
}: {
  user: User | null;
  onPress: () => void;
}) {
  const initial = (user?.name ?? user?.email ?? "U").charAt(0).toUpperCase();
  return (
    <Pressable
      onPress={onPress}
      className="flex-row items-center gap-3 px-4 py-3.5 active:bg-secondary border-b border-border"
    >
      {user?.avatar_url ? (
        <Image
          source={{ uri: user.avatar_url }}
          className="size-10 rounded-full bg-muted"
        />
      ) : (
        <View className="size-10 rounded-full bg-muted items-center justify-center">
          <Text className="text-sm font-medium text-muted-foreground">
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
          <Text className="text-xs text-muted-foreground mt-0.5" numberOfLines={1}>
            {user.email}
          </Text>
        ) : null}
      </View>
      <Ionicons name="chevron-forward" size={16} color={ICON_MUTED} />
    </Pressable>
  );
}
