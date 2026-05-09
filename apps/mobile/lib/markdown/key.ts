/**
 * Stable React key generator for markdown AST traversal.
 *
 * Walks pass `path: number[]` (e.g. `[2, 1]` for the 2nd inline token of the
 * 3rd block) and we join into `"2-1"`. Path stays stable across re-renders
 * unless the AST structure changes — which is when we WANT new keys, so
 * React reconciles correctly.
 */
export function nodeKey(path: number[]): string {
  return path.join("-");
}
