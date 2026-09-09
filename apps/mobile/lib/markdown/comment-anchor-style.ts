/**
 * 评论锚点在 enriched 里的 chip 外观（RUYI-108）。
 *
 * enriched 不允许为任何叶子节点注入 React（issues #54 / #232 / #246），Web 的
 * `CommentMentionCard` 组件在手机端没有挂载点。它给的替代能力是 `linkVariants`：
 * 按 URL 正则给单条链接换配色。chip 感因此由两半拼成——`preprocess.ts` 加 💬
 * 前缀补上图标位，这里补上底色并去掉下划线。
 *
 * 单独成文件而不是内联进 `markdown-style.ts`，是因为那份 style 是个读 RN 主题的
 * hook，进不了手机端 node-only 的 vitest lane；而这里恰恰有一处**必须**被测的
 * 东西：正则一旦与 `resolveLinkAction` 认的 URL 形态对不上，chip 会静默退化成
 * 普通下划线链接——点击照常工作，外观全错，正是本次返工要修的缺陷。
 */
/**
 * 匹配评论锚点 URL 的正则源码串（交给原生编译，不是 JS RegExp 实例）。
 *
 * 锚在 scheme 开头，正文里恰好包含这个字符串的外链不会被误染。
 * 刻意不使用后行断言：iOS 的 NSRegularExpression 不支持，库只会打印警告，
 * 然后这条 variant 永远不命中。
 */
export const COMMENT_ANCHOR_URL_PATTERN = "^mention://comment/";

export interface CommentAnchorLinkVariant {
  color: string;
  underline: boolean;
  backgroundColor: string;
}

/** 本 variant 用到的主题字段，写成结构性依赖，好让 node lane 直接传 THEME。 */
export interface CommentAnchorTheme {
  foreground: string;
  surface2: string;
}

/**
 * 底色取 `surface2` 而非品牌色：同一段落里往往还有真正的外链，chip 抢不过它们
 * 才对——Web 的 chip 外壳同样是中性的。前景色用 `foreground`，与 chip 内的正文
 * 同色，读起来是「一块被引用的东西」，不是「一个可跳走的链接」。
 */
export function commentAnchorLinkVariant(
  theme: CommentAnchorTheme,
): CommentAnchorLinkVariant {
  return {
    color: theme.foreground,
    underline: false,
    backgroundColor: theme.surface2,
  };
}
