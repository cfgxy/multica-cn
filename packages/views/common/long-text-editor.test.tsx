/**
 * @vitest-environment jsdom
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { renderWithI18n } from "../test/i18n";
import { LongTextEditor } from "./long-text-editor";

const LABEL = "Workspace context";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

/** Controlled harness — the real callers own the draft in their own state,
 *  so every edit must flow through onChange and back in via props. */
function Harness({
  initial = "",
  onBlur,
  onSave,
  disabled,
  expandable,
  preview,
  minHeight,
  maxHeight,
  hint,
}: {
  initial?: string;
  onBlur?: () => void;
  onSave?: () => void | Promise<void>;
  disabled?: boolean;
  expandable?: boolean;
  preview?: boolean;
  minHeight?: number;
  maxHeight?: number;
  hint?: React.ReactNode;
}) {
  const [value, setValue] = useState(initial);
  return (
    <LongTextEditor
      value={value}
      onChange={setValue}
      onBlur={onBlur}
      onSave={onSave}
      ariaLabel={LABEL}
      dialogTitle={LABEL}
      placeholder="Write here"
      disabled={disabled}
      expandable={expandable}
      preview={preview}
      minHeight={minHeight}
      maxHeight={maxHeight}
      hint={hint}
    />
  );
}

function getInlineTextarea() {
  return screen.getByLabelText(LABEL) as HTMLTextAreaElement;
}

/** The textarea inside the open expanded dialog (labelled with the same
 *  aria-label as the inline one, so scope the query to the dialog). */
function getDialogTextarea() {
  return within(screen.getByRole("dialog")).getByLabelText(
    LABEL,
  ) as HTMLTextAreaElement;
}

