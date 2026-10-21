import type { Root } from "mdast";

interface MutableNode {
  type: string;
  value?: unknown;
  children?: MutableNode[];
  data?: unknown;
}

function isCurrencyProse(value: string): boolean {
  return /^\d[\d,.]*(?:\s|\/)[\s\S]*[A-Za-z]/.test(value);
}

function restoreCurrencyNodes(node: MutableNode): void {
  if (node.type === "inlineMath" && typeof node.value === "string" && isCurrencyProse(node.value)) {
    node.type = "text";
    node.value = `$${node.value}$`;
    delete node.data;
    return;
  }

  node.children?.forEach(restoreCurrencyNodes);
}

export function remarkRestoreCurrencyText() {
  return (tree: Root) => restoreCurrencyNodes(tree as MutableNode);
}
