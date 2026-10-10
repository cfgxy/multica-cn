import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../test/i18n";
import { NavigationProvider } from "../navigation/context";
import type { NavigationAdapter } from "../navigation/types";
import {
  CurrentIssueRenderContextProvider,
  type CurrentIssueRenderContextValue,
  type ResolvedAnchorComment,
} from "../issues/current-issue-render-context";

const toastError = vi.hoisted(() => vi.fn());

/**
 * 服务端跨单锚点解析结果（RUYI-643）。宿主本地解析不到时，
 * CommentMentionCard 会经 useResolveCommentAnchor 探测目标评论；
 * 测试用这个可变槽位扮演服务端，默认 null（不可达）。
 */
const commentAnchorState = vi.hoisted(() => ({
  value: null as {
    issue_id: string;
    identifier: string;
    author_type: string;
    author_id: string;
    created_at: string;
    excerpt: string;
  } | null,
}));

vi.mock("sonner", () => ({ toast: { error: toastError } }));

vi.mock("../issues/hooks", () => ({
  useResolveIssueIdentifier: () => null,
  useResolveCommentAnchor: () => commentAnchorState.value,
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (_type: string, _id: string) => "顾小鱼",
  }),
}));

vi.mock("../issues/components/issue-hover-card", () => ({
  IssueHoverCard: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

vi.mock("../editor/link-hover-card", () => ({
  useLinkHover: () => ({}),
  LinkHoverCard: () => null,
}));

vi.mock("@multica/core/api", () => ({
  api: { getAttachmentTextContent: vi.fn() },
  PreviewTooLargeError: class extends Error {},
  PreviewUnsupportedError: class extends Error {},
}));

vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useWorkspacePaths: () => ({
    issueDetail: (id: string) => `/acme/issues/${id}`,
    projectDetail: (id: string) => `/acme/projects/${id}`,
  }),
  useWorkspaceSlug: () => "acme",
}));

vi.mock("mermaid", () => ({
  default: { initialize: vi.fn(), render: vi.fn() },
}));

import { RichContent } from "./rich-content";

const ISSUE_ID = "11111111-1111-4111-8111-111111111111";
const COMMENT_ID = "33333333-3333-4333-8333-333333333333";
const MISSING_ID = "44444444-4444-4444-8444-444444444444";
const OTHER_ISSUE_ID = "22222222-2222-4222-8222-222222222222";

const RESOLVED: ResolvedAnchorComment = {
  id: COMMENT_ID,
  author: "顾小鱼",
  createdAt: new Date("2026-09-08T10:00:00Z").toISOString(),
  excerpt: "锚点落地走既有 landing effect",
};

function adapter(): NavigationAdapter {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (path) => `https://app.example${path}`,
  };
}

function renderAnchor(
  context: CurrentIssueRenderContextValue | null,
  commentId = COMMENT_ID,
) {
  return renderWithI18n(
    <NavigationProvider value={adapter()}>
      {context ? (
        <CurrentIssueRenderContextProvider value={context}>
          <RichContent content={`见 [某条评论](mention://comment/${commentId})`} />
        </CurrentIssueRenderContextProvider>
      ) : (
        <RichContent content={`见 [某条评论](mention://comment/${commentId})`} />
      )}
    </NavigationProvider>,
  );
}

/** 只解析 COMMENT_ID 的宿主：其余一律 null，模拟「本端手上没有这条」。 */
function hostContext(
  requestCommentFocus = vi.fn(),
): CurrentIssueRenderContextValue {
  return {
    id: ISSUE_ID,
    identifier: "MUL-7",
    resolveComment: (id) => (id === COMMENT_ID ? RESOLVED : null),
    requestCommentFocus,
  };
}

beforeEach(() => {
  toastError.mockClear();
  commentAnchorState.value = null;
});

