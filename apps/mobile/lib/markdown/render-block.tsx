/**
 * Block token renderer. Each branch returns one block-level View (or null
 * to skip). Block tokens form the top level of the marked AST plus the
 * children of blockquote / list-item.
 *
 * Tables: V2.3. For now we render them as a single muted line "[table]"
 * so the user sees the content exists but knows it's not laid out — same
 * approach as inline image degradation.
 */
import type { Token, Tokens } from "marked";
import { View } from "react-native";
import { Text } from "@/components/ui/text";
import { cn } from "@/lib/utils";
import {
  BLOCK_GAP,
  BODY_CLASS,
  HEADING_CLASS,
  HEADING_TOP_GAP,
  HR_CLASS,
  LIST_INDENT,
  PARAGRAPH_LEADING,
  QUOTE_BORDER_CLASS,
} from "./tokens";
import { renderInline } from "./render-inline";
import { CodeBlock } from "./code-block";
import { MarkdownImage } from "./markdown-image";
import { nodeKey } from "./key";

interface Ctx {
  /** List nesting depth — 0 at top level, +1 per nested list. Used for
   *  paddingLeft on nested list children so indentation reads. */
  listDepth: number;
}

const ROOT_CTX: Ctx = { listDepth: 0 };

export function renderBlocks(
  tokens: Token[],
  parentPath: number[] = [],
  ctx: Ctx = ROOT_CTX,
): React.ReactNode {
  return tokens.map((token, i) => {
    const path = [...parentPath, i];
    return (
      <View key={nodeKey(path)}>{renderBlock(token, path, ctx)}</View>
    );
  });
}

function renderBlock(
  token: Token,
  path: number[],
  ctx: Ctx,
): React.ReactNode {
  switch (token.type) {
    case "space":
      return null;
    case "paragraph": {
      const t = token as Tokens.Paragraph;
      return (
        <View className={BLOCK_GAP}>
          <Text className={cn(BODY_CLASS, PARAGRAPH_LEADING)}>
            {renderInline(t.tokens, path)}
          </Text>
        </View>
      );
    }
    case "heading": {
      const t = token as Tokens.Heading;
      const cls = HEADING_CLASS[t.depth] ?? HEADING_CLASS[6];
      return (
        <View className={cn(HEADING_TOP_GAP, BLOCK_GAP)}>
          <Text className={cn(cls, "text-foreground")}>
            {renderInline(t.tokens, path)}
          </Text>
        </View>
      );
    }
    case "blockquote": {
      const t = token as Tokens.Blockquote;
      return (
        <View className={cn(QUOTE_BORDER_CLASS, BLOCK_GAP)}>
          {renderBlocks(t.tokens, path, ctx)}
        </View>
      );
    }
    case "code": {
      const t = token as Tokens.Code;
      return (
        <View className={BLOCK_GAP}>
          <CodeBlock code={t.text} lang={t.lang} />
        </View>
      );
    }
    case "hr":
      return <View className={cn(HR_CLASS, BLOCK_GAP)} />;
    case "list": {
      const t = token as Tokens.List;
      return (
        <View className={BLOCK_GAP}>
          {t.items.map((item, i) =>
            renderListItem(item, t, i, [...path, i], ctx),
          )}
        </View>
      );
    }
    case "image": {
      // Standalone block-level image (rare — usually wrapped in paragraph).
      const t = token as Tokens.Image;
      return (
        <View className={BLOCK_GAP}>
          <MarkdownImage uri={t.href} alt={t.text} />
        </View>
      );
    }
    case "table": {
      // Card-per-row layout — phone screens can't fit a multi-column grid
      // without horizontal scroll, and horizontal-scroll tables get lost
      // on touch. The card form turns each row into "header: value" pairs
      // stacked vertically (responsive design pattern endorsed by every
      // mobile markdown renderer we surveyed).
      const t = token as Tokens.Table;
      return (
        <View className={cn(BLOCK_GAP, "gap-2")}>
          {t.rows.map((row, ri) => (
            <View
              key={ri}
              className="bg-muted/50 rounded-md p-3 gap-1.5"
            >
              {row.map((cell, ci) => {
                const header = t.header[ci];
                if (!header) return null;
                return (
                  <View
                    key={ci}
                    className="flex-row items-start gap-2"
                  >
                    <Text className="text-xs text-muted-foreground font-medium shrink-0 w-20">
                      {header.text}
                    </Text>
                    <Text
                      className={cn(BODY_CLASS, "flex-1")}
                    >
                      {renderInline(cell.tokens, [...path, ri, ci])}
                    </Text>
                  </View>
                );
              })}
            </View>
          ))}
        </View>
      );
    }
    case "html": {
      // Most HTML is stripped by preprocess.ts:stripHtml before the lexer
      // sees it. What lands here is residual / unparseable / weird block
      // HTML that slipped past the regex. Render the raw text in a muted
      // tone so nothing disappears, but it visually deprioritises since
      // it's almost certainly an editor artifact.
      const text = (token as Tokens.HTML).text.trim();
      if (!text) return null;
      return (
        <View className={BLOCK_GAP}>
          <Text className="text-xs text-muted-foreground italic">
            {text}
          </Text>
        </View>
      );
    }
    default: {
      // Generic / unknown block type. Try to fall back to its tokens or
      // raw text so content is never silently lost.
      const generic = token as { tokens?: Token[]; raw?: string };
      if (generic.tokens) {
        return renderBlocks(generic.tokens, path, ctx);
      }
      return (
        <Text className={cn(BODY_CLASS, PARAGRAPH_LEADING, BLOCK_GAP)}>
          {generic.raw ?? ""}
        </Text>
      );
    }
  }
}

function renderListItem(
  item: Tokens.ListItem,
  parent: Tokens.List,
  index: number,
  path: number[],
  ctx: Ctx,
): React.ReactNode {
  const bullet = item.task
    ? item.checked
      ? "☑ "
      : "☐ "
    : parent.ordered
      ? `${(typeof parent.start === "number" ? parent.start : 1) + index}. `
      : "•  ";
  const childCtx: Ctx = { listDepth: ctx.listDepth + 1 };

  // List items can contain a mix of inline-only content (most common —
  // marked emits a single `text` token with `.tokens`) and nested blocks
  // (paragraph, list, code, etc.). Detect: if the only block is a `text`
  // token with inline tokens, render inline; otherwise dispatch to
  // renderBlocks for nested blocks.
  const inlineOnly =
    item.tokens.length === 1 &&
    item.tokens[0]?.type === "text" &&
    Array.isArray((item.tokens[0] as Tokens.Text).tokens);

  return (
    <View
      key={nodeKey(path)}
      className="flex-row mb-1"
      style={{ paddingLeft: ctx.listDepth * LIST_INDENT }}
    >
      <Text className={cn(BODY_CLASS, "shrink-0 w-6")}>{bullet}</Text>
      <View className="flex-1">
        {inlineOnly ? (
          <Text className={cn(BODY_CLASS, PARAGRAPH_LEADING)}>
            {renderInline(
              (item.tokens[0] as Tokens.Text).tokens,
              [...path, 0],
            )}
          </Text>
        ) : (
          renderBlocks(item.tokens, path, childCtx)
        )}
      </View>
    </View>
  );
}
