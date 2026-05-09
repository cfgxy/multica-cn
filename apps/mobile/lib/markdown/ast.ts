/**
 * AST transforms applied between marked.lexer and the renderer.
 *
 * Today: `promoteInlineImages` — pull image tokens out of paragraph
 * containers so they can be rendered as block-level <Image> components.
 *
 * Why this exists:
 *   marked.lexer always wraps `![alt](url)` in a paragraph (CommonMark
 *   says image is an inline construct, no exceptions). On web that's
 *   fine because <img> is inline-friendly under HTML/CSS. On RN, an
 *   <Image> is a <View> and CANNOT be a child of a <Text>, so the
 *   inline renderer would have to degrade every image to literal
 *   "[image]" text — making no image render at all.
 *
 *   This pass walks the token tree and:
 *     - Paragraph contains ONLY image tokens (and pure-whitespace text):
 *       drop the paragraph, emit each image as a sibling block. Common
 *       case: a comment that's a single screenshot.
 *     - Paragraph mixes images with real text: split the paragraph at
 *       each image. Emit `[text-before-paragraph, image, text-after-paragraph]`.
 *     - Paragraph has no images: pass through unchanged.
 *
 *   Recurse into containers (blockquote, list, list_item) so images
 *   inside `> ![](url)` or `- ![](url)` also get promoted.
 *
 * Output is the same set of leaf content as the input — no images
 * dropped, no text dropped — just restructured into a tree the RN
 * renderer can map to native primitives.
 */
import type { Token, Tokens } from "marked";

function isWhitespaceText(t: Token): boolean {
  return (
    t.type === "text" && /^\s*$/.test((t as Tokens.Text).text ?? "")
  );
}

function splitParagraph(para: Tokens.Paragraph): Token[] {
  const inline = para.tokens ?? [];
  const hasImage = inline.some((t) => t.type === "image");
  if (!hasImage) return [para];

  // Pure-images-only path (most common: standalone screenshots).
  const onlyImagesAndWhitespace = inline.every(
    (t) => t.type === "image" || isWhitespaceText(t),
  );
  if (onlyImagesAndWhitespace) {
    return inline.filter((t) => t.type === "image");
  }

  // Mixed inline content: split the paragraph at image boundaries so
  // each image becomes its own block, with text-only paragraph fragments
  // before and after.
  const out: Token[] = [];
  let buffer: Token[] = [];
  const flushBuffer = () => {
    if (buffer.length === 0) return;
    if (buffer.every(isWhitespaceText)) {
      buffer = [];
      return;
    }
    out.push({
      type: "paragraph",
      raw: "",
      text: "",
      tokens: buffer,
    } as Tokens.Paragraph);
    buffer = [];
  };
  for (const t of inline) {
    if (t.type === "image") {
      flushBuffer();
      out.push(t);
    } else {
      buffer.push(t);
    }
  }
  flushBuffer();
  return out;
}

export function promoteInlineImages(tokens: Token[]): Token[] {
  const out: Token[] = [];
  for (const token of tokens) {
    if (token.type === "paragraph") {
      out.push(...splitParagraph(token as Tokens.Paragraph));
      continue;
    }
    if (token.type === "blockquote") {
      const bq = token as Tokens.Blockquote;
      out.push({ ...bq, tokens: promoteInlineImages(bq.tokens) });
      continue;
    }
    if (token.type === "list") {
      const list = token as Tokens.List;
      out.push({
        ...list,
        items: list.items.map((item) => ({
          ...item,
          tokens: promoteInlineImages(item.tokens),
        })),
      });
      continue;
    }
    out.push(token);
  }
  return out;
}
