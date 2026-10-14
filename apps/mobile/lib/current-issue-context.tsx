/**
 * 当前详情页的 Issue id（RUYI-731 自引过滤的移动端通道）。
 *
 * Markdown 渲染树在 issue 详情、聊天、收件箱等多个场景复用，引用尾块
 * （IssueReferenceList）只有挂在详情页语境里才知道「正文正在描述哪一
 * 个 Issue」。默认实现是 null——非详情场景不做自引过滤，引用行照常渲染；
 * 只有显式挂了 provider 的屏才启用「解析后 id 等于当前 Issue 的行不渲染」
 * 语义。同 `CommentAnchorProvider` 的取法：一个极薄的 context，屏挂
 * provider，渲染树深处消费，props 不用逐层穿透。
 */
import { createContext, use, type ReactNode } from "react";

const CurrentIssueContext = createContext<string | null>(null);

export function useCurrentIssueId(): string | null {
  return use(CurrentIssueContext);
}

export function CurrentIssueIdProvider({
  value,
  children,
}: {
  value: string | null;
  children: ReactNode;
}) {
  return (
    <CurrentIssueContext.Provider value={value}>
      {children}
    </CurrentIssueContext.Provider>
  );
}