describe("LongTextEditor", () => {
  it("caps the inline height so long content scrolls inside instead of stretching the page", () => {
    renderWithI18n(<Harness initial={"line\n".repeat(200)} />);

    const textarea = getInlineTextarea();
    expect(textarea.style.maxHeight).toBe("360px");
    expect(textarea.className).toContain("overflow-y-auto");
  });

  it("honours custom inline height bounds", () => {
    renderWithI18n(<Harness minHeight={80} maxHeight={200} />);

    const textarea = getInlineTextarea();
    expect(textarea.style.maxHeight).toBe("200px");
    expect(textarea.style.minHeight).toBe("80px");
  });

  it("flows every keystroke to the caller's state (no silent buffering)", async () => {
    const user = userEvent.setup();
    renderWithI18n(<Harness />);

    await user.type(getInlineTextarea(), "hello");
    expect(screen.getByDisplayValue("hello")).toBeDefined();
  });

  it("expands into a large dialog that edits the same draft", async () => {
    const user = userEvent.setup();
    renderWithI18n(<Harness initial="draft text" />);

    await user.click(screen.getByRole("button", { name: "Expand editor" }));

    const dialog = screen.getByRole("dialog");
    expect(dialog).toBeDefined();
    expect(screen.getByRole("heading", { name: LABEL })).toBeDefined();

    const expanded = getDialogTextarea();
    expect(expanded.value).toBe("draft text");
    await user.clear(expanded);
    await user.type(expanded, "rewritten in dialog");
    expect(expanded.value).toBe("rewritten in dialog");
  });

  it("preview toggle renders markdown structure and switching back keeps the draft", async () => {
    const user = userEvent.setup();
    renderWithI18n(
      <Harness
        initial={[
          "# Heading",
          "",
          "- list item",
          "",
          "> quoted",
          "",
          "```js",
          "const x = 1;",
          "```",
          "",
          "[link](https://example.com) and **bold** and *italic*",
        ].join("\n")}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Expand editor" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Preview" }));

    // Preview must cover at least: heading, list, code block, quote, link,
    // bold/italic (RUYI-735 acceptance).
    expect(screen.getByRole("heading", { level: 1, name: "Heading" })).toBeDefined();
    expect(screen.getByRole("list")).toBeDefined();
    expect(screen.getByText("quoted").closest("blockquote")).toBeDefined();
    // Code blocks go through the shared shiki highlighter (async, tokenized
    // into spans), so assert on the <pre> text content rather than a single
    // text node.
    await waitFor(() => {
      const pre = screen.getByRole("dialog").querySelector("pre");
      expect(pre?.textContent).toContain("const x = 1;");
    });
    const link = screen.getByRole("link", { name: "link" });
    expect(link.getAttribute("href")).toBe("https://example.com");
    expect(screen.getByText("bold").closest("strong")).toBeDefined();
    expect(screen.getByText("italic").closest("em")).toBeDefined();

    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Edit" }));
    const expanded = getDialogTextarea();
    expect(expanded.value).toContain("# Heading");
  });

  it("Done keeps the draft, fires the flush hook, and returns to the page", async () => {
    const user = userEvent.setup();
    const onBlur = vi.fn();
    renderWithI18n(<Harness initial="before" onBlur={onBlur} />);

    await user.click(screen.getByRole("button", { name: "Expand editor" }));
    const expanded = getDialogTextarea();
    await user.clear(expanded);
    await user.type(expanded, "after");

    await user.click(screen.getByRole("button", { name: "Done" }));

    expect(onBlur).toHaveBeenCalled();
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    // The inline textarea shows the committed draft — nothing was lost.
    expect(getInlineTextarea().value).toBe("after");
  });

  it("Cancel discards back to the pre-expand snapshot", async () => {
    const user = userEvent.setup();
    renderWithI18n(<Harness initial="original" />);

    await user.click(screen.getByRole("button", { name: "Expand editor" }));
    const expanded = getDialogTextarea();
    await user.clear(expanded);
    await user.type(expanded, "temporary edit");

    await user.click(screen.getByRole("button", { name: "Cancel" }));

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    expect(getInlineTextarea().value).toBe("original");
  });

  it("overlay dismissal (Escape) never silently discards the draft", async () => {
    const user = userEvent.setup();
    renderWithI18n(<Harness initial="original" />);

    await user.click(screen.getByRole("button", { name: "Expand editor" }));
    const expanded = getDialogTextarea();
    await user.clear(expanded);
    await user.type(expanded, "kept edit");

    await user.keyboard("{Escape}");

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    // Escape is not Cancel: the edit survives.
    expect(getInlineTextarea().value).toBe("kept edit");
  });

  it("footer Save is always reachable and closes only after the save resolves", async () => {
    const user = userEvent.setup();
    let resolveSave!: () => void;
    const onSave = vi.fn(
      () => new Promise<void>((resolve) => { resolveSave = resolve; }),
    );
    renderWithI18n(<Harness initial="persist me" onSave={onSave} />);

    await user.click(screen.getByRole("button", { name: "Expand editor" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(onSave).toHaveBeenCalledTimes(1);
    // Still open while the save is in flight — no silent close-on-failure.
    expect(screen.queryByRole("dialog")).not.toBeNull();
    resolveSave();
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
  });

  it("keeps the dialog open when the save rejects", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn(() => Promise.reject(new Error("network down")));
    renderWithI18n(<Harness onSave={onSave} />);

    await user.click(screen.getByRole("button", { name: "Expand editor" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledTimes(1);
    });
    expect(screen.queryByRole("dialog")).not.toBeNull();
  });

  it("hides the expand affordance when disabled or expandable=false", () => {
    const { unmount } = renderWithI18n(<Harness disabled />);
    expect(screen.queryByRole("button", { name: "Expand editor" })).toBeNull();
    expect(getInlineTextarea().disabled).toBe(true);
    unmount();

    renderWithI18n(<Harness expandable={false} />);
    expect(screen.queryByRole("button", { name: "Expand editor" })).toBeNull();
  });

  it("renders the hint under the inline editor and inside the dialog", async () => {
    const user = userEvent.setup();
    renderWithI18n(<Harness hint={<span>32000 / 32000</span>} />);

    expect(screen.getAllByText("32000 / 32000").length).toBe(1);

    await user.click(screen.getByRole("button", { name: "Expand editor" }));
    expect(screen.getAllByText("32000 / 32000").length).toBeGreaterThanOrEqual(1);
  });
});
