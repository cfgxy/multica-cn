// RUYI-483 — comment-composer two-submit sequence through the REAL editor,
// REAL coordinated-upload engine, and the REAL comment draft store.
//
// QA 实机轮缺陷序列（RUYI-474 终轮报告探针4）：同一编辑器挂载内先发一条纯文本
// 评论（编辑器不重挂载），随后上传文件再发第二条——第二条的附件 comment_id 为空
// （孤儿附件），评论 content 只剩纯文本。首提路径（探针3）正常。
//
// The host below is wired exactly like packages/views/issues/components/comment-input.tsx
// (useUploadGate + useCommentUploads + useComposerSubmit with the reference-
// filtered attachment_ids payload and the clear-on-accept handler), minus the
// voice/quick-reply chrome.
import type { RefObject } from "react";
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

const ISSUE_ID = "issue-ruyi-483";
const DRAFT_KEY = `new:${ISSUE_ID}` as const;

let attSeq = 0;
function makeAttachment(filename: string, contentType: string): Attachment {
  attSeq += 1;
  const id = `att-ruyi-483-${attSeq}`;
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

// The host hands its `submit` (from useComposerSubmit) to the test through this
// handle — the same function the send button and Cmd/Ctrl+Enter invoke.
const harness: { submit: () => Promise<void> } = { submit: async () => {} };

// jsdom lacks the rect APIs ProseMirror's scrollIntoView touches (same stubs
// as quick-create-upload-roundtrip.test.tsx).
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
      value: vi.fn(() => "blob:test-paste"),
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

function Host({ editorRef }: { editorRef: RefObject<ContentEditorRef | null> }) {
  const uploadGate = useUploadGate(editorRef);
  const { attachments: pendingAttachments, handleUpload, gate } = useCommentUploads(
    DRAFT_KEY,
    { issueId: ISSUE_ID },
    uploadGate,
    editorRef,
  );
  const setDraft = useCommentDraftStore((s) => s.setDraft);

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
        // Mirrors comment-input.tsx onAccepted (no mid-flight edits in this
        // suite): success consumes the draft it submitted and scrubs the
        // editor; the mount stays alive.
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
        ref={editorRef}
        onUpdate={(md) => setDraft(DRAFT_KEY, md)}
        onUploadFile={handleUpload}
        onUploadingChange={uploadGate.onUploadingChange}
        debounceMs={100}
        currentIssueId={ISSUE_ID}
        attachments={pendingAttachments}
      />
    </I18nProvider>
  );
}

describe("comment composer: two submissions in one mount (RUYI-483)", () => {
  it("binds the second submission's upload to its own comment", async () => {
    mockApiUploadFile.mockImplementation(
      async (_file: File, _ctx: unknown, _signal: AbortSignal) => {
        await new Promise((r) => setTimeout(r, 20));
        return makeAttachment("notes.txt", "text/plain");
      },
    );
    const editorRef: React.RefObject<ContentEditorRef | null> = { current: null };
    const view = render(<Host editorRef={editorRef} />);

    await vi.waitFor(() => {
      expect(editorRef.current?.getMarkdown()).toBeDefined();
    });

    // --- Submission #1: plain text (the QA probe's first comment).
    await act(async () => {
      editorRef.current?.insertMarkdownAtEnd("first comment");
      editorRef.current?.flushPendingUpdate();
    });
    await act(async () => {
      await harness.submit();
    });
    expect(submissions[0]).toEqual({ content: "first comment", attachmentIds: undefined });
    // The accepted submit scrubbed the editor; the mount is untouched.
    expect(editorRef.current?.getMarkdown()).toBe("");

    // --- Upload a non-image file in the SAME mount (QA probe 4).
    const file = new File([new Uint8Array([1, 2, 3])], "notes.txt", { type: "text/plain" });
    await act(async () => {
      editorRef.current?.uploadFile(file);
    });
    await vi.waitFor(() => {
      expect(editorRef.current?.hasActiveUploads()).toBe(false);
    });

    // Let the debounced emit + the settle-delivery watcher fully play out.
    await act(async () => {
      await new Promise((r) => setTimeout(r, 60));
    });
    console.info(
      "[t+60] doc=",
      JSON.stringify(editorRef.current?.getMarkdown()),
      "body=",
      JSON.stringify(useCommentDraftStore.getState().getDraft(DRAFT_KEY)),
    );
    await act(async () => {
      await new Promise((r) => setTimeout(r, 120));
    });
    console.info(
      "[t+180] doc=",
      JSON.stringify(editorRef.current?.getMarkdown()),
      "body=",
      JSON.stringify(useCommentDraftStore.getState().getDraft(DRAFT_KEY)),
    );
    await act(async () => {
      await new Promise((r) => setTimeout(r, 120));
    });
    console.info(
      "[t+300] doc=",
      JSON.stringify(editorRef.current?.getMarkdown()),
      "body=",
      JSON.stringify(useCommentDraftStore.getState().getDraft(DRAFT_KEY)),
    );
    const mdAtSubmit = editorRef.current?.getMarkdown() ?? "";
    const storeBody = useCommentDraftStore.getState().getDraft(DRAFT_KEY) ?? "";

    // --- Submission #2: the comment the user sends with the file attached.
    await act(async () => {
      editorRef.current?.insertMarkdownAtEnd("second comment");
      editorRef.current?.flushPendingUpdate();
    });
    await act(async () => {
      await harness.submit();
    });

    expect(submissions.length).toBe(2);
    const second = submissions[1]!;
    // The second comment's content must carry the file reference and its
    // attachment_ids must bind the uploaded attachment — the server writes
    // attachment.comment_id from exactly these two fields.
    expect(mdAtSubmit).toContain("/api/attachments/");
    expect(storeBody).toContain("/api/attachments/");
    expect(second.content).toContain("!file[notes.txt](/api/attachments/");
    expect(second.attachmentIds).toBeDefined();
    expect(second.attachmentIds).toHaveLength(1);

    // Exactly ONE file reference in the document and the body (the settle
    // delivery must not double-append beside the inline-swapped card).
    const docCopies = mdAtSubmit.split("!file[notes.txt]").length - 1;
    expect(docCopies).toBe(1);
    const bodyCopies = storeBody.split("/api/attachments/att-ruyi-483-").length - 1;
    expect(bodyCopies).toBe(1);

    view.unmount();
  });
});
