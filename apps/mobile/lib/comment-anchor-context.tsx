/**
 * 评论锚点点击的下发通道（RUYI-108）。
 *
 * `mention://comment/<id>` 的点击发生在 Markdown 渲染树的最深处，而落地
 * 动作（展开 root、滚动定位、闪高亮）只有时间线拿得到列表状态。中间隔着
 * `EnrichedMarkdownText` 这层原生渲染——它不接受注入 React 子节点，只回调
 * 一个 `onLinkPress({url})`，所以链接与时间线之间无法靠 props 串起来。
 *
 * 同 `LightboxProvider` 的取法：一个极薄的 context，时间线挂 provider，
 * markdown 消费。默认实现是 no-op——评论正文在别的屏（如评论目录、聊天）
 * 里也会渲染，那里没有可定位的时间线，点击应当安静地什么都不做，而不是
 * 让渲染层崩掉。
 */
import { createContext, use, type ReactNode } from "react";

export interface CommentAnchorApi {
  /** 请求定位到本任务单内的某条评论；目标不可用时由实现方给降级反馈。 */
  focus: (commentId: string) => void;
}

const NOOP: CommentAnchorApi = { focus: () => {} };

const CommentAnchorContext = createContext<CommentAnchorApi>(NOOP);

export function useCommentAnchor(): CommentAnchorApi {
  return use(CommentAnchorContext);
}

export function CommentAnchorProvider({
  value,
  children,
}: {
  value: CommentAnchorApi;
  children: ReactNode;
}) {
  return (
    <CommentAnchorContext.Provider value={value}>
      {children}
    </CommentAnchorContext.Provider>
  );
}
