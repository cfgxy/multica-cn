// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  COMMENT_ANCHOR_URL_PATTERN,
  commentAnchorLinkVariant,
} from "./comment-anchor-style";
import { resolveLinkAction } from "./link-route";

/**
 * 手机端评论 chip 的样式契约（RUYI-108）。
 *
 * 这里守的是一个会静默失效的耦合：染色由原生按 URL 正则做，跳转由
 * `resolveLinkAction` 按前缀判断，两者各写各的。正则一旦与路由认的形态错开，
 * chip 退化成普通下划线链接——点击照常工作，看不出报错，只是「Mobile 没有
 * chip」，正是本轮返工要修的那条。
 */
const UUID = "019f49e2-5b07-7970-beef-c0d537fb8c1d";
const re = new RegExp(COMMENT_ANCHOR_URL_PATTERN);

describe("评论锚点 linkVariant", () => {
  it("路由判定为评论锚点的 URL，一定也会被染色", () => {
    const url = `mention://comment/${UUID}`;
    expect(resolveLinkAction(url, "ws")).toEqual({
      kind: "commentAnchor",
      commentId: UUID,
    });
    expect(re.test(url)).toBe(true);
  });

  it("不染色任何非评论锚点，包括正文里恰好含该串的外链", () => {
    for (const url of [
      `mention://issue/${UUID}`,
      `mention://agent/${UUID}`,
      "https://example.com/mention://comment/x",
      "/api/attachments/x/download",
    ]) {
      expect(re.test(url)).toBe(false);
    }
  });

  it("不使用后行断言（iOS 的 NSRegularExpression 不支持，会静默不命中）", () => {
    expect(COMMENT_ANCHOR_URL_PATTERN).not.toMatch(/\(\?<[=!]/);
  });

  // 主题取值用夹具而不是真的 `THEME`：`lib/theme.ts` 导入
  // `@react-navigation/native`，在手机端 node-only lane 里加载不了。真实 token
  // 的明暗差异是 theme.ts 自己的职责，这里只钉住「配色从主题来，不写死」。
  it("底色与文字色全部取自传入主题，不写死颜色", () => {
    for (const theme of [
      { foreground: "#111111", surface2: "#eeeeee" },
      { foreground: "#fafafa", surface2: "#303030" },
    ]) {
      const variant = commentAnchorLinkVariant(theme);
      // 无下划线 + 实心底色，是 chip 与普通链接唯一的视觉分界。
      expect(variant.underline).toBe(false);
      expect(variant.backgroundColor).toBe(theme.surface2);
      expect(variant.color).toBe(theme.foreground);
    }
  });
});
