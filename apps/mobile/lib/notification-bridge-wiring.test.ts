// @vitest-environment node
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * 通知跨服务器/空间中转的接线（RUYI-131）。
 *
 * 判定矩阵本身已在 `notification-target.test.ts` 按纯函数覆盖。这里只钉住
 * 「判定确实被接上」——navigator 依赖 expo-router / expo-notifications /
 * RN Alert，publisher 依赖 expo-notifications 原生模块，两者都跑不进 node
 * lane，源码断言是本仓能观察到接线断裂的唯一层。
 *
 * 要钉住的四件事：
 *   1. 点击端用**实时身份**（切换可能发生在停靠期间）做判定，而不是入队时
 *      的快照；
 *   2. 跨服务器落地复用 `switchServer` 的会话序列，而不是另写一份；
 *   3. 发布端把 server_id 写进 data —— 漏掉这一项，判定永远退化成「同服务器」，
 *      跨服务器 404 原样复现且单测全绿；
 *   4. 跨空间用 push、跨服务器用 replace（后者清了 query 缓存，旧栈已失效）。
 */
function source(relative: string): string {
  return readFileSync(
    resolve(dirname(fileURLToPath(import.meta.url)), relative),
    "utf8",
  );
}

const navigator = source(
  "../components/notifications/notification-response-navigator.tsx",
);
const publisher = source("../data/notifications/present-inbox-notification.ts");
const realtime = source("../data/realtime/use-notification-realtime.ts");
const serverSettings = source("../app/server-settings/index.tsx");

describe("通知中转接线", () => {
  it("点击端把判定交给 resolveNotificationTap，不再自己拼路由", () => {
    expect(navigator).toContain("resolveNotificationTap(");
    // 旧实现直接 push 字符串模板，跨身份时就是 404 的来源。
    expect(navigator).not.toContain("`/(app)/${workspaceSlug}/issue/");
  });

  it("判定读实时身份 getState()，而不是停靠时的快照", () => {
    expect(navigator).toContain(
      "activeServerId: useServerStore.getState().activeServerId",
    );
    expect(navigator).toContain("useWorkspaceStore.getState().currentWorkspaceSlug");
    expect(navigator).toContain("servers: useServerStore.getState().servers");
  });

  it("判定发生在 flush 之后：停靠分支仍在 resolve 之前", () => {
    const park = navigator.indexOf("if (!navReadyRef.current) {");
    const resolve = navigator.indexOf("resolveNotificationTap(");
    expect(park).toBeGreaterThan(-1);
    expect(resolve).toBeGreaterThan(-1);
    // 顺序反了，冷启动 tap 会在路由树就绪前拿到身份并弹窗，push 静默失效。
    expect(park).toBeLessThan(resolve);
  });

  it("跨服务器落地复用共享的 switchServer 会话序列", () => {
    expect(navigator).toContain("switchServer(action.serverId, qc)");
    // 设置页是第二个调用点：两处必须是同一份实现，否则会话行为漂移。
    expect(serverSettings).toContain("switchServer(entry.id, qc)");
    expect(navigator).not.toContain("setActiveServer(");
  });

  it("跨空间 push、跨服务器 replace", () => {
    expect(navigator).toContain('navigate("push", action.route)');
    expect(navigator).toContain('navigate("replace"');
  });

  it("发布端把服务器身份写进通知 data", () => {
    expect(publisher).toContain("server_id: origin.serverId");
    expect(publisher).toContain("workspace_slug: origin.workspaceSlug");
  });

  it("realtime 把当前 activeServerId 作为发布身份传下去", () => {
    expect(realtime).toContain("serverId,");
    expect(realtime).toContain("presentInboxNotification(item, body, {");
  });
});
