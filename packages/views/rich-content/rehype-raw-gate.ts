import type { Element, ElementContent, Root, RootContent } from "hast";
import type { VFile } from "vfile";
import rehypeKatex from "rehype-katex";
import rehypeRaw from "rehype-raw";

const rawTransform = rehypeRaw();
const katexTransform = rehypeKatex();
const SAFE_INLINE_TAGS = new Set(["strong", "em", "del", "code"]);
const SKIP_SAFE_INLINE_TRANSFORMS_DATA_KEY = "multicaSkipSafeInlineTransforms";

function isSafeInlineContent(node: ElementContent): boolean {
  if (node.type === "text") return true;
  if (node.type !== "element" || !SAFE_INLINE_TAGS.has(node.tagName)) return false;
  return hasNoProperties(node) && node.children.every(isSafeInlineContent);
}

function hasNoProperties(node: Element): boolean {
  return Object.keys(node.properties).length === 0;
}

function canSkipRawTransform(tree: Root): boolean {
  if (
    tree.children.length === 0 ||
    !tree.children.every(
      (node: RootContent) =>
        node.type === "element" && node.tagName === "p" && hasNoProperties(node),
    )
  ) {
    return false;
  }

  return tree.children.every(
    (node: RootContent) => node.type === "element" && node.children.every(isSafeInlineContent),
  );
}

export function rehypeRawWhenNeeded() {
  return (tree: Root, file: VFile): Root => {
    if (canSkipRawTransform(tree)) {
      // rehype-raw adds this root metadata even when the tree has no raw nodes.
      tree.data = { ...tree.data, quirksMode: false } as Root["data"];
      file.data[SKIP_SAFE_INLINE_TRANSFORMS_DATA_KEY] = true;
      return tree;
    }
    return rawTransform(tree, file) as Root;
  };
}

export function rehypeKatexWhenNeeded() {
  return (tree: Root, file: VFile): Root | undefined => {
    if (file.data[SKIP_SAFE_INLINE_TRANSFORMS_DATA_KEY] === true) return tree;
    return katexTransform(tree, file) as Root | undefined;
  };
}
