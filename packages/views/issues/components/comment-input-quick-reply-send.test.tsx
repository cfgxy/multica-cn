// RUYI-550 — quick-reply → send-button availability through the REAL editor.
//
// comment-composers.test.tsx drives the composers against a mock ContentEditor
// whose insertMarkdownAtEnd calls onUpdate synchronously. The real editor
// emits the host's onUpdate through a debounce window (debounceMs=100), and
// the send button's `disabled` derives from that emission — the exact layer
// where the "picked a quick reply, button stayed grey" defect lives. These
// tests therefore run the REAL ContentEditor + REAL @tiptap/markdown with
// real timers, driving picks through the real dropdown UI.
//
// Scenarios (dispatch-card acceptance): rapid multi-pick/switch, pick on an
// empty draft, pick with text already present, and pick after a send cleared
// the composer. Every scenario must end with the send button ENABLED without
// any further typing / focus juggling, and must STAY enabled.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { setCurrentWorkspace } from "@multica/core/platform";
import { useCommentDraftStore } from "@multica/core/issues/stores";
import { renderWithI18n } from "../../test/i18n";
import { CommentInput } from "./comment-input";

const apiListQuickReplies = vi.hoisted(() => vi.fn());
const apiListQuickActions = vi.hoisted(() => vi.fn());
const apiRenderQuickAction = vi.hoisted(() => vi.fn());
const apiListSkills = vi.hoisted(() => vi.fn());
const apiListWorkspaces = vi.hoisted(() => vi.fn());
const apiUploadFile = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", () => ({
  api: {
    listQuickReplies: apiListQuickReplies,
    listQuickActions: apiListQuickActions,
    renderQuickAction: apiRenderQuickAction,
    listSkills: apiListSkills,
    listWorkspaces: apiListWorkspaces,
    uploadFile: apiUploadFile,
  },
  getApi: () => ({ getBaseUrl: () => "http://localhost:3000" }),
  setApiInstance: vi.fn(),
}));

// Keep the real VoiceButton/VoiceOverlay (slot-swap rendering is part of the
// defect surface); stub only the session hook — WS + audio are out of scope.
const voiceHarness = vi.hoisted(() => ({
  onUserTurn: null as ((text: string) => void) | null,
}));
vi.mock("../../voice", async () => {
  const actual = await vi.importActual<typeof import("../../voice")>("../../voice");
  return {
    ...actual,
    useVoiceSession: (options: {
      agentId: string | null;
      onUserTurn?: (text: string) => void;
    }) => {
      voiceHarness.onUserTurn = options.onUserTurn ?? null;
      return {
        phase: "idle" as const,
        failure: null,
        userTurns: [],
        liveUserText: "",
        liveAssistantText: "",
        start: vi.fn(),
        end: vi.fn(),
        dismiss: vi.fn(),
      };
    },
  };
});

// Bubble/hover chrome needs workspace-route context the composer tests don't
// provide; it is irrelevant to the send-slot behavior under test (same stubs
// as quick-create-upload-roundtrip.test.tsx).
vi.mock("../../editor/bubble-menu", () => ({ EditorBubbleMenu: () => null }));
vi.mock("../../editor/link-hover-card", () => ({
  useLinkHover: () => ({}),
  LinkHoverCard: () => null,
}));
vi.mock("../../navigation", () => ({
  useAppOrigin: () => "http://localhost:3000",
  resolveClickIntent: () => "same-tab",
}));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

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
});

const QUICK_REPLIES = {
  total: 3,
  quick_replies: [
    { id: "qr-1", workspace_id: "ws-1", name: "Ack", content: "On it — will update shortly.", position: 0, created_at: "", updated_at: "" },
    { id: "qr-2", workspace_id: "ws-1", name: "Tests", content: "I'll add the tests.", position: 1, created_at: "", updated_at: "" },
    { id: "qr-3", workspace_id: "ws-1", name: "Conflicts", content: "I'll take the PR conflicts.", position: 2, created_at: "", updated_at: "" },
  ],
};

function renderCommentInput(onSubmit: (content: string) => Promise<string | boolean>) {
  apiListQuickReplies.mockResolvedValue(QUICK_REPLIES);
  apiListSkills.mockResolvedValue([]);
  apiListQuickActions.mockResolvedValue({ quick_actions: [], total: 0 });
  apiListWorkspaces.mockResolvedValue([]);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = renderWithI18n(
    <QueryClientProvider client={queryClient}>
      <CommentInput issueId="issue-1" onSubmit={onSubmit} />
    </QueryClientProvider>,
  );
  return view;
}

function getSendButton(): HTMLButtonElement {
  // The send affordance is the last control in the composer's action cluster.
  const buttons = screen.getAllByRole("button");
  const send = buttons.find((b) => b.getAttribute("aria-label") === "Send");
  if (!send) throw new Error("Send button not rendered");
  return send as HTMLButtonElement;
}

async function pickQuickReply(name: RegExp) {
  fireEvent.click(screen.getByRole("button", { name: "Quick Replies" }));
  const item = await screen.findByRole("menuitem", { name });
  fireEvent.click(item);
}

/** The send button must be usable within a bounded wait — the debounced
 *  emission is ~100ms, so anything past this ceiling reads as the defect. */
