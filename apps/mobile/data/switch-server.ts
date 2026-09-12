/**
 * 服务器切换的会话编排 —— 从 `app/server-settings/index.tsx` 的 doSwitch
 * 提取（RUYI-131）。
 *
 * 提取的唯一原因是本次新增了第二个调用点：跨服务器通知点击后要先恢复目标
 * 服务器会话再落地 Issue（`components/notifications/notification-response-
 * navigator.tsx`）。两个调用点的会话序列必须完全一致，否则「从设置页切」
 * 与「从通知切」会出现两套 token/缓存行为。
 *
 * 落地路由**不在**这里决定：设置页落 inbox，通知落目标 Issue。函数只把
 * 切换后的会话事实（失败 / 无会话 / 有会话+已恢复 slug）交回调用方。
 */
import type { QueryClient } from "@tanstack/react-query";

import { api } from "./api";
import { useAuthStore } from "./auth-store";
import { useServerStore } from "./server-store";
import { useWorkspaceStore } from "./workspace-store";

export type ServerSwitchOutcome =
  /** 落盘失败 —— 用户仍留在原服务器会话里，未做任何破坏性动作。 */
  | { kind: "failed"; error: unknown }
  /** 切换成功，但目标服务器没有可恢复的会话（或 getMe 失败）。 */
  | { kind: "signed-out" }
  /** 切换成功且会话已恢复；slug 为该服务器持久化的最后使用空间。 */
  | { kind: "signed-in"; slug: string | null };

/**
 * 切到目标服务器并恢复其会话。
 *
 * 顺序与原 doSwitch 完全一致：
 *   1. 先落盘（失败即原地返回，不动会话）；
 *   2. 立即丢弃内存中的旧 token —— 地址已指向新服务器，继续带旧 token
 *      发请求会以「B 返回 401」触发全局登出，反过来毁掉 B 的已存快照；
 *   3. 清 React Query 缓存 —— 不同后端数据互不相通，漏清会把 A 的缓存
 *      渲染在 B 的会话里；
 *   4. 重跑 initialize() 按目标服务器恢复 token + slug + user。
 */
export async function switchServer(
  serverId: string,
  queryClient: QueryClient,
): Promise<ServerSwitchOutcome> {
  try {
    await useServerStore.getState().setActiveServer(serverId);
  } catch (error) {
    return { kind: "failed", error };
  }
  api.setToken(null);
  queryClient.clear();
  await useAuthStore.getState().initialize();
  const { user } = useAuthStore.getState();
  if (!user) return { kind: "signed-out" };
  return {
    kind: "signed-in",
    slug: useWorkspaceStore.getState().currentWorkspaceSlug,
  };
}
