// RUYI-478 — quick-create pasted-image round-trip through the REAL editor,
// REAL coordinated-upload engine, and the REAL issue draft store.
//
// content-editor.test.tsx mocks uploadAndInsertFile and file-upload.test.tsx
// builds bare editors; neither exercises the quick-create wiring where the
// QA defect lives: paste → placeholder → coordinator upload → settle →
// serialized draft body → submit-time reference filtering.
//
// The host below is wired exactly like packages/views/modals/quick-create-issue.tsx
// (useUploadGate + useIssueCreateUploads + ContentEditor with a store-writing
// onUpdate), minus the modal chrome.
import type { RefObject } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import { useIssueDraftStore } from "@multica/core/issues/stores";
import type { Attachment } from "@multica/core/types";
import enCommon from "../locales/en/common.json";
import enEditor from "../locales/en/editor.json";
import { ContentEditor, type ContentEditorRef } from "./content-editor";
import { useUploadGate } from "./use-upload-gate";
import { useIssueCreateUploads } from "../modals/use-issue-create-uploads";

const mockApiUploadFile = vi.hoisted(() => vi.fn());
vi.mock("@multica/core/api", () => ({ api: { uploadFile: mockApiUploadFile } }));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));
vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ fetchQuery: async () => null }),
  useQuery: () => ({ data: undefined, isLoading: false }),
}));
vi.mock("@multica/core/paths", () => ({
  useWorkspaceSlug: () => "ws",
}));
vi.mock("../navigation", () => ({
  useAppOrigin: () => "http://localhost:3000",
  resolveClickIntent: () => "same-tab",
}));
vi.mock("./bubble-menu", () => ({ EditorBubbleMenu: () => null }));
vi.mock("./link-hover-card", () => ({
  useLinkHover: () => ({}),
  LinkHoverCard: () => null,
}));

const TEST_RESOURCES = { en: { common: enCommon, editor: enEditor } };

const ATTACHMENT_ID = "att-ruyi-478";
const FINAL_URL = `/api/attachments/${ATTACHMENT_ID}/download`;

function makeAttachment(id: string): Attachment {
  return {
    id,
    workspace_id: "ws-1",
    uploader_type: "member",
    uploader_id: "user-1",
    filename: "image.png",
    content_type: "image/png",
    size_bytes: 1234,
    url: `/uploads/workspaces/ws-1/${id}.png`,
    download_url: `/api/attachments/${id}/download`,
    markdown_url: `/api/attachments/${id}/download`,
    created_at: new Date(0).toISOString(),
  } as unknown as Attachment;
}

// jsdom lacks the rect APIs ProseMirror's scrollIntoView touches (same stubs
// as file-upload.test.tsx).
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
  useIssueDraftStore.getState().clearDraft();
  mockApiUploadFile.mockReset();
});

afterEach(() => {
  document.body.innerHTML = "";
});

function Host({
  editorRef,
  remountKey,
}: {
  editorRef: RefObject<ContentEditorRef | null>;
  remountKey?: string;
}) {
  const uploadGate = useUploadGate(editorRef);
  const { attachments, handleUpload } = useIssueCreateUploads(
    "agent",
    uploadGate,
    editorRef,
  );
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <ContentEditor
        key={remountKey}
        ref={editorRef}
        onUploadFile={handleUpload}
        onUploadingChange={uploadGate.onUploadingChange}
        attachments={attachments}
        onUpdate={(md) => {
          useIssueDraftStore.getState().setAgent({ prompt: md });
        }}
        debounceMs={150}
      />
    </I18nProvider>
  );
}

/** Wire the mocked coordinator transport to succeed with a real-shaped attachment. */
function mockUploadSucceeds(afterMs = 20): void {
  mockApiUploadFile.mockImplementation(
    async (_file: File, _ctx: unknown, _signal: AbortSignal) => {
      await new Promise((r) => setTimeout(r, afterMs));
      return makeAttachment(ATTACHMENT_ID);
    },
  );
}