describe("mention://comment 锚点渲染", () => {
  it("能解析时渲染为可点击 chip，点击把定位请求交回宿主", async () => {
    const focus = vi.fn();
    renderAnchor(hostContext(focus));

    const chip = screen.getByRole("button", { name: /顾小鱼/ });
    // 标签用作者写的链接文字，不被摘要覆盖——引用者选的措辞比机器摘要更贴题。
    expect(chip).toHaveTextContent("某条评论");
    // 无障碍名里必须带作者与摘要，屏幕阅读器要在跳之前知道跳到哪。
    expect(chip.getAttribute("aria-label")).toContain(RESOLVED.excerpt);

    await userEvent.click(chip);
    expect(focus).toHaveBeenCalledWith(COMMENT_ID);
  });

  it("解析不到时降级为不可点击 chip，只提示不可用，不泄露任何评论内容", async () => {
    const focus = vi.fn();
    renderAnchor(hostContext(focus), MISSING_ID);

    // 降级态不是 button：没有可执行的动作，不该出现在 tab 序列里。
    expect(screen.queryByRole("button")).toBeNull();
    const chip = screen.getByText("某条评论");
    await userEvent.click(chip);

    expect(focus).not.toHaveBeenCalled();
    expect(toastError).toHaveBeenCalledTimes(1);
    // 「删了／没权限／别的单／还没加载」四种 miss 对外必须一模一样。
    expect(document.body.textContent).not.toContain(RESOLVED.author);
    expect(document.body.textContent).not.toContain(RESOLVED.excerpt);
  });

  it("没有宿主 context 时（如 inbox 预览）同样降级，不崩", () => {
    renderAnchor(null);

    expect(screen.queryByRole("button")).toBeNull();
    expect(screen.getByText("某条评论")).toBeInTheDocument();
  });

  it("宿主知道 issue 但不托管评论列表时降级", () => {
    renderAnchor({ id: ISSUE_ID, identifier: "MUL-7" });

    expect(screen.queryByRole("button")).toBeNull();
    expect(screen.getByText("某条评论")).toBeInTheDocument();
  });
});

describe("跨单评论引用解析（RUYI-643）", () => {
  /** 宿主本地解析不到（引用目标在别的 issue），走服务端锚点探测。 */
  function renderCrossIssue(content: string) {
    const nav = adapter();
    renderWithI18n(
      <NavigationProvider value={nav}>
        <CurrentIssueRenderContextProvider value={hostContext()}>
          <RichContent content={content} />
        </CurrentIssueRenderContextProvider>
      </NavigationProvider>,
    );
    return nav;
  }

  it("服务端锚点命中（他单）→ 渲染为指向目标单评论的链接，点击 SPA 跳转", async () => {
    commentAnchorState.value = {
      issue_id: OTHER_ISSUE_ID,
      identifier: "RUYI-587",
      author_type: "member",
      author_id: "user-1",
      created_at: RESOLVED.createdAt,
      excerpt: RESOLVED.excerpt,
    };
    const nav = renderCrossIssue(
      `见 [RUYI-587 回传记录](mention://comment/${MISSING_ID})`,
    );

    // 跨单跳转是真实导航：URL 变化、可新开 tab，所以是 link 不是 button。
    const link = screen.getByRole("link", { name: /顾小鱼/ });
    expect(link).toHaveAttribute(
      "href",
      `/acme/issues/RUYI-587#comment-${MISSING_ID}`,
    );
    // 标签仍用作者写的链接文字；aria 名带作者与摘要，与同单 chip 对齐。
    expect(link).toHaveTextContent("RUYI-587 回传记录");
    expect(link.getAttribute("aria-label")).toContain(RESOLVED.excerpt);

    await userEvent.click(link);
    expect(nav.push).toHaveBeenCalledWith(
      `/acme/issues/RUYI-587#comment-${MISSING_ID}`,
    );
  });

  it("服务端锚点也未命中 → 保持降级虚线 chip，不产出链接、不泄露内容", async () => {
    commentAnchorState.value = null;
    const nav = renderCrossIssue(
      `见 [某条评论](mention://comment/${MISSING_ID})`,
    );

    expect(screen.queryByRole("link")).toBeNull();
    expect(screen.queryByRole("button")).toBeNull();
    const chip = screen.getByText("某条评论");
    await userEvent.click(chip);

    expect(nav.push).not.toHaveBeenCalled();
    expect(toastError).toHaveBeenCalledTimes(1);
    expect(document.body.textContent).not.toContain(RESOLVED.author);
    expect(document.body.textContent).not.toContain(RESOLVED.excerpt);
  });

  it("锚点命中的目标就在本单（本地未加载的评论）→ 统一走链接跳转落地", () => {
    commentAnchorState.value = {
      issue_id: ISSUE_ID,
      identifier: "MUL-7",
      author_type: "member",
      author_id: "user-1",
      created_at: RESOLVED.createdAt,
      excerpt: RESOLVED.excerpt,
    };
    renderCrossIssue(`见 [某条评论](mention://comment/${MISSING_ID})`);

    const link = screen.getByRole("link", { name: /顾小鱼/ });
    expect(link).toHaveAttribute(
      "href",
      `/acme/issues/MUL-7#comment-${MISSING_ID}`,
    );
  });
});
