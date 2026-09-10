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

vi.mock("sonner", () => ({ toast: { error: toastError } }));

vi.mock("../issues/hooks", () => ({
  useResolveIssueIdentifier: () => null,
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
