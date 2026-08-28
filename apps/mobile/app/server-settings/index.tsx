/**
 * 服务器列表页 —— 应用内切换 API 服务器地址(RUYI-4)。
 *
 * 路由刻意放在 (auth) / (app) 两个分组之外:未登录用户连自建后端是核心
 * 场景,登录前必须可达。
 *
 * header 用屏内自绘的 `<Header />`(分组 layout 已关掉原生 Stack header),
 * 它自己处理 top inset;原生 header 在 Android 上会压住状态栏,详见
 * `_layout.tsx` 的注释(RUYI-25)。
 *
 * 视觉沿用 `more/settings.tsx` 的 SectionGroup / 行模式,不引入新组件。
 * 内置项置顶且没有「…」菜单 —— 不可编辑删除直接体现为「没有可点的入口」,
 * 而不是置灰按钮让用户点一次才知道禁用。
 *
 * 行内「…」用 `components/ui/dropdown-menu`(@rn-primitives)而不是
 * `ActionSheetIOS`:后者的原生模块只在 iOS 侧实现,Android 上
 * `TurboModuleRegistry.get('ActionSheetManager')` 返回 null,组件内的
 * invariant 会直接抛 —— 编辑/删除是自定义服务器的唯一入口,在 Android
 * 上崩掉等于填错地址就只能卸载重装(QA 评审 P0-2)。
 */
import { useCallback } from "react";
import { Alert, Pressable, ScrollView, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Ionicons } from "@expo/vector-icons";
import { router } from "expo-router";
import { useQueryClient } from "@tanstack/react-query";
import { Text } from "@/components/ui/text";
import { Header } from "@/components/ui/header";
import { Separator } from "@/components/ui/separator";
import { IconButton } from "@/components/ui/icon-button";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from "@/components/ui/dropdown-menu";
import { useAuthStore } from "@/data/auth-store";
import { api } from "@/data/api";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useServerStore } from "@/data/server-store";
import { pickActiveServer, type ServerEntry } from "@/data/server-config";
import { THEME } from "@/lib/theme";
import { useColorScheme } from "@/lib/use-color-scheme";
import { useT } from "@/lib/use-t";

