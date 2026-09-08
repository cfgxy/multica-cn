import { describe, expect, it } from "vitest";
import { resolveLinkAction } from "./link-route";

/**
 * 链接路由决策（RUYI-72）。
 *
 * 表格被拆出原生 markdown 渲染后，行详情里的字段值是独立的 enriched 实例，
 * 必须承接与散文完全相同的 `onLinkPress` 语义。把决策抽成纯函数后，
 * 「mention 内部跳转 / 附件重签 / 其余交系统」三条分支才有测试保障，
 * 而不是靠两处渲染代码各自复制一遍。
 */
describe("resolveLinkAction", () => {
  it("mention://issue 走 App 内路由", () => {
    expect(resolveLinkAction("mention://issue/abc", "ws")).toEqual({
      kind: "route",
      path: "/ws/issue/abc",
    });
  });

  it("mention://project 走 App 内路由", () => {
    expect(resolveLinkAction("mention://project/p1", "ws")).toEqual({
      kind: "route",
      path: "/ws/project/p1",
    });
  });

  it("mention://comment 解析为同任务单内的评论锚点", () => {
    expect(resolveLinkAction("mention://comment/c1", "ws")).toEqual({
      kind: "commentAnchor",
      commentId: "c1",
    });
  });

  it("评论锚点不依赖 workspace slug（定位发生在当前任务单内）", () => {
    expect(resolveLinkAction("mention://comment/c1", null)).toEqual({
      kind: "commentAnchor",
      commentId: "c1",
    });
  });

  it("没有详情页的 mention 类型静默忽略，绝不交给系统", () => {
    for (const url of [
      "mention://member/m1",
      "mention://agent/a1",
      "mention://squad/s1",
      "mention://all/all",
      "mention://issue",
      "mention://comment",
      "mention://comment/",
    ]) {
      expect(resolveLinkAction(url, "ws")).toEqual({ kind: "noop" });
    }
  });

  it("缺少 workspace slug 时 mention 不跳转（拼不出路由）", () => {
    expect(resolveLinkAction("mention://issue/abc", null)).toEqual({
      kind: "noop",
    });
  });

  it("附件链接识别为附件动作，带出附件 id", () => {
    const id = "11111111-2222-3333-4444-555555555555";
    const url = `/api/attachments/${id}/download`;
    expect(resolveLinkAction(url, "ws")).toEqual({
      kind: "attachment",
      id,
      fallbackUrl: url,
    });
  });

  it("http(s) 等外部链接交给系统打开", () => {
    expect(resolveLinkAction("https://example.com/a", "ws")).toEqual({
      kind: "external",
      url: "https://example.com/a",
    });
    expect(resolveLinkAction("mailto:a@b.c", "ws")).toEqual({
      kind: "external",
      url: "mailto:a@b.c",
    });
  });
});
