import { ActivityIndicator, ScrollView, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useQuery } from "@tanstack/react-query";
import { router } from "expo-router";
import type { Workspace } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { CardPressable } from "@/components/ui/card";
import { ScreenHeader } from "@/components/ui/screen-header";
import { workspaceListOptions } from "@/data/queries/workspaces";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";

export default function Settings() {
  const user = useAuthStore((s) => s.user);
  const logout = useAuthStore((s) => s.logout);
  const currentSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const setCurrentWorkspace = useWorkspaceStore((s) => s.setCurrentWorkspace);
  const clearWorkspace = useWorkspaceStore((s) => s.clear);
  const { data, isLoading, error } = useQuery(workspaceListOptions());

  const onSwitch = async (ws: Workspace) => {
    if (ws.slug === currentSlug) return;
    await setCurrentWorkspace(ws.id, ws.slug);
    // Replace (not push) so the back stack doesn't trail to the old workspace.
    router.replace(`/${ws.slug}/inbox`);
  };

  const onSignOut = async () => {
    await clearWorkspace();
    await logout();
  };

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["top"]}>
      <ScreenHeader title="Settings" />
      <ScrollView contentContainerClassName="px-4 pb-6 gap-6">
        {/* Account */}
        <View className="gap-2">
          <Text className="text-xs uppercase tracking-wider text-muted-foreground">
            Account
          </Text>
          <View className="rounded-md border border-border bg-card p-4">
            <Text className="text-base font-medium text-foreground">
              {user?.name ?? "—"}
            </Text>
            <Text className="text-sm text-muted-foreground mt-1">
              {user?.email}
            </Text>
          </View>
        </View>

        {/* Workspaces */}
        <View className="gap-2">
          <Text className="text-xs uppercase tracking-wider text-muted-foreground">
            Workspaces
          </Text>
          {isLoading ? (
            <View className="py-4 items-center">
              <ActivityIndicator />
            </View>
          ) : error ? (
            <Text className="text-sm text-destructive">
              Failed to load workspaces
            </Text>
          ) : (
            <View className="gap-2">
              {data?.map((ws) => {
                const isActive = ws.slug === currentSlug;
                return (
                  <CardPressable
                    key={ws.id}
                    onPress={() => onSwitch(ws)}
                    disabled={isActive}
                  >
                    <View className="flex-row items-center justify-between">
                      <View className="flex-1 pr-2">
                        <Text className="text-base font-medium text-foreground">
                          {ws.name}
                        </Text>
                        <Text className="text-xs text-muted-foreground mt-0.5">
                          /{ws.slug}
                        </Text>
                      </View>
                      {isActive ? (
                        <Text className="text-xs text-muted-foreground">
                          Active
                        </Text>
                      ) : null}
                    </View>
                  </CardPressable>
                );
              })}
            </View>
          )}
        </View>

        {/* Sign out */}
        <View className="pt-4 border-t border-border">
          <Button variant="outline" onPress={onSignOut}>
            Sign out
          </Button>
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}
