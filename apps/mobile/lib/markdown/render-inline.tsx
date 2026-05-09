/**
 * Inline token renderer. EVERY return value must be a `<Text>` (or
 * a fragment of `<Text>`s) — never a `<View>`, `<Pressable>`, or
 * `<Image>`. This is RN's hard rule: a `<Text>` may only contain other
 * `<Text>`s. Composition examples:
 *
 *     <Text>Hello <Text className="font-bold">world</Text>!</Text>
 *
 * Mention chips and links also return `<Text>` (with onPress) so they
 * compose into the parent paragraph cleanly.
 *
 * Inline image degradation: an `image` token inside a paragraph is
 * rendered as literal "[image: alt]" since `<Image>` can't be inline.
 * V2.2 will add an AST pre-pass that pulls inline images out into block
 * siblings before this renderer ever sees them.
 */
import { Fragment, type ReactNode } from "react";
import type { Token, Tokens } from "marked";
import { Text } from "@/components/ui/text";
import { INLINE_CODE_CLASS } from "./tokens";
import { MarkdownLink } from "./link";
import { nodeKey } from "./key";

export function renderInline(
  tokens: Token[] | undefined,
  parentPath: number[],
): ReactNode {
  if (!tokens || tokens.length === 0) return null;
  return tokens.map((token, i) => {
    const path = [...parentPath, i];
    return (
      <Fragment key={nodeKey(path)}>{renderInlineToken(token, path)}</Fragment>
    );
  });
}

function renderInlineToken(token: Token, path: number[]): ReactNode {
  switch (token.type) {
    case "text": {
      const t = token as Tokens.Text;
      // Some text tokens carry their own nested inline tokens (e.g. when
      // mixing escapes). Recurse if present, else just emit raw text.
      if (t.tokens && t.tokens.length > 0) {
        return renderInline(t.tokens, path);
      }
      return t.text;
    }
    case "escape":
      return (token as Tokens.Escape).text;
    case "strong": {
      const t = token as Tokens.Strong;
      return (
        <Text className="font-semibold">{renderInline(t.tokens, path)}</Text>
      );
    }
    case "em": {
      const t = token as Tokens.Em;
      return <Text className="italic">{renderInline(t.tokens, path)}</Text>;
    }
    case "del": {
      const t = token as Tokens.Del;
      return (
        <Text className="line-through">{renderInline(t.tokens, path)}</Text>
      );
    }
    case "codespan": {
      const t = token as Tokens.Codespan;
      // selectable so users can long-press to copy. Same caveat as
      // CodeBlock: iOS only offers "Copy" (whole text), Android offers
      // range selection.
      return (
        <Text className={INLINE_CODE_CLASS} selectable>
          {t.text}
        </Text>
      );
    }
    case "br":
      return "\n";
    case "link": {
      const t = token as Tokens.Link;
      return (
        <MarkdownLink href={t.href} fallbackText={t.text}>
          {renderInline(t.tokens, path)}
        </MarkdownLink>
      );
    }
    case "image": {
      // Inline image inside a paragraph — RN can't inline an Image into a
      // Text. Render a textual placeholder. Block-level standalone images
      // are handled in render-block.tsx, not here.
      const t = token as Tokens.Image;
      return `[image${t.text ? `: ${t.text}` : ""}]`;
    }
    case "html": {
      // Residual inline HTML (most stripped by preprocess.ts:stripHtml).
      // Render the inner text only — never the raw `<tag>` markers.
      const t = token as Tokens.Tag;
      return t.text ?? "";
    }
    default:
      // Unknown inline token — fall back to its raw text so content is
      // never silently dropped.
      return (token as { raw?: string }).raw ?? "";
  }
}
