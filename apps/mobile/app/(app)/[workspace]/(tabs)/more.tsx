/**
 * More tab — catch-all for everything not on the primary nav.
 *
 * Grouped list, iOS-style:
 *  - Workspace: Projects, Agents (read-only browse)
 *  - Personal:  Pins, Notifications preferences
 *  - Account:   Workspace switch, Theme, Sign out
 *
 * Each row points at a placeholder sub-page until filled in.
 */
import { ScrollView, View, Pressable, ActivityIndicator } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Ionicons } from "@expo/vector-icons";
import { router } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import type { Workspace } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { ScreenHeader } from "@/components/ui/screen-header";
import { cn } from "@/lib/utils";
import { HeaderActions } from "@/components/ui/app-header-actions";
import { workspaceListOptions } from "@/data/queries/workspaces";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";

export default function More() {
  const user = useAuthStore((s) => s.user);
  const logout = useAuthStore((s) => s.logout);
  const currentSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const setCurrentWorkspace = useWorkspaceStore((s) => s.setCurrentWorkspace);
  const clearWorkspace = useWorkspaceStore((s) => s.clear);
  const { data, isLoading, error } = useQuery(workspaceListOptions());

  const onSwitch = async (ws: Workspace) => {
    if (ws.slug === currentSlug) return;
    await setCurrentWorkspace(ws.id, ws.slug);
    router.replace(`/${ws.slug}/inbox`);
  };

  const onSignOut = async () => {
    await clearWorkspace();
    await logout();
  };

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["top"]}>
      <ScreenHeader title="More" right={<HeaderActions />} />
      <ScrollView contentContainerClassName="px-4 pb-6 gap-6">
        {/* Workspace */}
        <SectionGroup title="Workspace">
          <NavRow
            icon="folder-outline"
            label="Projects"
            onPress={() =>
              currentSlug && router.push(`/${currentSlug}/more/projects`)
            }
          />
          <NavRow
            icon="people-circle-outline"
            label="Agents"
            onPress={() =>
              currentSlug && router.push(`/${currentSlug}/more/agents`)
            }
          />
        </SectionGroup>

        {/* Personal */}
        <SectionGroup title="Personal">
          <NavRow
            icon="bookmark-outline"
            label="Pins"
            onPress={() =>
              currentSlug && router.push(`/${currentSlug}/more/pins`)
            }
          />
          <NavRow
            icon="notifications-outline"
            label="Notifications"
            onPress={() =>
              currentSlug && router.push(`/${currentSlug}/more/notifications`)
            }
          />
        </SectionGroup>

        {/* Account */}
        <SectionGroup title="Account">
          <View className="p-4">
            <Text className="text-base font-medium text-foreground">
              {user?.name ?? "—"}
            </Text>
            <Text className="text-sm text-muted-foreground mt-1">
              {user?.email}
            </Text>
          </View>
        </SectionGroup>

        {/* Workspaces — same SectionGroup pattern as the lists above */}
        <SectionGroup title="Workspaces">
          {isLoading ? (
            <View className="py-4 items-center">
              <ActivityIndicator />
            </View>
          ) : error ? (
            <View className="p-4">
              <Text className="text-sm text-destructive">
                Failed to load workspaces
              </Text>
            </View>
          ) : (
            data?.map((ws, idx) => {
              const isActive = ws.slug === currentSlug;
              const isLast = idx === (data?.length ?? 0) - 1;
              return (
                <WorkspaceRow
                  key={ws.id}
                  name={ws.name}
                  slug={ws.slug}
                  isActive={isActive}
                  isLast={isLast}
                  onPress={() => onSwitch(ws)}
                />
              );
            })
          )}
        </SectionGroup>

        <View className="pt-4 border-t border-border">
          <Button variant="outline" onPress={onSignOut}>
            Sign out
          </Button>
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

function SectionGroup({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <View className="gap-2">
      <Text className="text-xs uppercase tracking-wider text-muted-foreground">
        {title}
      </Text>
      <View className="rounded-md border border-border bg-card overflow-hidden">
        {children}
      </View>
    </View>
  );
}

function NavRow({
  icon,
  label,
  onPress,
}: {
  icon: keyof typeof Ionicons.glyphMap;
  label: string;
  onPress: () => void;
}) {
  return (
    <Pressable
      onPress={onPress}
      className="flex-row items-center px-4 py-3.5 active:bg-secondary border-b border-border"
    >
      <Ionicons name={icon} size={20} color="#71717a" />
      <Text className="ml-3 flex-1 text-base text-foreground">{label}</Text>
      <Ionicons name="chevron-forward" size={18} color="#71717a" />
    </Pressable>
  );
}

function WorkspaceRow({
  name,
  slug,
  isActive,
  isLast,
  onPress,
}: {
  name: string;
  slug: string;
  isActive: boolean;
  isLast: boolean;
  onPress: () => void;
}) {
  return (
    <Pressable
      onPress={onPress}
      disabled={isActive}
      className={cn(
        "flex-row items-center px-4 py-3.5 active:bg-secondary",
        !isLast && "border-b border-border",
      )}
    >
      <View className="flex-1">
        <Text className="text-base font-medium text-foreground">{name}</Text>
        <Text className="text-xs text-muted-foreground mt-0.5">/{slug}</Text>
      </View>
      {isActive ? (
        <Ionicons name="checkmark" size={18} color="#71717a" />
      ) : (
        <Ionicons name="chevron-forward" size={18} color="#71717a" />
      )}
    </Pressable>
  );
}