export default function ServerListScreen() {
  const { t } = useT("settings");
  const servers = useServerStore((s) => s.servers);
  const activeServerId = useServerStore((s) => s.activeServerId);
  const setActiveServer = useServerStore((s) => s.setActiveServer);
  const removeServer = useServerStore((s) => s.removeServer);
  const user = useAuthStore((s) => s.user);
  const qc = useQueryClient();
  const { colorScheme } = useColorScheme();
  const mutedFg = THEME[colorScheme].mutedForeground;

  const active = pickActiveServer(servers, activeServerId);

  /**
   * 真正执行切换。会话按服务器分键保存(secure-storage),切换 = 换生效
   * 服务器 + 清 React Query 缓存 + 重跑 initialize() 恢复目标服务器的
   * 快照(token + slug → getMe 重建 user)。缓存无条件清:不同后端的数
   * 据互不相通,漏清会把 A 后端的缓存渲染在 B 后端会话里。
   *
   * 顺序仍是「先落盘再动会话」:落盘失败时用户留在原会话里;反过来就
   * 会先把人踢出当前服务器却什么都没切成。
   */
  const doSwitch = useCallback(
    async (entry: ServerEntry) => {
      try {
        await setActiveServer(entry.id);
      } catch (err) {
        Alert.alert(
          t("server.switch_failed_title", "Switch failed"),
          err instanceof Error
            ? err.message
            : t("server.switch_failed_message", "Could not switch servers."),
        );
        return;
      }
      // 立即丢弃内存中的旧 token:setActiveServer 后 api 的地址已指向新
      // 服务器,继续带着 A 的 token 发请求会以「B 返回 401」的形式触发
      // 全局登出路径,反过来毁掉 B 的已存快照。A 的持久化快照不受影响
      // (secure-storage 分键保存);initialize 随后按目标服务器重设。
      api.setToken(null);
      // 覆盖未登录场景:未登录时 initialize() 读不到分键 token,自然落
      // 到登录页,与旧流程等价。
      qc.clear();
      await useAuthStore.getState().initialize();
      const { user: nextUser } = useAuthStore.getState();
      const slug = useWorkspaceStore.getState().currentWorkspaceSlug;
      // 目标服务器没有已保存会话(或 getMe 网络失败但 token 已保留——
      // 与冷启动同行为,重试即恢复)→ 登录页;有 token 无 slug → 选工作区。
      router.replace(
        !nextUser ? "/login" : !slug ? "/select-workspace" : `/${slug}/inbox`,
      );
    },
    [qc, setActiveServer, t],
  );

  const onSelect = useCallback(
    (entry: ServerEntry) => {
      if (entry.id === active.id) return;
      // 未登录时直接切换,不打扰;已登录弹确认(切换会重载该服务器的
      // 会话数据,非破坏性,不用 destructive 样式)。
      if (!user) {
        void doSwitch(entry);
        return;
      }
      Alert.alert(
        t("server.switch_title", "Switch server?"),
        t("server.switch_message", { name: entry.name || entry.apiUrl }),
        [
          { text: t("server.cancel", "Cancel"), style: "cancel" },
          {
            text: t("server.switch_confirm", "Switch"),
            onPress: () => void doSwitch(entry),
          },
        ],
      );
    },
    [active.id, user, doSwitch, t],
  );

  const onDelete = useCallback(
    (entry: ServerEntry) => {
      Alert.alert(
        t("server.delete_title", "Delete server"),
        t("server.delete_message", { name: entry.name || entry.apiUrl }),
        [
          { text: t("server.cancel", "Cancel"), style: "cancel" },
          {
            text: t("server.delete", "Delete"),
            style: "destructive",
            onPress: () => void removeServer(entry.id),
          },
        ],
      );
    },
    [removeServer, t],
  );

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
      <Header
        title={t("server.title", "Servers")}
        left={
          <IconButton
            name="arrow-back"
            onPress={() => router.back()}
            accessibilityLabel={t("server.back", "Back")}
          />
        }
        right={
          <IconButton
            name="add"
            onPress={() => router.push("/server-settings/new")}
            accessibilityLabel={t("server.add", "Add server")}
          />
        }
      />
      <ScrollView
        className="flex-1"
        contentContainerClassName="px-4 py-4 gap-6"
      >
        <View className="gap-2">
          <Text className="text-xs uppercase tracking-wider text-muted-foreground px-1">
            {t("server.title", "Servers")}
          </Text>
          <View className="rounded-md border border-border bg-card overflow-hidden">
            {servers.map((entry, idx) => (
              <View key={entry.id}>
                <ServerRow
                  entry={entry}
                  isActive={entry.id === active.id}
                  iconColor={mutedFg}
                  onPress={() => onSelect(entry)}
                  onEdit={() => router.push(`/server-settings/${entry.id}`)}
                  onDelete={() => onDelete(entry)}
                />
                {idx < servers.length - 1 ? <Separator /> : null}
              </View>
            ))}
          </View>
          <Text className="text-xs text-muted-foreground px-1">
            {t(
              "server.hint",
              "Tap a server to connect to it. Each server keeps its own signed-in session on this device.",
            )}
          </Text>
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

/**
 * 「…」菜单刻意是行 Pressable 的**兄弟节点**而非子节点。
 *
 * 之前的结构把它嵌在设了 `disabled={isActive}` 的 Pressable 里,而 RN 的
 * `disabled` 会经 usePressability 吞掉整棵子树的手势响应 —— 当前生效的
 * 自定义服务器因此连编辑入口都点不动(QA 评审 P1)。拆成兄弟节点后,
 * 行的可点状态与菜单的可用性彻底解耦,`disabled` 也就不再需要:
 * `onSelect` 内部对「点的就是当前项」已经直接 return。
 */
function ServerRow({
  entry,
  isActive,
  iconColor,
  onPress,
  onEdit,
  onDelete,
}: {
  entry: ServerEntry;
  isActive: boolean;
  iconColor: string;
  onPress: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const { t } = useT("settings");
  return (
    <View className="flex-row items-center pr-2">
      <Pressable
        onPress={onPress}
        className="flex-1 flex-row items-center px-4 py-3.5 active:bg-secondary gap-3"
      >
        <View className="flex-1">
          <View className="flex-row items-center gap-2">
            <Text className="text-base font-medium text-foreground">
              {entry.name || entry.apiUrl}
            </Text>
            {entry.builtIn ? (
              <View className="rounded bg-muted px-1.5 py-0.5">
                <Text className="text-xs text-muted-foreground">
                  {t("server.built_in", "Built-in")}
                </Text>
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
      </Pressable>
      {/* 内置项不可编辑不可删,直接不出菜单 —— 没有可点的入口比置灰更清楚。 */}
      {entry.builtIn ? null : (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <IconButton
              name="ellipsis-horizontal"
              iconSize={18}
              color={iconColor}
              accessibilityLabel={t("server.row_actions", {
                name: entry.name || entry.apiUrl,
              })}
            />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-40">
            <DropdownMenuItem onPress={onEdit}>
              <Text>{t("server.edit", "Edit")}</Text>
            </DropdownMenuItem>
            {/* 当前生效项不给删除 —— 删掉正在用的服务器会造成一次隐式的
                地址变化,行为不可预期。用户需要先切走再删。 */}
            {isActive ? null : (
              <DropdownMenuItem variant="destructive" onPress={onDelete}>
                <Text>{t("server.delete", "Delete")}</Text>
              </DropdownMenuItem>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </View>
  );
}