describe("quick-create pasted image round-trip (RUYI-478)", () => {
  it("keeps the settled image node in the document and the draft body until submit time", async () => {
    mockUploadSucceeds();
    const editorRef: React.RefObject<ContentEditorRef | null> = { current: null };
    const view = render(<Host editorRef={editorRef} />);

    // Wait for the deferred Tiptap instance.
    await vi.waitFor(() => {
      expect(editorRef.current?.getMarkdown()).toBeDefined();
    });

    // Paste: the imperative path the fileUpload extension drives for real
    // clipboard files ends in the same uploadAndInsertFile call.
    const file = new File([new Uint8Array([137, 80, 78, 71])], "image.png", {
      type: "image/png",
    });
    await act(async () => {
      editorRef.current?.uploadFile(file);
    });

    // Mid-upload the placeholder serializes to nothing, and the submit gate
    // must see it.
    expect(editorRef.current?.hasActiveUploads()).toBe(true);

    // Upload settles (mock resolves after 20ms) → inline blob→URL swap.
    await vi.waitFor(() => {
      expect(editorRef.current?.hasActiveUploads()).toBe(false);
    });

    // The image is content now and must SURVIVE to submit time.
    const md = editorRef.current?.getMarkdown() ?? "";
    expect(md).toContain(`![image.png](${FINAL_URL})`);

    // After the debounced onUpdate fires, the persisted draft body carries it.
    await vi.waitFor(() => {
      expect(useIssueDraftStore.getState().draft.agent.prompt).toContain(FINAL_URL);
    });

    // …and it is still there after the debounce window passes twice (the
    // QA signature: image gone from the document while the stale draft
    // snapshot still holds it).
    await act(async () => {
      await new Promise((r) => setTimeout(r, 400));
    });
    expect(editorRef.current?.getMarkdown()).toContain(FINAL_URL);
    expect(useIssueDraftStore.getState().draft.agent.prompt).toContain(FINAL_URL);

    view.unmount();
  });

  it("blocks submit while an upload is in flight (explicit timing contract)", async () => {
    mockUploadSucceeds(150);
    const editorRef: React.RefObject<ContentEditorRef | null> = { current: null };
    const view = render(<Host editorRef={editorRef} />);

    await vi.waitFor(() => {
      expect(editorRef.current?.getMarkdown()).toBeDefined();
    });

    const file = new File(["x"], "late.png", { type: "image/png" });
    await act(async () => {
      editorRef.current?.uploadFile(file);
    });
    expect(editorRef.current?.hasActiveUploads()).toBe(true);

    // Submitting now must not silently drop the image: the gate reports
    // blocked, so useComposerSubmit returns before reading the body.
    // (Assert the gate; the composer contract itself is covered in
    // use-composer-submit.test.tsx.)
    expect(editorRef.current?.hasActiveUploads()).toBe(true);

    await vi.waitFor(() => {
      expect(editorRef.current?.hasActiveUploads()).toBe(false);
    });
    expect(editorRef.current?.getMarkdown()).toContain(`![image.png](${FINAL_URL})`);

    view.unmount();
  });

  it("delivers a settled upload into a REMOUNTED editor while the host stays alive", async () => {
    // The QA failure signature: the upload completes but the document it
    // lands in was rebuilt in between (any conditionally-rendered editor
    // remount does this). The coordinator's write-back is gated on the
    // HOST being gone (`mountedRef`), so a live host + dead editor instance
    // left the finished attachment nowhere: inline swap returns on the
    // destroyed instance, write-back never fires, and the image silently
    // vanishes from the body while the attachment row says "uploaded".
    mockUploadSucceeds(120);
    const editorRef: React.RefObject<ContentEditorRef | null> = { current: null };
    const view = render(<Host editorRef={editorRef} remountKey="v1" />);

    await vi.waitFor(() => {
      expect(editorRef.current?.getMarkdown()).toBeDefined();
    });

    const file = new File([new Uint8Array([137, 80, 78, 71])], "image.png", {
      type: "image/png",
    });
    await act(async () => {
      editorRef.current?.uploadFile(file);
    });
    expect(editorRef.current?.hasActiveUploads()).toBe(true);

    // The editor instance is rebuilt mid-upload; the HOST (and therefore the
    // coordinated-upload hook and its mountedRef) is untouched.
    await act(async () => {
      view.rerender(<Host editorRef={editorRef} remountKey="v2" />);
    });
    await vi.waitFor(() => {
      expect(editorRef.current?.getMarkdown()).toBeDefined();
    });

    // Upload settles AFTER the remount.
    await act(async () => {
      await new Promise((r) => setTimeout(r, 250));
    });

    // The finished image MUST reach the live document (write-back path) and
    // the persisted draft body — a settled upload may not vanish just
    // because the document it started in was rebuilt. Exactly ONCE: the
    // write-back must neither double-append beside a rebuilt placeholder nor
    // race the debounced body emit.
    const md = editorRef.current?.getMarkdown() ?? "";
    expect(md).toContain(`![image.png](${FINAL_URL})`);
    expect(md.split(`![image.png](${FINAL_URL})`).length - 1).toBe(1);
    const stored = useIssueDraftStore.getState().draft.agent.prompt;
    expect(stored).toContain(FINAL_URL);
    expect(stored.split(FINAL_URL).length - 1).toBe(1);

    view.unmount();
  });

  it("delivers a settled image whose editor dies inside the debounce window (RUYI-493)", async () => {
    // The residual window from RUYI-478: the settle swap lands in a LIVE
    // editor's document, so no "editor lost" flag is set and the debounced
    // onUpdate becomes the only writer of the body. Destroying the editor
    // inside that window drops the pending update with the instance — the
    // image sits in a dead document while the draft body never learns of it.
    mockUploadSucceeds(20);
    const editorRef: React.RefObject<ContentEditorRef | null> = { current: null };
    const view = render(<Host editorRef={editorRef} remountKey="v1" />);

    await vi.waitFor(() => {
      expect(editorRef.current?.getMarkdown()).toBeDefined();
    });

    const file = new File([new Uint8Array([137, 80, 78, 71])], "image.png", {
      type: "image/png",
    });
    await act(async () => {
      editorRef.current?.uploadFile(file);
    });

    // Settle while the editor is alive: the inline swap lands here.
    await vi.waitFor(() => {
      expect(editorRef.current?.hasActiveUploads()).toBe(false);
    });

    // Destroy the instance inside the 150ms debounce window (remount, same
    // host — the coordinated-upload hook survives, its recheck must too).
    await act(async () => {
      view.rerender(<Host editorRef={editorRef} remountKey="v2" />);
    });
    await vi.waitFor(() => {
      expect(editorRef.current?.getMarkdown()).toBeDefined();
    });

    // The image must still reach the persisted draft body — the submit-time
    // reference filter binds against it — and the replacement editor's
    // document, exactly once each.
    await vi.waitFor(
      () => {
        expect(useIssueDraftStore.getState().draft.agent.prompt).toContain(FINAL_URL);
      },
      { timeout: 3000 },
    );
    const stored = useIssueDraftStore.getState().draft.agent.prompt;
    expect(stored.split(FINAL_URL).length - 1).toBe(1);
    const md = editorRef.current?.getMarkdown() ?? "";
    expect(md).toContain(`![image.png](${FINAL_URL})`);
    expect(md.split(`![image.png](${FINAL_URL})`).length - 1).toBe(1);

    view.unmount();
  });

  it("does not backfill an upload whose placeholder the user deleted on the live editor", async () => {
    // MUL-5181 guard for the delivery confirmation: a placeholder the user
    // deleted mid-upload must stay deleted even once the upload settles.
    // clearContent is coarser than a Backspace but leaves the engine in the
    // same observable state — a live document with no node carrying the
    // upload's uploadId.
    mockUploadSucceeds(20);
    const editorRef: React.RefObject<ContentEditorRef | null> = { current: null };
    const view = render(<Host editorRef={editorRef} />);

    await vi.waitFor(() => {
      expect(editorRef.current?.getMarkdown()).toBeDefined();
    });

    const file = new File([new Uint8Array([137, 80, 78, 71])], "image.png", {
      type: "image/png",
    });
    await act(async () => {
      editorRef.current?.uploadFile(file);
    });
    expect(editorRef.current?.hasActiveUploads()).toBe(true);

    // User deletes the placeholder mid-upload, before the settle.
    await act(async () => {
      editorRef.current?.clearContent();
    });

    // Settle arrives on the still-alive editor; nothing may write the image
    // back, now or after the debounce window has fully passed.
    await act(async () => {
      await new Promise((r) => setTimeout(r, 250));
    });
    expect(editorRef.current?.getMarkdown() ?? "").not.toContain(FINAL_URL);
    expect(useIssueDraftStore.getState().draft.agent.prompt).not.toContain(FINAL_URL);

    view.unmount();
  });
});