async function expectSendEnabled() {
  await waitFor(() => expect(getSendButton().disabled).toBe(false), { timeout: 2000 });
}

async function expectSendDisabled() {
  await waitFor(() => expect(getSendButton().disabled).toBe(true), { timeout: 2000 });
}

beforeEach(() => {
  useCommentDraftStore.setState({ drafts: {} });
  setCurrentWorkspace("acme", "ws-1");
});

afterEach(() => {
  setCurrentWorkspace(null, null);
  vi.clearAllMocks();
});

describe("quick reply → send button availability (real editor, RUYI-550)", () => {
  it("first pick lands on the readonly shell: editor mounts, content lands, send enables", async () => {
    const onSubmit = vi.fn().mockResolvedValue(true);
    renderCommentInput(onSubmit);

    // Pick without ever activating the editor — the shell is the composer's
    // resting state, so this is the mainline first-interaction path.
    await pickQuickReply(/Tests/);

    await expectSendEnabled();
    // Stable: the enablement must not flap back to disabled.
    await new Promise((r) => setTimeout(r, 250));
    expect(getSendButton().disabled).toBe(false);
    expect(useCommentDraftStore.getState().getDraft("new:issue-1")).toContain(
      "I'll add the tests.",
    );
  });

  it("rapid multi-pick / switch keeps the send button enabled (no flapping)", async () => {
    const onSubmit = vi.fn().mockResolvedValue(true);
    renderCommentInput(onSubmit);

    // Three picks back-to-back, no settling waits in between — every pick
    // re-arms the editor's debounced emission mid-window.
    await pickQuickReply(/Ack/);
    await pickQuickReply(/Conflicts/);
    await pickQuickReply(/Tests/);

    await expectSendEnabled();
    await new Promise((r) => setTimeout(r, 250));
    expect(getSendButton().disabled).toBe(false);
  });

  it("pick with text already in the draft keeps send enabled", async () => {
    const onSubmit = vi.fn().mockResolvedValue(true);
    renderCommentInput(onSubmit);

    await pickQuickReply(/Ack/);
    await expectSendEnabled();

    // The draft now holds text — picking again is the "existing text" case.
    await pickQuickReply(/Conflicts/);
    await expectSendEnabled();
    await new Promise((r) => setTimeout(r, 250));
    expect(getSendButton().disabled).toBe(false);
  });

  it("pick after a send cleared the composer re-enables send", async () => {
    const onSubmit = vi.fn().mockResolvedValue(true);
    renderCommentInput(onSubmit);

    await pickQuickReply(/Ack/);
    await expectSendEnabled();

    fireEvent.click(getSendButton());
    await waitFor(() => expect(onSubmit).toHaveBeenCalledOnce());
    await expectSendDisabled();

    // Composer is empty again (ready editor this time, not the shell) —
    // picking must re-enable send the same way.
    await pickQuickReply(/Tests/);
    await expectSendEnabled();
    await new Promise((r) => setTimeout(r, 250));
    expect(getSendButton().disabled).toBe(false);
  });

  it("picked content reaches submit once the user sends", async () => {
    const onSubmit = vi.fn().mockResolvedValue(true);
    renderCommentInput(onSubmit);

    await pickQuickReply(/Ack/);
    await pickQuickReply(/Tests/);
    await expectSendEnabled();

    fireEvent.click(getSendButton());
    await waitFor(() => expect(onSubmit).toHaveBeenCalledOnce());
    const content = onSubmit.mock.calls[0]?.[0] as string;
    expect(content).toContain("On it — will update shortly.");
    expect(content).toContain("I'll add the tests.");
  });
});

// RUYI-550 input-area parity: the mobile composer leads its toolbar with an
// @ mention entry and keeps a dedicated image button beside the generic file
// one — the desktop composer gains the same entries here. The @ entry must
// behave identically on the readonly shell and on the mounted editor, because
// both are mainline first-interaction paths.
describe("input-area entries — mobile parity (RUYI-550)", () => {
  it("renders the @ mention, image and file entries", () => {
    renderCommentInput(vi.fn().mockResolvedValue(true));

    expect(screen.getByRole("button", { name: "Mention someone or an issue" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Upload image" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Attach file" })).toBeTruthy();
    // The image entry narrows the OS picker to images; the generic entry stays
    // unrestricted — the same split as the mobile composer's two buttons.
    const accepts = Array.from(document.querySelectorAll('input[type="file"]')).map((i) =>
      i.getAttribute("accept"),
    );
    expect(accepts).toContain("image/*");
    expect(accepts).toContain(null);
  });

  it("@ entry inserts a real mention trigger on the shell and after mount", async () => {
    renderCommentInput(vi.fn().mockResolvedValue(true));
    const editorText = () => document.querySelector(".ProseMirror")?.textContent ?? "";

    // Shell path: the trigger queues until the lazy editor mounts.
    fireEvent.click(screen.getByRole("button", { name: "Mention someone or an issue" }));
    await waitFor(() => expect(editorText()).toBe("@"), { timeout: 2000 });

    // Mounted path: a second click inserts immediately at the caret.
    fireEvent.click(screen.getByRole("button", { name: "Mention someone or an issue" }));
    await waitFor(() => expect(editorText()).toBe("@@"));
  });
});
