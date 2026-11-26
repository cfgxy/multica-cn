import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Attachment, TimelineEntry } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";

const { openExternalMock } = vi.hoisted(() => ({
  openExternalMock: vi.fn(),
}));

vi.mock("@multica/core/config", () => ({
  useConfigStore: (selector: (s: { cdnDomain: string; cdnSigned: boolean; featureFlags: Record<string, boolean> }) => unknown) =>
    selector({ cdnDomain: "", cdnSigned: false, featureFlags: {} }),
  configStore: { getState: () => ({ cdnDomain: "", cdnSigned: false, featureFlags: {} }) },
}));

vi.mock("@multica/core/api", () => ({
  api: {
    getAttachmentTextContent: vi.fn(),
    getAttachment: vi.fn(),
    getAttachmentBlob: vi.fn(),
    getBaseUrl: () => "",
  },
  dispatchReasonCode: () => undefined,
  errorCode: () => undefined,
  PreviewTooLargeError: class extends Error {},
  PreviewUnsupportedError: class extends Error {},
}));

vi.mock("../../platform", () => ({
  openExternal: openExternalMock,
}));

vi.mock("../../navigation", () => ({
  useNavigation: () => ({
    push: vi.fn(),
    pathname: "/acme/issues",
    getShareableUrl: (p: string) => `https://app.example${p}`,
  }),
  useOptionalNavigation: () => ({
    push: vi.fn(),
    openInNewTab: vi.fn(),
    getShareableUrl: (p: string) => `https://app.example${p}`,
  }),
  useAppOrigin: () => null,
  resolveClickIntent: () => "push",
  AppLink: ({ href, children }: { href: string; children: React.ReactNode }) => (
    <a href={href}>{children}</a>
  ),
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => null,
  useWorkspaceSlug: () => "acme",
  useWorkspacePaths: () => ({
    issueDetail: (id: string) => `/acme/issues/${id}`,
    projectDetail: (id: string) => `/acme/projects/${id}`,
  }),
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: () => "Reviewer" }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

vi.mock("../hooks/use-comment-trigger-preview", () => ({
  useCommentTriggerPreview: () => ({ agents: [], blocked: [] }),
}));

vi.mock("../hooks", () => ({
  useResolveIssueIdentifier: () => null,
}));

vi.mock("../../editor/link-hover-card", () => ({
  useLinkHover: () => ({}),
  LinkHoverCard: () => null,
}));

import { CommentCard } from "./comment-card";

function makeAttachment(overrides: Partial<Attachment> & { id: string }): Attachment {
  return {
    workspace_id: "ws-1",
    issue_id: "issue-1",
    comment_id: "comment-1",
    chat_session_id: null,
    chat_message_id: null,
    uploader_type: "agent",
    uploader_id: "agent-1",
    filename: "demo.png",
    url: `https://cdn.example.test/${overrides.id}.png`,
    download_url: `https://cdn.example.test/${overrides.id}.png?Signature=ok`,
    markdown_url: `https://cdn.example.test/api/attachments/${overrides.id}/download`,
    content_type: "image/png",
    size_bytes: 128,
    created_at: "2026-08-05T00:00:00Z",
    ...overrides,
  };
}

function makeComment(overrides: Partial<TimelineEntry> = {}): TimelineEntry {
  return {
    id: "comment-1",
    issue_id: "issue-1",
    parent_id: null,
    actor_type: "agent",
    actor_id: "agent-1",
    content: "",
    type: "comment",
    created_at: "2026-08-05T00:00:00Z",
    updated_at: "2026-08-05T00:00:00Z",
    revision: 1,
    attachments: [],
    reactions: [],
    ...overrides,
  } as unknown as TimelineEntry;
}

function renderCommentCard(
  entry: TimelineEntry,
  options?: { issueAttachments?: Attachment[]; replies?: TimelineEntry[] },
) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <CommentCard
        issueId="issue-1"
        entry={entry}
        issueAttachments={options?.issueAttachments}
        replies={options?.replies ?? []}
        currentUserId="user-1"
        onReply={vi.fn().mockResolvedValue(true)}
        onEdit={vi.fn().mockResolvedValue(undefined)}
        onDelete={vi.fn()}
        onToggleReaction={vi.fn()}
      />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("CommentCard — attachment:// protocol resolution and dedup", () => {
  it("renders inline attachment:// image once with resolved URL and suppresses duplicate bottom card", () => {
    const att = makeAttachment({
      id: "11111111-1111-4111-8111-111111111111",
      filename: "demo.png",
      download_url: "https://cdn.example.test/demo-signed.png?Signature=fresh",
    });
    const entry = makeComment({
      content: "Check result:\n\n![demo](attachment://demo.png)",
      attachments: [att],
    });

    const { container } = renderCommentCard(entry);
    const imgs = container.querySelectorAll("img");
    expect(imgs).toHaveLength(1);
    expect(imgs[0]?.getAttribute("src")).toBe(
      "https://cdn.example.test/demo-signed.png?Signature=fresh",
    );
  });

  it("still renders standalone attachment below when attachment:// only appears in inline code", () => {
    const att = makeAttachment({
      id: "11111111-1111-4111-8111-111111111111",
      filename: "demo.png",
      download_url: "https://cdn.example.test/demo-signed.png?Signature=fresh",
    });
    const entry = makeComment({
      content: "Use `![demo](attachment://demo.png)` in markdown.",
      attachments: [att],
    });

    const { container } = renderCommentCard(entry);
    const imgs = container.querySelectorAll("img");
    expect(imgs).toHaveLength(1);
    expect(imgs[0]?.getAttribute("src")).toBe(
      "https://cdn.example.test/demo-signed.png?Signature=fresh",
    );
  });

  it("resolves cross-comment attachment:// via issueAttachments without adding it to bottom AttachmentList", () => {
    const earlier = makeAttachment({
      id: "22222222-2222-4222-8222-222222222222",
      comment_id: "comment-0",
      filename: "earlier.png",
      download_url: "https://cdn.example.test/earlier.png?Signature=ok",
    });
    const ownStandalone = makeAttachment({
      id: "33333333-3333-4333-8333-333333333333",
      comment_id: "comment-1",
      filename: "notes.txt",
      content_type: "text/plain",
      url: "https://cdn.example.test/notes.txt",
      download_url: "https://cdn.example.test/notes.txt?Signature=ok",
      markdown_url: "https://cdn.example.test/api/attachments/33333333-3333-4333-8333-333333333333/download",
    });
    const entry = makeComment({
      content: "Summary referencing ![earlier](attachment://earlier.png)",
      attachments: [ownStandalone],
    });

    const { container } = renderCommentCard(entry, {
      issueAttachments: [earlier, ownStandalone],
    });

    const imgs = container.querySelectorAll("img");
    expect(imgs).toHaveLength(1);
    expect(imgs[0]?.getAttribute("src")).toBe(
      "https://cdn.example.test/earlier.png?Signature=ok",
    );
    expect(screen.getByText("notes.txt")).toBeInTheDocument();
  });

  it("prefers entry.attachments over issueAttachments on filename collision and propagates to replies", () => {
    const issueSame = makeAttachment({
      id: "44444444-4444-4444-8444-444444444444",
      comment_id: "comment-0",
      filename: "same.png",
      download_url: "https://cdn.example.test/from-issue.png?Signature=1",
    });
    const ownSame = makeAttachment({
      id: "55555555-5555-4555-8555-555555555555",
      comment_id: "comment-1",
      filename: "same.png",
      download_url: "https://cdn.example.test/from-own.png?Signature=2",
    });
    const reply = makeComment({
      id: "reply-1",
      parent_id: "comment-1",
      content: "Reply referencing ![cross](attachment://cross-only.png)",
      attachments: [],
    });
    const crossOnly = makeAttachment({
      id: "66666666-6666-4666-8666-666666666666",
      comment_id: "comment-0",
      filename: "cross-only.png",
      download_url: "https://cdn.example.test/cross-only.png?Signature=3",
    });
    const root = makeComment({
      content: "Root ![same](attachment://same.png)",
      attachments: [ownSame],
    });

    const { container } = renderCommentCard(root, {
      issueAttachments: [issueSame, ownSame, crossOnly],
      replies: [reply],
    });

    const srcs = [...container.querySelectorAll("img")].map((img) =>
      img.getAttribute("src"),
    );
    expect(srcs).toEqual([
      "https://cdn.example.test/from-own.png?Signature=2",
      "https://cdn.example.test/cross-only.png?Signature=3",
    ]);
  });
});
