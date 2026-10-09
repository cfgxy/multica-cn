// RUYI-483 — the FULL CommentInput (real editor, real coordinated-upload
// engine, real draft store) through the QA probe sequence: post a plain-text
// comment, then — in the same mount — upload a file and post a second comment.
// The second comment's attachment must bind to its own comment (the server
// writes attachment.comment_id from the POSTed attachment_ids + the file
// reference in the content).
//
// Unlike comment-composers.test.tsx (mock editor) this exercises the real
// Tiptap serialization: a fileCard placeholder emits NOTHING until its settle
// swap lands, which is exactly the segment the QA orphan lives in.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, waitFor } from "@testing-library/react";
import type { Attachment } from "@multica/core/types";
import { useCommentDraftStore } from "@multica/core/issues/stores";
import { setCurrentWorkspace } from "@multica/core/platform";
import { renderWithI18n } from "../../test/i18n";
import { CommentInput } from "./comment-input";

const apiUploadFile = vi.hoisted(() => vi.fn());
const apiListWorkspaces = vi.hoisted(() => vi.fn());
const apiListQuickActions = vi.hoisted(() => vi.fn());
const apiListQuickReplies = vi.hoisted(() =>
  vi.fn().mockResolvedValue({ quick_replies: [], total: 0 }),
);
const apiListSkills = vi.hoisted(() => vi.fn().mockResolvedValue([]));

// 浏览器 provider 下原生 ESM 链接要求导出面完整：合并真实导出，仅覆盖测试关注面。
vi.mock("@multica/core/api", async (importActual) => {
  const actual = await importActual<typeof import("@multica/core/api")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      uploadFile: apiUploadFile,
      listWorkspaces: apiListWorkspaces,
      listQuickActions: apiListQuickActions,
      listQuickReplies: apiListQuickReplies,
      listSkills: apiListSkills,
    },
    getApi: () => ({ getBaseUrl: () => "http://localhost:3000" }) as never,
  };
});

vi.mock("sonner", async (importActual) => ({
  ...(await importActual<typeof import("sonner")>()),
  toast: { error: vi.fn(), success: vi.fn() },
}));

// RUYI-474 voice harness stub — the real chain needs WS + audio capture.
vi.mock("../../voice", async (importActual) => ({
  ...(await importActual<typeof import("../../voice")>()),
  useVoiceSession: () => ({
    phase: "idle",
    failure: null,
    userTurns: [],
    liveUserText: "",
    liveAssistantText: "",
    start: vi.fn(),
    end: vi.fn(),
    dismiss: vi.fn(),
  }),
  VoiceButton: () => null,
  VoiceOverlay: () => null,
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

const DRAFT_KEY = "new:issue-1" as const;

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
  localStorage.clear();
  useCommentDraftStore.setState({ drafts: {} });
  apiUploadFile.mockReset();
  apiListWorkspaces.mockReset();
  apiListQuickActions.mockReset();
  submissions.length = 0;
  attSeq = 0;
});

afterEach(() => {
  setCurrentWorkspace(null, null);
  document.body.innerHTML = "";
});

function renderComposer() {
  const onSubmit = vi.fn(
    (content: string, attachmentIds?: string[]) => {
      submissions.push({ content, attachmentIds: attachmentIds?.length ? attachmentIds : undefined });
      return Promise.resolve(`comment-${submissions.length}`);
    },
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = renderWithI18n(
    <QueryClientProvider client={queryClient}>
      <CommentInput issueId="issue-1" onSubmit={onSubmit} />
    </QueryClientProvider>,
  );
  return { view, onSubmit };
}

function lastButton(container: HTMLElement): HTMLButtonElement {
  const buttons = container.querySelectorAll("button");
  const button = buttons[buttons.length - 1];
  if (!button) throw new Error("Expected submit button to render");
  return button;
}

describe("CommentInput: two submissions in one mount with a file upload (RUYI-483)", () => {
  it("binds the second comment's upload to the second comment", async () => {
    // The QA probe's second-round file is a non-image (fileCard).
    apiUploadFile.mockImplementation(
      async (_file: File, _ctx: unknown, _signal: AbortSignal) => {
        await new Promise((r) => setTimeout(r, 30));
        return makeAttachment("notes.txt", "text/plain");
      },
    );

    // Pre-seed the draft so the real editor mounts with content — the same
    // document state "user typed a first comment" produces, without needing
    // keystroke simulation in jsdom.
    act(() => {
      useCommentDraftStore.getState().setDraft(DRAFT_KEY, "first comment");
    });

    const { view, onSubmit } = renderComposer();

    // The unsent draft mounts the real editor immediately (readonly-first
    // contract: a draft is standing intent).
    const editorArea = await waitFor(() => {
      const el = view.container.querySelector(".rich-text-editor");
      expect(el).not.toBeNull();
      return el as HTMLElement;
    });
    expect(editorArea.textContent).toContain("first comment");

    // --- Submission #1: plain text, no attachments.
    await act(async () => {
      fireEvent.click(lastButton(view.container));
    });
    await waitFor(() => {
      expect(submissions.length).toBe(1);
    });
    expect(submissions[0]).toMatchObject({ content: "first comment" });
    expect(submissions[0]?.attachmentIds).toBeUndefined();

    // The accepted submit scrubbed the editor; the composer stays mounted.
    await waitFor(() => {
      expect(useCommentDraftStore.getState().getDraft(DRAFT_KEY)).toBeUndefined();
    });

    // --- Upload a non-image file in the SAME mount (QA probe 4).
    const file = new File([new Uint8Array([1, 2, 3])], "notes.txt", { type: "text/plain" });
    const fileInputs = view.container.querySelectorAll('input[type="file"]');
    expect(fileInputs.length).toBeGreaterThan(0);
    await act(async () => {
      fireEvent.change(fileInputs[fileInputs.length - 1]!, { target: { files: [file] } });
    });

    // Let the upload settle, the inline swap land, the debounced emit fire,
    // and the settle-delivery watcher finish its work.
    await act(async () => {
      await new Promise((r) => setTimeout(r, 400));
    });

    // --- Submission #2.
    await act(async () => {
      fireEvent.click(lastButton(view.container));
    });
    await waitFor(() => {
      expect(submissions.length).toBe(2);
    });

    const second = submissions[1]!;
    // THE BUG CRITERION: the second comment must carry the file reference in
    // its content AND bind the attachment id — the server writes
    // attachment.comment_id from exactly these two fields.
    expect(second.content).toContain("!file[notes.txt](");
    expect(second.content).toContain("/api/attachments/att-ruyi-483-1/download");
    expect(second.attachmentIds).toEqual(["att-ruyi-483-1"]);

    void onSubmit;
    view.unmount();
  });
});
