// RUYI-483 — the upload settles after its Tiptap instance died (conditional
// editor remount inside a living composer), the QA-reproduced orphan shape.
//
// QA 实测序列（RUYI-474 终轮报告探针4，在 628790985 复现）：同一 composer 内
// 先发一条纯文本评论，再上传非图片文件（文件卡已写入正文），随后发送——评论
// content 只剩纯文本、附件 comment_id 为空（静默孤儿）。缺陷窗口：上传 settle
// 前编辑器实例经历一次条件重挂（宿主未必察觉），inline blob→URL swap 在已销毁
// 实例上 no-op，URL 永不进入 doc/draft；重挂后的实例从 draft 回灌出无 href 的
// 卡，提交时 contentReferencesAttachment 匹配不到 URL，attachment_ids 过滤丢 id。
//
// Host wiring mirrors comment-input.tsx exactly (same reference-filtered
// submit payload), so a failure here is the defect, a pass is the fix.
import type { RefObject } from "react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import { useCommentDraftStore } from "@multica/core/issues/stores";
import { contentReferencesAttachment, type Attachment } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enEditor from "../../locales/en/editor.json";
import {
  ContentEditor,
  useUploadGate,
  useComposerSubmit,
  type ContentEditorRef,
} from "../../editor";
import { useCommentUploads } from "./use-comment-uploads";

const mockApiUploadFile = vi.hoisted(() => vi.fn());
// 浏览器 provider 下原生 ESM 链接要求导出面完整：一律合并真实导出，仅覆盖测试关注面。
vi.mock("@multica/core/api", async (importActual) => {
  const actual = await importActual<typeof import("@multica/core/api")>();
  return { ...actual, api: { ...actual.api, uploadFile: mockApiUploadFile } };
});
vi.mock("sonner", async (importActual) => ({
  ...(await importActual<typeof import("sonner")>()),
  toast: { error: vi.fn(), success: vi.fn() },
}));
vi.mock("@tanstack/react-query", async (importActual) => ({
  ...(await importActual<typeof import("@tanstack/react-query")>()),
  useQueryClient: () => ({ fetchQuery: async () => null }),
  useQuery: () => ({ data: undefined, isLoading: false }),
}));
vi.mock("@multica/core/paths", async (importActual) => ({
  ...(await importActual<typeof import("@multica/core/paths")>()),
  useWorkspaceSlug: () => "ws",
}));
vi.mock("../../navigation", async (importActual) => ({
  ...(await importActual<typeof import("../../navigation")>()),
  useAppOrigin: () => "http://localhost:3000",
  resolveClickIntent: () => "same-tab",
}));
vi.mock("../../editor/bubble-menu", () => ({ EditorBubbleMenu: () => null }));
vi.mock("../../editor/link-hover-card", () => ({
  useLinkHover: () => ({}),
  LinkHoverCard: () => null,
}));

const TEST_RESOURCES = { en: { common: enCommon, editor: enEditor } };

const ISSUE_ID = "issue-ruyi-483-remount";
const DRAFT_KEY = `new:${ISSUE_ID}` as const;

let attSeq = 0;
function makeAttachment(filename: string, contentType: string): Attachment {
  attSeq += 1;
  const id = `att-ruyi-483r-${attSeq}`;
  return {
    id,
    workspace_id: "ws-1",
    uploader_type: "member",
    uploader_id: "user-1",
    filename,
    content_type: contentType,
    size_bytes: 1234,
    url: `/uploads/workspaces/ws-1/${id}.bin`,
    download_url: `/api/attachments/${id}/download`,
    markdown_url: `/api/attachments/${id}/download`,
    created_at: new Date(0).toISOString(),
  } as unknown as Attachment;
}

interface SubmittedComment {
  content: string;
  attachmentIds?: string[];
}

const submissions: SubmittedComment[] = [];

const harness: { submit: () => Promise<void>; remount: () => void } = {
  submit: async () => {},
  remount: () => {},
};

// jsdom lacks the rect APIs ProseMirror's scrollIntoView touches (same stubs
// as comment-second-submit-upload.test.tsx).
beforeEach(() => {
  const rect = () => new DOMRect(0, 0, 0, 0);
  const rectList = () =>
    ({ length: 0, item: () => null, [Symbol.iterator]: function* () {} }) as DOMRectList;
  Object.defineProperty(Range.prototype, "getBoundingClientRect", {
    configurable: true,
    value: rect,
  });
  Object.defineProperty(Range.prototype, "getClientRects", {
    configurable: true,
    value: rectList,
  });
  Object.defineProperty(HTMLElement.prototype, "getClientRects", {
    configurable: true,
    value: rectList,
  });
  Object.defineProperty(Text.prototype, "getClientRects", {
    configurable: true,
    value: rectList,
  });
  if (typeof URL.createObjectURL !== "function") {
    Object.defineProperty(URL, "createObjectURL", {
      configurable: true,
      value: vi.fn(() => "blob:test-remount"),
    });
    Object.defineProperty(URL, "revokeObjectURL", {
      configurable: true,
      value: vi.fn(),
    });
  }
  useCommentDraftStore.getState().clearDraft(DRAFT_KEY);
  mockApiUploadFile.mockReset();
  submissions.length = 0;
});

afterEach(() => {
  document.body.innerHTML = "";
});

