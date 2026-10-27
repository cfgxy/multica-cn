import { describe, expect, it } from "vitest";
import type { Attachment } from "@multica/core/types";
import {
  appendBodyMarkdown,
  attachmentMarkdown,
  pickMarkdownLink,
  referencedAttachmentIds,
} from "./attachment-markdown";

function att(over: Partial<Attachment> = {}): Attachment {
  return {
    id: "att-1",
    workspace_id: "ws-1",
    issue_id: null,
    comment_id: null,
    chat_session_id: null,
    chat_message_id: null,
    uploader_type: "member",
    uploader_id: "user-1",
    filename: "chart.png",
    url: "https://cdn.example/chart.png",
    download_url: "https://signed.example/chart.png?sig=x",
    markdown_url: "https://public.example/api/attachments/att-1/download",
    content_type: "image/png",
    size_bytes: 123,
    created_at: "2026-09-02T00:00:00Z",
    ...over,
  };
}

describe("pickMarkdownLink", () => {
  it("prefers the server-provided durable markdown_url", () => {
    const a = att();
    expect(pickMarkdownLink(a)).toBe(a.markdown_url);
  });

  it("falls back to the stable download path when markdown_url is empty", () => {
    const a = att({ markdown_url: "" });
    expect(pickMarkdownLink(a)).toBe(`/api/attachments/${a.id}/download`);
  });

  it("falls back to the raw storage url when there is no attachment id", () => {
    const a = att({ markdown_url: "", id: "" });
    expect(pickMarkdownLink(a)).toBe(a.url);
  });
});

describe("attachmentMarkdown", () => {
  it("renders images as ![filename](link)", () => {
    const a = att({ content_type: "image/png" });
    expect(attachmentMarkdown(a)).toBe(`![chart.png](${a.markdown_url})`);
  });

  it("renders non-images as [filename](link)", () => {
    const a = att({
      content_type: "application/pdf",
      filename: "spec.pdf",
    });
    expect(attachmentMarkdown(a)).toBe(`[spec.pdf](${a.markdown_url})`);
  });
});

describe("appendBodyMarkdown", () => {
  it("returns the fragment alone on an empty body", () => {
    expect(appendBodyMarkdown("", "![a](u)")).toBe("![a](u)");
    expect(appendBodyMarkdown("   ", "![a](u)")).toBe("![a](u)");
  });

  it("joins with a single newline when the body lacks a trailing newline", () => {
    expect(appendBodyMarkdown("text", "![a](u)")).toBe("text\n![a](u)");
  });

  it("keeps an existing trailing newline instead of doubling it", () => {
    expect(appendBodyMarkdown("text\n", "![a](u)")).toBe("text\n![a](u)");
  });
});

describe("referencedAttachmentIds", () => {
  it("binds uploads whose link survives in the body (any server URL form)", () => {
    const a = att({ id: "att-1" });
    const b = att({
      id: "att-2",
      filename: "spec.pdf",
      content_type: "application/pdf",
      markdown_url: "",
    });
    const body = [
      "text",
      `![chart.png](${a.markdown_url})`,
      `[spec.pdf](/api/attachments/att-2/download)`,
    ].join("\n");
    expect(referencedAttachmentIds([a, b], body)).toEqual(["att-1", "att-2"]);
  });

  it("unbinds an upload whose reference line the user deleted (web MUL-5181 parity)", () => {
    const a = att({ id: "att-1" });
    const b = att({
      id: "att-2",
      filename: "spec.pdf",
      content_type: "application/pdf",
      markdown_url: "https://public.example/api/attachments/att-2/download",
    });
    const body = `![chart.png](${a.markdown_url})`;
    expect(referencedAttachmentIds([a, b], body)).toEqual(["att-1"]);
  });

  it("returns no ids on an empty body", () => {
    expect(referencedAttachmentIds([att()], "")).toEqual([]);
  });
});
