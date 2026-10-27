import { ActivityIndicator, ScrollView, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useQuery } from "@tanstack/react-query";
import { router } from "expo-router";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { CardPressable } from "@/components/ui/card";
import { workspaceListOptions } from "@/data/queries/workspaces";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useT } from "@/lib/use-t";

export default function SelectWorkspace() {
  const user = useAuthStore((s) => s.user);
  const logout = useAuthStore((s) => s.logout);
  const setCurrentWorkspace = useWorkspaceStore((s) => s.setCurrentWorkspace);
  // 本屏文案归 workspace ns。原绑定无 ns（默认 common），但文件里唯一的
  // 既有调用点用的是绝对 key `layout:sidebar.log_out`，不受默认 ns 影响。
  const { t } = useT("workspace");
  const { data, isLoading, error, refetch } = useQuery(workspaceListOptions());

  const onSelect = async (id: string, slug: string) => {
    await setCurrentWorkspace(id, slug);
    router.replace(`/${slug}/inbox`);
  };

  return (
    <SafeAreaView className="flex-1 bg-background">
      <ScrollView contentContainerClassName="px-6 py-6 gap-6">
        <View className="gap-1">
          <Text className="text-xs uppercase tracking-wider text-muted-foreground">
            {t("mobile.select.signed_in_as", "Signed in as")}
          </Text>
          <Text className="text-base text-foreground">{user?.email}</Text>
        </View>

        <View className="gap-3">
          <Text className="text-2xl font-semibold text-foreground">
            {t("mobile.select.title", "Select a workspace")}
          </Text>

          {isLoading ? (
            <View className="py-8 items-center">
              <ActivityIndicator />
            </View>
          ) : error ? (
            <View className="gap-3">
              <Text className="text-sm text-destructive">
                {/* 整句插值而非拼接：中日韩里错误详情的位置与英文不同，
                    「加载失败：<详情>」这种冒号拼接翻不出自然语序。 */}
                {t(
                  "mobile.select.load_failed",
                  "Failed to load workspaces: {{reason}}",
                  {
                    reason:
                      error instanceof Error
                        ? error.message
                        : t("mobile.select.unknown_error", "unknown error"),
                  },
                )}
              </Text>
              <Button variant="outline" onPress={() => refetch()}>
                <Text>{t("mobile.select.retry", "Retry")}</Text>
              </Button>
            </View>
          ) : !data || data.length === 0 ? (
            <Text className="text-sm text-muted-foreground">
              {t(
                "mobile.select.no_workspaces",
                "You don't belong to any workspaces yet. Contact your workspace admin to be invited.",
              )}
            </Text>
          ) : (
            <View className="gap-3">
              {data.map((ws) => (
                <CardPressable
                  key={ws.id}
                  onPress={() => onSelect(ws.id, ws.slug)}
                >
                  <Text className="text-base font-semibold text-foreground">
                    {ws.name}
                  </Text>
                  <Text className="text-xs text-muted-foreground mt-1">
                    /{ws.slug}
                  </Text>
                  {ws.description ? (
                    <Text className="text-sm text-muted-foreground mt-2">
                      {ws.description}
                    </Text>
                  ) : null}
                </CardPressable>
              ))}
            </View>
          )}
        </View>

        <View className="pt-4 border-t border-border">
          <Button variant="outline" onPress={() => logout()}>
            <Text>{t("layout:sidebar.log_out", "Log out")}</Text>
          </Button>
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}