// editorEpoch flips the ContentEditor key: a conditional remount of the
// shared editor destroys the Tiptap instance while the composer (this Host)
// stays alive — the exact shape RUYI-478/RUYI-493 describe.
function Host({ editorRef }: { editorRef: RefObject<ContentEditorRef | null> }) {
  const [editorEpoch, setEditorEpoch] = useState(0);
  const uploadGate = useUploadGate(editorRef);
  const { attachments: pendingAttachments, handleUpload, gate } = useCommentUploads(
    DRAFT_KEY,
    { issueId: ISSUE_ID },
    uploadGate,
    editorRef,
  );
  // Hydrate like a reopened composer: read the live draft on every render so
  // a remounted editor instance starts from the persisted body.
  const initialDraft = useCommentDraftStore.getState().getDraft(DRAFT_KEY);
  const setDraft = useCommentDraftStore((s) => s.setDraft);
  harness.remount = () => setEditorEpoch((n) => n + 1);

  const { submit } = useComposerSubmit({
    editorRef,
    uploadGate: gate,
    afterAccepted: "none",
    onSubmit: (content) => {
      // Mirrors comment-input.tsx: bind only uploads the body still references.
      const activeIds = pendingAttachments
        .filter((a) => contentReferencesAttachment(content, a))
        .map((a) => a.id);
      submissions.push({ content, attachmentIds: activeIds.length > 0 ? activeIds : undefined });
      return Promise.resolve(`comment-${submissions.length}`).then((commentId) => {
        useCommentDraftStore.getState().clearDraft(DRAFT_KEY);
        editorRef.current?.clearContent();
        return !!commentId;
      });
    },
  });
  harness.submit = submit;

  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <ContentEditor
        key={editorEpoch}
        ref={editorRef}
        defaultValue={initialDraft}
        onUpdate={(md) => {
          console.info("[dbg] onUpdate fired:", JSON.stringify(md?.slice?.(0, 60)));
          setDraft(DRAFT_KEY, md);
        }}
        onUploadFile={handleUpload}
        onUploadingChange={uploadGate.onUploadingChange}
        debounceMs={100}
        currentIssueId={ISSUE_ID}
        attachments={pendingAttachments}
      />
    </I18nProvider>
  );
}

describe("comment composer: upload settles after an editor remount (RUYI-483)", () => {
  it("delivers the finished link to the reopened editor and binds it on submit", async () => {
    // Upload stays in flight until the test resolves it — the settle must
    // arrive AFTER the editor instance below was destroyed.
    let resolveUpload!: (a: Attachment) => void;
    mockApiUploadFile.mockImplementation(
      () =>
        new Promise<Attachment>((resolve) => {
          resolveUpload = resolve;
        }),
    );
    const editorRef: React.RefObject<ContentEditorRef | null> = { current: null };
    const view = render(<Host editorRef={editorRef} />);

    await vi.waitFor(() => {
      expect(editorRef.current?.getMarkdown()).toBeDefined();
    });

    // Seed the draft body so the store target exists (same as any composer
    // that already holds text).
    await act(async () => {
      editorRef.current?.insertMarkdownAtEnd("in progress");
      editorRef.current?.flushPendingUpdate();
    });

    // Start the upload: the skeleton fileCard enters the document. An
    // uploading card serializes to nothing, so — exactly like a real user —
    // keep typing afterwards; the typed bytes are what carries the debounced
    // emit that persists the draft.
    const file = new File([new Uint8Array([1, 2, 3])], "notes.txt", { type: "text/plain" });
    await act(async () => {
      editorRef.current?.uploadFile(file);
    });
    expect(editorRef.current?.hasActiveUploads()).toBe(true);
    await act(async () => {
      editorRef.current?.insertMarkdownAtEnd("typing while uploading");
    });

    // Let the debounced emit persist the body into the draft store.
    await act(async () => {
      await new Promise((r) => setTimeout(r, 200));
    });
    expect(useCommentDraftStore.getState().getDraft(DRAFT_KEY)).toContain("typing while uploading");

    // The conditional remount: the old Tiptap instance dies mid-upload.
    // (An uploading card serializes to an &nbsp; placeholder, so the reopened
    // instance may not redraw the card from the persisted body alone — the
    // settled link delivery below must not depend on the card surviving.)
    await act(async () => {
      harness.remount();
    });
    await vi.waitFor(() => {
      expect(editorRef.current?.getMarkdown()).toContain("typing while uploading");
    });
    expect(editorRef.current?.getMarkdown()).not.toContain("/api/attachments/");

    // The upload settles against the dead instance.
    await act(async () => {
      resolveUpload(makeAttachment("notes.txt", "text/plain"));
    });

    // Let the post-settle delivery watcher play out (DELIVER_RETRY_MS ticks).
    await act(async () => {
      await new Promise((r) => setTimeout(r, 300));
    });

    // The finished link must have reached BOTH the live document and the
    // persisted draft — the reopened editor is what the user sees and submits.
    const mdAtSubmit = editorRef.current?.getMarkdown() ?? "";
    const storeBody = useCommentDraftStore.getState().getDraft(DRAFT_KEY) ?? "";
    expect(mdAtSubmit).toContain("[notes.txt](/api/attachments/");
    expect(storeBody).toContain("/api/attachments/");

    // Submission binds the attachment — no silent orphan.
    await act(async () => {
      editorRef.current?.insertMarkdownAtEnd("second comment");
      editorRef.current?.flushPendingUpdate();
    });
    await act(async () => {
      await harness.submit();
    });

    expect(submissions.length).toBe(1);
    const sent = submissions[0]!;
    expect(sent.content).toContain("[notes.txt](/api/attachments/");
    expect(sent.attachmentIds).toBeDefined();
    expect(sent.attachmentIds).toHaveLength(1);

    // Exactly one reference in doc and body (no double delivery).
    const docCopies = mdAtSubmit.split("/api/attachments/att-ruyi-483r-").length - 1;
    expect(docCopies).toBe(1);
    const bodyCopies = storeBody.split("/api/attachments/att-ruyi-483r-").length - 1;
    expect(bodyCopies).toBe(1);

    view.unmount();
  });
});
