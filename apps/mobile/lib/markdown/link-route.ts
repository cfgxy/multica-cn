/**
 * Markdown 链接点击的路由决策（纯函数）。
 *
 * 原本这段判断内联在 `markdown.tsx` 的 `onLinkPress` 里。RUYI-72 把表格
 * 拆出原生渲染后出现了第二个 enriched 实例（行详情里的字段值），两处必须
 * 共用同一套语义，否则表格里的链接行为会与散文里不一致。抽成纯函数还带来
 * 一个直接好处：mention/附件/外链三条分支能进 mobile 的 node-only vitest
 * lane，不需要挂 RN 原生模块。
 *
 * 决策本身不做副作用，调用方拿到 action 后再决定 router.push / 附件重签 /
 * Linking.openURL——副作用留在 `markdown.tsx`，依赖注入的形态不变。
 */
import { attachmentIdFromUrl } from "@/lib/attachment-open";

export type LinkAction =
  /** App 内导航到 `path`。 */
  | { kind: "route"; path: string }
  /** 附件：需按 RUYI-73 重新签发 URL 再打开。 */
  | { kind: "attachment"; id: string; fallbackUrl: string }
  /**
   * 同任务单内的评论锚点（RUYI-108）：定位到当前 issue 时间线里的某条评论。
   *
   * 刻意不是 `route`——它不换页面，只在当前时间线里展开并高亮目标行，
   * 所以既不需要 workspace slug，也没有可跳转的 path。跨任务单不解析：
   * 消费方查不到该 id 时按降级反馈处理，不做网络探测（那会把一个
   * 无副作用的渲染锚点变成可探测存在性的接口）。
   */
  | { kind: "commentAnchor"; commentId: string }
  /** 交给系统打开（http(s) / mailto / tel / app scheme）。 */
  | { kind: "external"; url: string }
  /** 静默忽略。 */
  | { kind: "noop" };

export function resolveLinkAction(
  url: string,
  wsSlug: string | null,
): LinkAction {
  // `mention://` 是内部 scheme——绝不能交给系统。没有 App 注册它，
  // `Linking.openURL("mention://…")` 会弹 iOS 的 "Cannot open URL" 或
  // 静默失败。所有形态在这里穷举，一律不落到 external 分支：
  //
  //   mention://issue/<uuid>   → 跳转 issue 详情
  //   mention://project/<uuid> → 跳转 project 详情
  //   mention://comment/<uuid> → 当前任务单内定位评论（不换页）
  //   mention://member|agent|squad/<uuid> → 无对应页面，忽略
  //   mention://all/all        → 纯语义（「所有人」），忽略
  //   畸形                     → 忽略
  if (url.startsWith("mention://")) {
    const rest = url.slice("mention://".length);
    const slash = rest.indexOf("/");
    if (slash < 0) return { kind: "noop" };
    const type = rest.slice(0, slash);
    const id = rest.slice(slash + 1);
    if (!id) return { kind: "noop" };
    // 评论锚点在 slug 判断之前返回：定位发生在当前时间线内，不拼路由，
    // 因此不该被「拿不到 slug」这条针对跳转的前置条件连坐。
    if (type === "comment") return { kind: "commentAnchor", commentId: id };
    if (!wsSlug) return { kind: "noop" };
    // 手机端路由段是单数（`/issue/`、`/project/`），与 mention 类型同词但
    // 映射保持显式，新增类型不会静默走空。
    if (type === "issue") return { kind: "route", path: `/${wsSlug}/issue/${id}` };
    if (type === "project")
      return { kind: "route", path: `/${wsSlug}/project/${id}` };
    return { kind: "noop" };
  }

  // 附件链接：持久化的 `/api/attachments/<id>/download` 文件卡、行内附件
  // 引用、签名 URL、`mc://file/<id>`——点击时重新签发并携带自身凭据打开
  // （RUYI-73）。
  const attachmentId = attachmentIdFromUrl(url);
  if (attachmentId) {
    return { kind: "attachment", id: attachmentId, fallbackUrl: url };
  }

  return { kind: "external", url };
}
