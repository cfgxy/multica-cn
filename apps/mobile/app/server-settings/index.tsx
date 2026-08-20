/**
 * 服务器列表页 —— 应用内切换 API 服务器地址(RUYI-4)。
 *
 * 路由刻意放在 (auth) / (app) 两个分组之外:未登录用户连自建后端是核心
 * 场景,登录前必须可达。
 *
 * 视觉沿用 `more/settings.tsx` 的 SectionGroup / 行模式,不引入新组件。
 * 内置项置顶且没有「…」菜单 —— 不可编辑删除直接体现为「没有可点的入口」,
 * 而不是置灰按钮让用户点一次才知道禁用。
 */
import { useCallback } from "react";
import {
  ActionSheetIOS,
  Alert,
  Pressable,
  ScrollView,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Ionicons } from "@expo/vector-icons";
import { Stack, router } from "expo-router";
import { useQueryClient } from "@tanstack/react-query";
import { Text } from "@/components/ui/text";
import { Separator } from "@/components/ui/separator";
import { IconButton } from "@/components/ui/icon-button";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useServerStore } from "@/data/server-store";
import { pickActiveServer, type ServerEntry } from "@/data/server-config";
import { THEME } from "@/lib/theme";
import { useColorScheme } from "@/lib/use-color-scheme";

export default function ServerListScreen() {
  const servers = useServerStore((s) => s.servers);
  const activeServerId = useServerStore((s) => s.activeServerId);
  const setActiveServer = useServerStore((s) => s.setActiveServer);
  const removeServer = useServerStore((s) => s.removeServer);
  const user = useAuthStore((s) => s.user);
  const logout = useAuthStore((s) => s.logout);
  const clearWorkspace = useWorkspaceStore((s) => s.clear);
  const qc = useQueryClient();
  const { colorScheme } = useColorScheme();
  const mutedFg = THEME[colorScheme].mutedForeground;

  const active = pickActiveServer(servers, activeServerId);

  /**
   * 真正执行切换。除清 token 外必须同时清 workspace-store 与 React Query
   * 缓存 —— 不同后端的工作区 slug 与数据互不相通,漏清会把 A 后端的缓存
   * 渲染在 B 后端会话里。
   */
  const doSwitch = useCallback(
    async (entry: ServerEntry) => {
      // 先切换并落盘,成功后才清本地会话 —— 落盘失败时用户留在原会话里,
      // 反过来就会把人踢出去却什么都没切成。logout 只清本地 token,
      // 不发网络请求,所以此刻地址已变也不影响。
      try {
        await setActiveServer(entry.id);
      } catch (err) {
        Alert.alert(
          "Switch failed",
          err instanceof Error ? err.message : "Could not switch servers.",
        );
        return;
      }
      if (!user) return;
      await clearWorkspace();
      await logout();
      qc.clear();
      router.replace("/login");
    },
    [user, clearWorkspace, logout, qc, setActiveServer],
  );

  const onSelect = useCallback(
    (entry: ServerEntry) => {
      if (entry.id === active.id) return;
      // 未登录时直接切换,不打扰;已登录必须二次确认(会退出账号)。
      if (!user) {
        void doSwitch(entry);
        return;
      }
      Alert.alert(
        "Switch server?",
        `You'll be signed out of the current account before connecting to ${entry.name || entry.apiUrl}.`,
        [
          { text: "Cancel", style: "cancel" },
          {
            text: "Switch",
            style: "destructive",
            onPress: () => void doSwitch(entry),
          },
        ],
      );
    },
    [active.id, user, doSwitch],
  );

  const onDelete = useCallback(
    (entry: ServerEntry) => {
      Alert.alert(
        "Delete server",
        `Remove ${entry.name || entry.apiUrl} from this device?`,
        [
          { text: "Cancel", style: "cancel" },
          {
            text: "Delete",
            style: "destructive",
            onPress: () => void removeServer(entry.id),
          },
        ],
      );
    },
    [removeServer],
  );

  const onPressMenu = useCallback(
    (entry: ServerEntry) => {
      // 当前生效项不给删除项 —— 删掉正在用的服务器会造成一次隐式的地址
      // 变化,行为不可预期。用户需要先切走再删。
      const canDelete = entry.id !== active.id;
      const options = canDelete
        ? ["Cancel", "Edit", "Delete"]
        : ["Cancel", "Edit"];
      ActionSheetIOS.showActionSheetWithOptions(
        {
          options,
          cancelButtonIndex: 0,
          destructiveButtonIndex: canDelete ? 2 : undefined,
          title: entry.name || entry.apiUrl,
        },
        (i) => {
          const label = options[i];
          if (label === "Edit") router.push(`/server-settings/${entry.id}`);
          else if (label === "Delete") onDelete(entry);
        },
      );
    },
    [active.id, onDelete],
  );

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
      <Stack.Screen
        options={{
          title: "Servers",
          headerBackTitle: "Back",
          headerRight: () => (
            <IconButton
              name="add"
              onPress={() => router.push("/server-settings/new")}
              accessibilityLabel="Add server"
            />
          ),
        }}
      />
      <ScrollView
        className="flex-1"
        contentContainerClassName="px-4 py-4 gap-6"
      >
        <View className="gap-2">
          <Text className="text-xs uppercase tracking-wider text-muted-foreground px-1">
            Servers
          </Text>
          <View className="rounded-md border border-border bg-card overflow-hidden">
            {servers.map((entry, idx) => (
              <View key={entry.id}>
                <ServerRow
                  entry={entry}
                  isActive={entry.id === active.id}
                  iconColor={mutedFg}
                  onPress={() => onSelect(entry)}
                  onPressMenu={
                    entry.builtIn ? undefined : () => onPressMenu(entry)
                  }
                />
                {idx < servers.length - 1 ? <Separator /> : null}
              </View>
            ))}
          </View>
          <Text className="text-xs text-muted-foreground px-1">
            Tap a server to connect to it. Switching signs you out of the
            current account.
          </Text>
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

function ServerRow({
  entry,
  isActive,
  iconColor,
  onPress,
  onPressMenu,
}: {
  entry: ServerEntry;
  isActive: boolean;
  iconColor: string;
  onPress: () => void;
  /** 内置项不传 —— 没有可编辑/删除的操作,也就不出「…」。 */
  onPressMenu?: () => void;
}) {
  return (
    <Pressable
      onPress={onPress}
      disabled={isActive}
      className="flex-row items-center px-4 py-3.5 active:bg-secondary gap-3"
    >
      <View className="flex-1">
        <View className="flex-row items-center gap-2">
          <Text className="text-base font-medium text-foreground">
            {entry.name || entry.apiUrl}
          </Text>
          {entry.builtIn ? (
            <View className="rounded bg-muted px-1.5 py-0.5">
              <Text className="text-xs text-muted-foreground">Built-in</Text>
            </View>
          ) : null}
        </View>
        <Text className="text-xs text-muted-foreground mt-0.5">
          {entry.apiUrl}
        </Text>
      </View>
      {isActive ? (
        <Ionicons name="checkmark" size={18} color={iconColor} />
      ) : null}
      {onPressMenu ? (
        <IconButton
          name="ellipsis-horizontal"
          iconSize={18}
          color={iconColor}
          onPress={onPressMenu}
          accessibilityLabel={`Actions for ${entry.name || entry.apiUrl}`}
        />
      ) : null}
    </Pressable>
  );
}
